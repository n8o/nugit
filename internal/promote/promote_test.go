package promote

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ---- fixtures: real temp repos, real git, synthetic names ----

func write(t *testing.T, dir, rel, content string) string {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo makes a real git repo with one commit, so HEAD resolves.
func initRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "README.md", "seed\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "seed")
}

// lesson is a ratified lesson record with a keyword line, the shape `distill`
// mints and the shape the dedup rule reads.
func lesson(id, heading, insight, keywords string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: lesson\nscope: global\nstatus: active\n" +
		"created: 2026-01-01T00:00:00Z\nprovenance:\n  commit: abc123\n  citation: origin note\n---\n\n" +
		"# Lesson — " + heading + "\n\n**Trigger:** something observable broke.\n\n" +
		"**Insight:** " + insight + "\n\n**Keywords:** " + keywords + "\n"
}

func cfgYML(orgRepo, hubName, hubPath string) string {
	s := "schema_version: 1\norg:\n  repo: " + orgRepo + "\n"
	if hubName != "" {
		s += "  hub: " + hubName + "\npeers:\n  - name: " + hubName + "\n    path: " + hubPath + "\n"
	}
	return s
}

// org builds a member repo + a hub repo side by side and returns their dirs.
func org(t *testing.T) (member, hub string) {
	t.Helper()
	root := t.TempDir()
	member, hub = filepath.Join(root, "member"), filepath.Join(root, "hub")
	initRepo(t, member)
	initRepo(t, hub)
	write(t, member, ".nugit/config.yml", cfgYML("member-repo", "hub", "../hub"))
	write(t, hub, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: org-hub\n")
	write(t, hub, ".nugit/lessons/placeholder.md", lesson("LESSON-unrelated",
		"Unrelated thing", "totally different subject matter.", "widget, sprocket"))
	// Commit the hub's own store, so "the hub is dirty" in a test means promote
	// dirtied it and nothing else.
	git(t, hub, "add", "-A")
	git(t, hub, "commit", "-qm", "hub store")
	return member, hub
}

// ---- the happy path ----

func TestPromoteWritesRewrittenProvenanceAsProposed(t *testing.T) {
	member, hub := org(t)
	write(t, member, ".nugit/lessons/retention.md", lesson("LESSON-registry-retention",
		"Registry retention reaps untagged artifacts",
		"the retention policy deletes anything without a protected tag.",
		"registry, retention, artifacts"))

	res, err := Promote(Options{RepoDir: member, ID: "LESSON-registry-retention"})
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if res.DestPath != ".nugit/lessons/retention.md" {
		t.Errorf("DestPath = %q", res.DestPath)
	}
	got, err := os.ReadFile(filepath.Join(hub, filepath.FromSlash(res.DestPath)))
	if err != nil {
		t.Fatalf("hub file: %v", err)
	}
	s := string(got)
	for _, want := range []string{
		"status: proposed",
		"origin_repo: member-repo",
		"origin_path: .nugit/lessons/retention.md",
		"id: LESSON-registry-retention", // ADR-0001: the id is NEVER rewritten
	} {
		if !strings.Contains(s, want) {
			t.Errorf("promoted file missing %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "status: active") {
		t.Errorf("promoted file kept its ratified status:\n%s", s)
	}
	if !strings.Contains(s, "commit: "+res.Commit) || res.Commit == "" {
		t.Errorf("provenance.commit = %q, want the ORIGIN repo's HEAD", res.Commit)
	}
	// The origin's own citation must survive rather than be discarded.
	if !strings.Contains(s, "origin cited: origin note") {
		t.Errorf("origin citation was dropped:\n%s", s)
	}
	// The body is untouched.
	if !strings.Contains(s, "**Insight:** the retention policy deletes anything without a protected tag.") {
		t.Errorf("body changed:\n%s", s)
	}
}

// The boundary this whole feature turns on: promote writes a file and does
// NOTHING else. The hub's working tree must be dirty and its HEAD unmoved.
func TestPromoteNeverInvokesGitInTheHub(t *testing.T) {
	member, hub := org(t)
	write(t, member, ".nugit/lessons/retention.md", lesson("LESSON-registry-retention",
		"Registry retention reaps untagged artifacts",
		"the retention policy deletes anything without a protected tag.",
		"registry, retention, artifacts"))

	headBefore := git(t, hub, "rev-parse", "HEAD")
	countBefore := git(t, hub, "rev-list", "--count", "HEAD")
	statusBefore := git(t, hub, "status", "--porcelain")
	if statusBefore != "" {
		t.Fatalf("fixture hub was already dirty: %q", statusBefore)
	}

	if _, err := Promote(Options{RepoDir: member, ID: "LESSON-registry-retention"}); err != nil {
		t.Fatalf("promote: %v", err)
	}

	if got := git(t, hub, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HUB HEAD MOVED: %s -> %s. promote must never commit in the hub (ADR-0035).", headBefore, got)
	}
	status := git(t, hub, "status", "--porcelain")
	if !strings.Contains(status, ".nugit/lessons/retention.md") {
		t.Errorf("hub status = %q, want the promoted file listed as untracked/uncommitted", status)
	}
	// Nothing staged: promote must not `git add` either.
	if staged := git(t, hub, "diff", "--cached", "--name-only"); staged != "" {
		t.Errorf("HUB INDEX TOUCHED: %q. promote must not stage anything.", staged)
	}
	// And no stray commit anywhere in the hub's history.
	if got := git(t, hub, "rev-list", "--count", "HEAD"); got != countBefore {
		t.Errorf("hub commit count = %s, want %s", got, countBefore)
	}
}

// ---- dedup ----

func TestPromoteRefusesNearDuplicateAndProceedsWithForce(t *testing.T) {
	member, hub := org(t)
	write(t, member, ".nugit/lessons/retention.md", lesson("LESSON-registry-retention",
		"Registry retention reaps untagged artifacts",
		"the retention policy deletes anything without a protected tag.",
		"registry, retention, artifacts"))
	if _, err := Promote(Options{RepoDir: member, ID: "LESSON-registry-retention"}); err != nil {
		t.Fatalf("first promote: %v", err)
	}

	// A DIFFERENT record in the member repo saying substantially the same thing.
	write(t, member, ".nugit/lessons/retention2.md", lesson("LESSON-registry-cleanup",
		"Registry cleanup removes artifacts we needed",
		"the same retention behaviour, discovered again.",
		"registry, retention, cleanup"))

	_, err := Promote(Options{RepoDir: member, ID: "LESSON-registry-cleanup"})
	if err == nil {
		t.Fatal("promote accepted a near-duplicate — the org would accumulate two records for one fact")
	}
	var dup *DuplicateError
	if !asDuplicate(err, &dup) {
		t.Fatalf("error is not a DuplicateError: %v", err)
	}
	if dup.ID != "LESSON-registry-retention" {
		t.Errorf("refusal named %q, want the hub record it duplicates", dup.ID)
	}
	if !strings.Contains(err.Error(), "-force") {
		t.Errorf("refusal does not mention the override: %v", err)
	}

	res, err := Promote(Options{RepoDir: member, ID: "LESSON-registry-cleanup", Force: true})
	if err != nil {
		t.Fatalf("-force did not override the dedup refusal: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(hub, filepath.FromSlash(res.DestPath))); serr != nil {
		t.Errorf("-force promoted but wrote nothing: %v", serr)
	}
}

func asDuplicate(err error, out **DuplicateError) bool {
	d, ok := err.(*DuplicateError)
	if ok {
		*out = d
	}
	return ok
}

// ---- refusals ----

func TestPromoteRefusesUnratified(t *testing.T) {
	member, _ := org(t)
	rec := strings.Replace(lesson("LESSON-draft", "A draft", "not reviewed yet.", "draft, thing"),
		"status: active", "status: proposed", 1)
	write(t, member, ".nugit/lessons/draft.md", rec)

	_, err := Promote(Options{RepoDir: member, ID: "LESSON-draft"})
	if err == nil {
		t.Fatal("promote exported an unratified candidate")
	}
	if !strings.Contains(err.Error(), "ratify") {
		t.Errorf("refusal should point at `nugit ratify`: %v", err)
	}
}

func TestPromoteRefusesUnknownID(t *testing.T) {
	member, _ := org(t)
	if _, err := Promote(Options{RepoDir: member, ID: "LESSON-nope"}); err == nil {
		t.Fatal("promote accepted an id that does not exist locally")
	}
}

func TestPromoteRefusesWithNoHubConfigured(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "member")
	initRepo(t, member)
	write(t, member, ".nugit/config.yml", cfgYML("member-repo", "", ""))
	write(t, member, ".nugit/lessons/a.md", lesson("LESSON-a", "A", "insight.", "a, b"))

	_, err := Promote(Options{RepoDir: member, ID: "LESSON-a"})
	if err == nil {
		t.Fatal("promote wrote somewhere with no hub configured")
	}
	if !strings.Contains(err.Error(), "org.hub") {
		t.Errorf("refusal should name the missing knob: %v", err)
	}
}

func TestPromoteRefusesWhenHubIsNotCheckedOut(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "member")
	initRepo(t, member)
	write(t, member, ".nugit/config.yml", cfgYML("member-repo", "hub", "../hub"))
	write(t, member, ".nugit/lessons/a.md", lesson("LESSON-a", "A", "insight.", "a, b"))

	_, err := Promote(Options{RepoDir: member, ID: "LESSON-a"})
	if err == nil {
		t.Fatal("promote succeeded against an absent hub")
	}
	if !strings.Contains(err.Error(), "not checked out") {
		t.Errorf("refusal should say the hub is absent: %v", err)
	}
}

func TestPromoteRefusesWithoutOrgRepo(t *testing.T) {
	member, _ := org(t)
	write(t, member, ".nugit/config.yml",
		"schema_version: 1\norg:\n  hub: hub\npeers:\n  - name: hub\n    path: ../hub\n")
	write(t, member, ".nugit/lessons/a.md", lesson("LESSON-a", "A", "insight.", "a, b"))

	_, err := Promote(Options{RepoDir: member, ID: "LESSON-a"})
	if err == nil {
		t.Fatal("promote stamped a record it could not attribute")
	}
	if !strings.Contains(err.Error(), "org.repo") {
		t.Errorf("refusal should name org.repo: %v", err)
	}
}

// An id the hub already holds is refused even under -force: ids are stable keys
// (ADR-0001) and two records under one key is a store nobody can reason about.
func TestPromoteIDCollisionIsNotOverridableByForce(t *testing.T) {
	member, hub := org(t)
	write(t, hub, ".nugit/lessons/theirs.md", lesson("LESSON-shared-id",
		"Their version", "the hub's own answer.", "alpha, beta"))
	write(t, member, ".nugit/lessons/mine.md", lesson("LESSON-shared-id",
		"My version", "a completely different subject.", "gamma, delta"))

	for _, force := range []bool{false, true} {
		_, err := Promote(Options{RepoDir: member, ID: "LESSON-shared-id", Force: force})
		if err == nil {
			t.Fatalf("force=%v: promote created a second record under one id", force)
		}
		if !strings.Contains(err.Error(), "stable keys") {
			t.Errorf("force=%v: refusal should cite stable ids: %v", force, err)
		}
	}
}

// The ADR-0032 cross-store kill, arriving by copy instead of by merge.
func TestPromoteRefusesASupersedesThatWouldKillAHubRecord(t *testing.T) {
	member, hub := org(t)
	write(t, hub, ".nugit/decisions/0007-theirs.md",
		"---\nschema_version: 1\nid: ADR-0007\ntype: decision\nscope: global\nstatus: accepted\n"+
			"created: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# ADR-0007 — The hub's own decision\n\nbody\n")
	write(t, member, ".nugit/decisions/0009-mine.md",
		"---\nschema_version: 1\nid: ADR-0009\ntype: decision\nscope: global\nstatus: accepted\n"+
			"created: 2026-01-01T00:00:00Z\nsupersedes: ADR-0007\nprovenance:\n  commit: x\n---\n\n"+
			"# ADR-0009 — Replaces our own 0007\n\nbody\n")

	_, err := Promote(Options{RepoDir: member, ID: "ADR-0009", Force: true})
	if err == nil {
		t.Fatal("promote copied in a record whose supersedes would kill a HUB record")
	}
	if !strings.Contains(err.Error(), "ADR-0032") {
		t.Errorf("refusal should cite the isolation rule it protects: %v", err)
	}
}

// ---- -dry-run ----

func TestDryRunWritesNothing(t *testing.T) {
	member, hub := org(t)
	write(t, member, ".nugit/lessons/retention.md", lesson("LESSON-registry-retention",
		"Registry retention reaps untagged artifacts", "insight.", "registry, retention"))

	res, err := Promote(Options{RepoDir: member, ID: "LESSON-registry-retention", DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if !res.DryRun || res.Content == "" {
		t.Errorf("dry run returned no plan: %+v", res)
	}
	if !strings.Contains(res.Content, "status: proposed") {
		t.Errorf("dry-run content is not what would be written:\n%s", res.Content)
	}
	if _, serr := os.Stat(filepath.Join(hub, filepath.FromSlash(res.DestPath))); serr == nil {
		t.Fatal("DRY RUN WROTE A FILE — -dry-run must be side-effect free")
	}
	if status := git(t, hub, "status", "--porcelain"); status != "" {
		t.Errorf("dry run dirtied the hub: %q", status)
	}
}

// ---- -to ----

func TestPromoteToNamesAnotherConfiguredPeer(t *testing.T) {
	root := t.TempDir()
	member, hub, other := filepath.Join(root, "member"), filepath.Join(root, "hub"), filepath.Join(root, "other")
	for _, d := range []string{member, hub, other} {
		initRepo(t, d)
		write(t, d, ".nugit/config.yml", "schema_version: 1\n")
	}
	write(t, member, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: member-repo\n  hub: hub\npeers:\n"+
			"  - name: hub\n    path: ../hub\n  - name: other\n    path: ../other\n")
	write(t, member, ".nugit/lessons/a.md", lesson("LESSON-a", "A", "insight.", "a, b"))

	res, err := Promote(Options{RepoDir: member, ID: "LESSON-a", To: "other"})
	if err != nil {
		t.Fatalf("-to other: %v", err)
	}
	if res.Hub != "other" {
		t.Errorf("wrote to %q, want other", res.Hub)
	}
	if _, serr := os.Stat(filepath.Join(other, ".nugit/lessons/a.md")); serr != nil {
		t.Errorf("nothing landed in the named peer: %v", serr)
	}
	if _, serr := os.Stat(filepath.Join(hub, ".nugit/lessons/a.md")); serr == nil {
		t.Error("-to also wrote into the default hub")
	}

	if _, err := Promote(Options{RepoDir: member, ID: "LESSON-a", To: "unconfigured"}); err == nil {
		t.Fatal("-to accepted a peer this repo never configured")
	}
}

// ---- an occupied destination ----

func TestOccupiedDestinationNeedsForce(t *testing.T) {
	member, hub := org(t)
	write(t, member, ".nugit/lessons/a.md", lesson("LESSON-a", "A", "insight one.", "alpha, beta"))
	write(t, hub, ".nugit/lessons/a.md", "---\nschema_version: 1\nid: LESSON-other\ntype: lesson\n"+
		"scope: global\nstatus: active\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n"+
		"# Lesson — Something else entirely\n\n**Insight:** unrelated.\n\n**Keywords:** zeta, eta\n")

	if _, err := Promote(Options{RepoDir: member, ID: "LESSON-a"}); err == nil {
		t.Fatal("promote silently overwrote an occupied hub path")
	}
	res, err := Promote(Options{RepoDir: member, ID: "LESSON-a", Force: true})
	if err != nil {
		t.Fatalf("-force: %v", err)
	}
	if !res.Overwrote {
		t.Error("Overwrote not reported")
	}
}

// ---- the rewrite, unit-level ----

func TestRewriteReplacesProvenanceAndPreservesEverythingElse(t *testing.T) {
	src := "---\nschema_version: 1\nid: ADR-0003\ntype: decision\nscope: global\nstatus: accepted\n" +
		"created: 2026-01-01T00:00:00Z\nrelates_to:\n  - constrains:render\n  - informs:ADR-0001\n" +
		"provenance:\n  commit: deadbeef\n  agent: someone\n  citation: a note\nconfidence: high\n---\n\n# ADR-0003 — Title\n\nBody stays.\n"
	got, err := rewrite(src, "member-repo", ".nugit/decisions/0003-x.md", "cafe1234")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"status: proposed",
		"relates_to:\n  - constrains:render\n  - informs:ADR-0001",
		"confidence: high",
		"commit: cafe1234",
		"origin_repo: member-repo",
		"# ADR-0003 — Title\n\nBody stays.\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	// The origin's provenance entries must not survive as live keys.
	if strings.Contains(got, "commit: deadbeef") || strings.Contains(got, "agent: someone") {
		t.Errorf("origin provenance leaked through:\n%s", got)
	}
	if strings.Count(got, "provenance:") != 1 {
		t.Errorf("expected exactly one provenance block:\n%s", got)
	}
}

func TestRewriteRefusesAHeaderItCannotEdit(t *testing.T) {
	if _, err := rewrite("no front matter here\n", "r", "p", "c"); err == nil {
		t.Error("rewrote a file with no front-matter fence")
	}
	if _, err := rewrite("---\nid: X\n---\nbody\n", "r", "p", "c"); err == nil {
		t.Error("rewrote a header with no status line")
	}
}
