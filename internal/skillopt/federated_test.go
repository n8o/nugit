package skillopt

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/knowledge"
)

// globalLesson is a federatable peer lesson: `scope: global` + ratified, the
// ADR-0032 admission gate. A component-scoped or proposed one must never enter
// the corpus.
func globalLesson(id, status, body string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: lesson\nscope: global\nstatus: " + status +
		"\ncreated: 2026-07-01T00:00:00Z\nprovenance:\n  commit: seed\n---\n\n" + body
}

func peerLesson(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, ".nugit", "lessons", name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The property that makes the flag safe to add: a corpus generated before
// federation existed must be reproducible byte-for-byte afterwards.
func TestWithoutPeersOutputIsByteIdentical(t *testing.T) {
	local, peer := t.TempDir(), t.TempDir()
	writeLesson(t, local, "a.md", lessonFile("LESSON-a", "active", "", cleanBody))
	peerLesson(t, peer, "b.md", globalLesson("LESSON-b", "active", cleanBody))

	plain, prep, err := Export(Options{RepoDir: local})
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := WriteJSONL(&want, plain); err != nil {
		t.Fatal(err)
	}

	// Configuring a peer changes nothing unless -peers is passed: the Options
	// field is the only switch, and without it nothing reads the peer directory.
	again, arep, err := Export(Options{RepoDir: local})
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := WriteJSONL(&got, again); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Fatalf("non-federated export is not deterministic:\n%s\nvs\n%s", got.String(), want.String())
	}
	if strings.Contains(got.String(), "origin:") {
		t.Errorf("a non-federated case carries an origin label — that changes every existing corpus:\n%s", got.String())
	}
	if prep.Summary.ByOrigin != nil || arep.Summary.ByOrigin != nil {
		t.Error("emitted_by_origin appears in a non-federated report")
	}
	if prep.Summary.Peers != nil {
		t.Error("peers appears in a non-federated report")
	}
}

func TestFederatedExportIncludesPeerLessonsWithOriginLabels(t *testing.T) {
	local, peer := t.TempDir(), t.TempDir()
	writeLesson(t, local, "a.md", lessonFile("LESSON-a", "active", "", cleanBody))
	peerLesson(t, peer, "b.md", globalLesson("LESSON-b", "active", cleanBody))

	cases, rep, err := Export(Options{RepoDir: local,
		Peers: []knowledge.PeerSource{{Name: "platform", Dir: peer, Hub: true}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 2 {
		t.Fatalf("want 2 cases across 2 repos, got %v", numbers(cases))
	}
	byNumber := map[string]Case{}
	for _, c := range cases {
		byNumber[c.Number] = c
	}
	localCase, ok := byNumber["lesson:a"]
	if !ok {
		t.Fatalf("local case number changed under federation: %v", numbers(cases))
	}
	if !hasLabel(localCase, "origin:local") {
		t.Errorf("local case labels = %v, want origin:local", localCase.Labels)
	}
	// A foreign case number carries its origin: identity in a merged set is
	// (origin, id), and the number is what a consumer hashes into a split.
	foreign, ok := byNumber["lesson:platform/b"]
	if !ok {
		t.Fatalf("foreign case number is not origin-qualified: %v", numbers(cases))
	}
	if !hasLabel(foreign, "origin:platform") {
		t.Errorf("foreign case labels = %v, want origin:platform", foreign.Labels)
	}
	if !strings.HasPrefix(foreign.Source, "platform:") {
		t.Errorf("foreign source = %q, want an origin-qualified path (ADR-0032 point 8)", foreign.Source)
	}
	if rep.Summary.ByOrigin["local"] != 1 || rep.Summary.ByOrigin["platform"] != 1 {
		t.Errorf("ByOrigin = %v, want one case from each repo", rep.Summary.ByOrigin)
	}
	if !strings.Contains(rep.SummaryLines(), "federated across 2 origin(s)") {
		t.Errorf("summary does not report the federation:\n%s", rep.SummaryLines())
	}
}

// The ADR-0027 gate is what makes the corpus worth anything, and a foreign
// lesson gets no exemption from it.
func TestLeakageGateStillRefusesForeignLessons(t *testing.T) {
	local, peer := t.TempDir(), t.TempDir()
	writeLesson(t, local, "a.md", lessonFile("LESSON-a", "active", "", cleanBody))
	// A trigger that is a Conventional Commits subject — leak vector 2.
	leaky := "# Lesson — a leaky one\n\n**Trigger:** feat(export): add the thing\n\n" +
		"**Insight:** the thing needed adding.\n\n**Keywords:** thing\n"
	peerLesson(t, peer, "leaky.md", globalLesson("LESSON-leaky", "active", leaky))

	cases, rep, err := Export(Options{RepoDir: local,
		Peers: []knowledge.PeerSource{{Name: "platform", Dir: peer}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if strings.Contains(c.Number, "platform/") {
			t.Fatalf("GATE BYPASSED FOR A FOREIGN LESSON: %s emitted with input %q", c.Number, c.Input)
		}
	}
	var found bool
	for _, r := range rep.Refused {
		if r.Number == "lesson:platform/leaky" {
			found = true
		}
	}
	if !found {
		t.Errorf("the refused foreign lesson is not in the report: %+v", rep.Refused)
	}
}

// The ADR-0032 admission gate: a peer's component-scoped or unratified lesson is
// not ours to export, and does not even appear as a refusal in a report about
// THIS repo's capture hygiene.
func TestPeerAdmissionGateAppliesToTheCorpus(t *testing.T) {
	local, peer := t.TempDir(), t.TempDir()
	writeLesson(t, local, "a.md", lessonFile("LESSON-a", "active", "", cleanBody))
	peerLesson(t, peer, "scoped.md", strings.Replace(
		globalLesson("LESSON-scoped", "active", cleanBody), "scope: global", "scope: delta", 1))
	peerLesson(t, peer, "draft.md", globalLesson("LESSON-draft", "proposed", cleanBody))

	cases, rep, err := Export(Options{RepoDir: local,
		Peers: []knowledge.PeerSource{{Name: "platform", Dir: peer}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 || cases[0].Number != "lesson:a" {
		t.Fatalf("a peer's scoped/unratified lesson entered the corpus: %v", numbers(cases))
	}
	for _, r := range rep.Refused {
		if strings.Contains(r.Number, "platform/") {
			t.Errorf("an inadmissible peer lesson appears as a refusal: %s", r.Number)
		}
	}
}

// An absent peer is the normal CI state and must degrade, not fail.
func TestUnreachablePeerDegradesAndIsReported(t *testing.T) {
	local := t.TempDir()
	writeLesson(t, local, "a.md", lessonFile("LESSON-a", "active", "", cleanBody))

	cases, rep, err := Export(Options{RepoDir: local,
		Peers: []knowledge.PeerSource{{Name: "gone", Dir: filepath.Join(local, "nowhere")}}})
	if err != nil {
		t.Fatalf("an absent peer failed the export: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("local cases lost: %v", numbers(cases))
	}
	if len(rep.Summary.Peers) != 1 || rep.Summary.Peers[0].Reachable {
		t.Fatalf("peer reachability not reported: %+v", rep.Summary.Peers)
	}
	if !strings.Contains(rep.SummaryLines(), "peer gone") {
		t.Errorf("summary does not name the absent peer:\n%s", rep.SummaryLines())
	}
}

func hasLabel(c Case, want string) bool {
	for _, l := range c.Labels {
		if l == want {
			return true
		}
	}
	return false
}
