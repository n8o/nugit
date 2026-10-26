package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// record builds a store file with the three sections the queue shows.
func record(id, kind, status string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: " + kind + "\nscope: engine\n" +
		"status: " + status + "\ncreated: 2026-01-02T00:00:00Z\nconfidence: high\n" +
		"provenance:\n  commit: seed\n---\n\n# " + id + " — a decision with a title\n\n" +
		"## Context\n\nThe lane rotted because deciding meant reading front-matter.\n\n" +
		"## Decision\n\nServe the lane over loopback and write the store on a click.\n\n" +
		"## Rejected\n\nA rendered report — it cannot close the loop.\n"
}

func seedRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, ".nugit/config.yml", "schema_version: 1\n")
	write(t, dir, ".nugit/decisions/0001-one.md", record("ADR-0001", "decision", "proposed"))
	write(t, dir, ".nugit/decisions/0002-two.md", record("ADR-0002", "decision", "accepted"))
	write(t, dir, ".nugit/lessons/a-lesson.md", record("LESSON-x", "lesson", "proposed"))
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

func serve(t *testing.T, dir string) (*Server, *httptest.Server) {
	t.Helper()
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func getQueue(t *testing.T, s *Server, ts *httptest.Server) Queue {
	t.Helper()
	res, err := http.Get(ts.URL + "/api/queue?t=" + s.token)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("queue status %d", res.StatusCode)
	}
	var q Queue
	if err := json.NewDecoder(res.Body).Decode(&q); err != nil {
		t.Fatal(err)
	}
	return q
}

func decide(t *testing.T, s *Server, ts *httptest.Server, id, action string) decideResp {
	t.Helper()
	res, err := http.Post(ts.URL+"/api/decide?t="+s.token, "application/json",
		strings.NewReader(`{"id":"`+id+`","action":"`+action+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var r decideResp
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		t.Fatal(err)
	}
	return r
}

// Only proposed records appear, and each arrives with the three sections a
// reviewer decides from. The sections are the whole point of the page: the
// first cut of the extractor anchored `^` without (?m) and returned every one
// of them empty, which looks exactly like a store with no prose in it.
func TestQueueCarriesTheSectionsToDecideFrom(t *testing.T) {
	dir := seedRepo(t)
	s, ts := serve(t, dir)
	q := getQueue(t, s, ts)

	if len(q.Items) != 2 {
		t.Fatalf("want the two proposed records, got %d: %+v", len(q.Items), q.Items)
	}
	byID := map[string]Item{}
	for _, it := range q.Items {
		byID[it.ID] = it
	}
	if _, ok := byID["ADR-0002"]; ok {
		t.Error("an accepted record is not a candidate and must not be listed")
	}
	adr := byID["ADR-0001"]
	for name, got := range map[string]string{
		"context": adr.Context, "decision": adr.Decision, "rejected": adr.Rejected,
	} {
		if got == "" {
			t.Errorf("%s came back empty — the section extractor is broken", name)
		}
	}
	if !strings.Contains(adr.Context, "lane rotted") {
		t.Errorf("context = %q", adr.Context)
	}
	if !strings.Contains(adr.Rejected, "rendered report") {
		t.Errorf("rejected = %q", adr.Rejected)
	}
	// The id is its own column, so the duplicated "ADR-0001 — " prefix is gone.
	if strings.HasPrefix(adr.Title, "ADR-0001") {
		t.Errorf("title still repeats the id: %q", adr.Title)
	}
	if adr.Scope != "engine" || adr.Confidence != "high" || adr.Path == "" {
		t.Errorf("metadata missing: %+v", adr)
	}
}

// The button has to say what it will do, and a lesson does not become
// `accepted`. The answer comes from ratify's own table, not a copy.
func TestRatifiesComesFromRatifysTable(t *testing.T) {
	dir := seedRepo(t)
	s, ts := serve(t, dir)
	for _, it := range getQueue(t, s, ts).Items {
		want := "accepted"
		if it.Type == "lesson" {
			want = "active"
		}
		if it.Ratifies != want {
			t.Errorf("%s (%s) ratifies as %q, want %q", it.ID, it.Type, it.Ratifies, want)
		}
	}
}

// Loopback keeps other machines out; the token keeps other local processes out
// of routes that edit and delete files in the user's repo.
func TestEveryRouteRequiresTheRunToken(t *testing.T) {
	dir := seedRepo(t)
	s, ts := serve(t, dir)
	for _, path := range []string{"/api/queue", "/api/decide"} {
		res, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s without a token = %d, want 403", path, res.StatusCode)
		}
		res2, err := http.Get(ts.URL + path + "?t=" + s.token + "x")
		if err != nil {
			t.Fatal(err)
		}
		res2.Body.Close()
		if res2.StatusCode != http.StatusForbidden {
			t.Errorf("%s with a wrong token = %d, want 403", path, res2.StatusCode)
		}
	}
	// The UI itself is served without one: the token travels in the URL the
	// command prints, so the page has to load before its script can send it.
	res, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Errorf("UI status %d", res.StatusCode)
	}
}

func TestAcceptRatifiesInPlace(t *testing.T) {
	dir := seedRepo(t)
	s, ts := serve(t, dir)

	if r := decide(t, s, ts, "ADR-0001", "accept"); r.Error != "" {
		t.Fatalf("accept failed: %s", r.Error)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".nugit/decisions/0001-one.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "status: accepted") {
		t.Errorf("status not promoted:\n%s", b)
	}
	if strings.Contains(string(b), "status: proposed") {
		t.Error("the proposed line survived")
	}
	// Idempotence: the lane no longer holds it, and a second accept refuses.
	for _, it := range getQueue(t, s, ts).Items {
		if it.ID == "ADR-0001" {
			t.Error("a ratified record is still in the lane")
		}
	}
	if r := decide(t, s, ts, "ADR-0001", "accept"); r.Error == "" {
		t.Error("re-ratifying should refuse")
	}
}

// Withdrawing removes the candidate. There is no `rejected` status, and
// `invalidated` would make stale-knowledge warn forever on the component the
// declined record happened to scope — git history is the archive instead.
func TestDeclineRemovesTheCandidateOnly(t *testing.T) {
	dir := seedRepo(t)
	s, ts := serve(t, dir)
	path := filepath.Join(dir, ".nugit/decisions/0001-one.md")

	if r := decide(t, s, ts, "ADR-0001", "decline"); r.Error != "" {
		t.Fatalf("decline failed: %s", r.Error)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Error("the candidate file is still there")
	}
	// An accepted record is not a candidate and must survive a decline attempt.
	kept := filepath.Join(dir, ".nugit/decisions/0002-two.md")
	r := decide(t, s, ts, "ADR-0002", "decline")
	if r.Error == "" {
		t.Error("declining an accepted record should refuse")
	}
	if !strings.Contains(r.Error, "not proposed") {
		t.Errorf("the refusal should say why: %q", r.Error)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Error("a refused decline must not delete anything")
	}
	// Unknown ids refuse rather than deleting something adjacent.
	if r := decide(t, s, ts, "ADR-9999", "decline"); r.Error == "" {
		t.Error("declining an unknown id should refuse")
	}
}

// What the review changed is reported back, so the footer can name the files
// to commit — the command commits nothing itself.
func TestChangedFilesAreReported(t *testing.T) {
	dir := seedRepo(t)
	s, ts := serve(t, dir)
	decide(t, s, ts, "ADR-0001", "accept")
	decide(t, s, ts, "LESSON-x", "decline")

	q := getQueue(t, s, ts)
	joined := strings.Join(q.Changed, "\n")
	if !strings.Contains(joined, "ratified .nugit/decisions/0001-one.md") {
		t.Errorf("missing the ratified file: %v", q.Changed)
	}
	if !strings.Contains(joined, "removed .nugit/lessons/a-lesson.md") {
		t.Errorf("missing the removed file: %v", q.Changed)
	}
	if len(q.Items) != 0 {
		t.Errorf("the lane should be empty now: %+v", q.Items)
	}
}

// A repo with no store is a clear error at startup, not a running server that
// serves an empty page.
func TestNewRefusesARepoWithNoStore(t *testing.T) {
	if _, err := New(t.TempDir()); err == nil {
		t.Error("expected a refusal when .nugit/ is absent")
	} else if !strings.Contains(err.Error(), "nugit init") {
		t.Errorf("the error should say what to do: %v", err)
	}
}

func TestDeclineRejectsABadAction(t *testing.T) {
	dir := seedRepo(t)
	s, ts := serve(t, dir)
	res, err := http.Post(ts.URL+"/api/decide?t="+s.token, "application/json",
		strings.NewReader(`{"id":"ADR-0001","action":"delete-everything"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d, want 400", res.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nugit/decisions/0001-one.md")); err != nil {
		t.Error("an unknown action must change nothing")
	}
}
