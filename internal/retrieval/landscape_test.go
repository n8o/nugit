package retrieval

import (
	"encoding/json"
	"strings"
	"testing"
)

// THE PAYOFF CASE (ADR-0034 point 5). Repo A holds the paths that configure a
// shared artifact registry; repo B owns the registry and holds the hard-won
// lesson about its retention behaviour. Before the landscape, A could only see
// B's lesson on a lucky task-keyword match. With it, the org's own artifact says
// that file configures B's system, and the lesson arrives deterministically.

const registryLandscape = `workspace {
  model {
    gateway = softwareSystem "Consumer Gateway" {
      properties { "nugit_repo" "consumer-gateway" }
    }
    registry = softwareSystem "Shared artifact registry" {
      properties {
        "nugit_owner" "producer-service"
        "nugit_paths" "platform/registry/**,deploy/registry/*.yaml"
      }
    }
    gateway -> registry "pulls build artifacts"
  }
}
`

// landscaped builds the two-repo pair: a local repo naming one peer, both with
// declared org identities, and the landscape wherever `where` says.
func landscaped(t *testing.T, where string, peerFiles map[string]string) (local, peer string) {
	t.Helper()
	local, peer = federated(t, peerFiles)
	wf(t, local, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: consumer-gateway\npeers:\n  - name: platform\n    path: "+peer+"\n")
	// The peer declares its OWN org identity — the bilateral fact that joins a
	// system's nugit_owner to a peer namespace (ADR-0033 point 3).
	wf(t, peer, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: producer-service\n")
	switch where {
	case "local":
		wf(t, local, ".nugit/architecture/landscape.dsl", registryLandscape)
	case "peer":
		wf(t, peer, ".nugit/architecture/landscape.dsl", registryLandscape)
	}
	return local, peer
}

func TestSharedSystemSurfacesAndAdmitsTheOwnersLesson(t *testing.T) {
	local, _ := landscaped(t, "local", map[string]string{
		".nugit/lessons/retention.md": statusObj("LESSON-RETENTION", "lesson", "global", "accepted",
			"images vanish after 14 days unless the keep-policy is pinned per-repository", ""),
	})
	// A task whose keywords deliberately do NOT overlap the lesson: without the
	// landscape binding this bundle would not contain it.
	b, err := Context(Options{RepoDir: local, Path: "platform/registry/retention.yaml",
		Task: "adjust scheduling priority"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Landscape) != 1 {
		t.Fatalf("shared system missing from the bundle: %+v", b.Landscape)
	}
	ls := b.Landscape[0]
	if ls.ID != "registry" || ls.Name != "Shared artifact registry" || ls.Owner != "producer-service" {
		t.Errorf("landscape item = %+v", ls)
	}
	if ls.OwnedHere {
		t.Error("this repo is consumer-gateway; it does not own the registry")
	}
	if ls.Origin != "" {
		t.Errorf("a local landscape must not be attributed to a peer: %+v", ls)
	}

	var lesson *Item
	for i := range b.Lessons {
		if b.Lessons[i].ID == "LESSON-RETENTION" {
			lesson = &b.Lessons[i]
		}
	}
	if lesson == nil {
		t.Fatalf("the owner's lesson was not admitted: %+v", b.Lessons)
	}
	if lesson.Origin != "platform" {
		t.Errorf("origin = %q, want the peer namespace", lesson.Origin)
	}
	if lesson.SharedSystem != "registry" {
		t.Errorf("the admission reason must be recorded, got %q", lesson.SharedSystem)
	}
	md := b.Markdown()
	for _, want := range []string{
		"**Shared infrastructure**",
		"Shared artifact registry",
		"producer-service",
		"not this repo",
		"platform:LESSON-RETENTION", // qualified id (ADR-0032)
		"peer platform",             // origin spelled out
		"shared system registry",    // why it is here
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown must contain %q:\n%s", want, md)
		}
	}
}

// The binding is PATH-scoped: an unrelated path in the same repo gets neither
// the shared system nor the keyword-gate bypass.
func TestLandscapeBindingDoesNotLeakToOtherPaths(t *testing.T) {
	local, _ := landscaped(t, "local", map[string]string{
		".nugit/lessons/retention.md": statusObj("LESSON-RETENTION", "lesson", "global", "accepted",
			"images vanish after 14 days unless the keep-policy is pinned", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "internal/render/render.go",
		Task: "adjust scheduling priority"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Landscape) != 0 {
		t.Fatalf("an unrelated path must surface no shared system: %+v", b.Landscape)
	}
	if ids(b.Lessons)["LESSON-RETENTION"] {
		t.Error("a keyword-mismatched peer lesson must stay out without a landscape binding")
	}
}

// Only the OWNING peer's knowledge is admitted. A second peer that owns nothing
// this path configures keeps the ordinary ADR-0032 gate.
func TestOnlyTheOwningPeerIsAdmitted(t *testing.T) {
	local, _ := landscaped(t, "local", map[string]string{
		".nugit/lessons/retention.md": statusObj("LESSON-RETENTION", "lesson", "global", "accepted",
			"images vanish after 14 days unless the keep-policy is pinned", ""),
	})
	other := t.TempDir()
	wf(t, other, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: unrelated-service\n")
	wf(t, other, ".nugit/lessons/x.md", statusObj("LESSON-OTHER", "lesson", "global", "accepted",
		"something entirely unrelated to this path", ""))
	wf(t, local, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: consumer-gateway\npeers:\n"+
			"  - name: platform\n    path: "+peerPathOf(t, local)+"\n"+
			"  - name: other\n    path: "+other+"\n")

	b, err := Context(Options{RepoDir: local, Path: "platform/registry/retention.yaml",
		Task: "adjust scheduling priority"})
	if err != nil {
		t.Fatal(err)
	}
	if !ids(b.Lessons)["LESSON-RETENTION"] {
		t.Fatalf("the owner's lesson must be admitted: %+v", b.Lessons)
	}
	if ids(b.Lessons)["LESSON-OTHER"] {
		t.Errorf("a non-owning peer keeps the ordinary keyword gate: %+v", b.Lessons)
	}
}

// peerPathOf recovers the configured peer path from a repo built by landscaped.
func peerPathOf(t *testing.T, local string) string {
	t.Helper()
	b, err := readFile(local, ".nugit/config.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(b, "\n") {
		if s := strings.TrimSpace(line); strings.HasPrefix(s, "path: ") {
			return strings.TrimPrefix(s, "path: ")
		}
	}
	t.Fatal("no peer path in config")
	return ""
}

// A landscape read from a PEER governs this repo's view identically, and says
// so — the reader must never mistake another repo's org model for its own.
func TestLandscapeFromAPeer(t *testing.T) {
	local, _ := landscaped(t, "peer", map[string]string{
		".nugit/lessons/retention.md": statusObj("LESSON-RETENTION", "lesson", "global", "accepted",
			"images vanish after 14 days unless the keep-policy is pinned", ""),
	})
	b, err := Context(Options{RepoDir: local, Path: "deploy/registry/prod.yaml", Task: "bump replicas"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Landscape) != 1 || b.Landscape[0].Origin != "platform" {
		t.Fatalf("landscape should come from the peer: %+v", b.Landscape)
	}
	if !ids(b.Lessons)["LESSON-RETENTION"] {
		t.Errorf("the owner's lesson must still be admitted: %+v", b.Lessons)
	}
	if !strings.Contains(b.Markdown(), "org landscape, peer platform") {
		t.Errorf("markdown must attribute the landscape to its peer:\n%s", b.Markdown())
	}
}

// Two peers declaring a landscape resolves to NOTHING (ADR-0011): retrieval
// degrades to exactly the pre-landscape bundle rather than picking one.
func TestAmbiguousLandscapeSurfacesNothing(t *testing.T) {
	local, peer := landscaped(t, "peer", map[string]string{
		".nugit/lessons/retention.md": statusObj("LESSON-RETENTION", "lesson", "global", "accepted",
			"images vanish after 14 days unless the keep-policy is pinned", ""),
	})
	second := t.TempDir()
	wf(t, second, ".nugit/architecture/landscape.dsl", registryLandscape)
	wf(t, second, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: another-service\n")
	wf(t, local, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: consumer-gateway\npeers:\n"+
			"  - name: platform\n    path: "+peer+"\n  - name: second\n    path: "+second+"\n")

	b, err := Context(Options{RepoDir: local, Path: "platform/registry/retention.yaml",
		Task: "adjust scheduling priority"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Landscape) != 0 {
		t.Fatalf("ambiguity must fail closed to no landscape: %+v", b.Landscape)
	}
	if ids(b.Lessons)["LESSON-RETENTION"] {
		t.Error("with no landscape there is no binding, so the keyword gate stands")
	}
}

// The section obeys the ADR-0032/0033 budget discipline: it is dropped like any
// other kind, the drop is RECORDED, and it never displaces the spec.
func TestLandscapeRespectsBudgetAndNeverDisplacesTheSpec(t *testing.T) {
	local, _ := landscaped(t, "local", nil)
	wf(t, local, ".nugit/specs/s.md", obj("SPEC-1", "spec", "global", "the active spec", ""))

	b, err := Context(Options{RepoDir: local, Path: "platform/registry/retention.yaml", BudgetTokens: 30})
	if err != nil {
		t.Fatal(err)
	}
	if b.Spec == nil {
		t.Fatal("the spec is part of the mandatory baseline and must survive")
	}
	if len(b.Landscape) != 0 {
		t.Fatalf("the landscape section should not have fit in 30 tokens: %+v", b.Landscape)
	}
	var found bool
	for _, d := range b.Dropped {
		if strings.Contains(d, "landscape registry") {
			found = true
		}
	}
	if !found || !b.Truncated {
		t.Errorf("every cut must be recorded, never silent: truncated=%v dropped=%v", b.Truncated, b.Dropped)
	}
}

// A repo with no landscape anywhere gets a byte-identical bundle: the JSON must
// carry no landscape key at all (ADR-0034 point 2).
func TestNoLandscapeMeansNoBundleChange(t *testing.T) {
	dir := setup(t)
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Landscape) != 0 {
		t.Fatalf("no landscape ⇒ no section: %+v", b.Landscape)
	}
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "landscape") || strings.Contains(string(raw), "shared_system") {
		t.Errorf("landscape keys must be omitted entirely when unused:\n%s", raw)
	}
	if strings.Contains(b.Markdown(), "Shared infrastructure") {
		t.Errorf("markdown gained a section:\n%s", b.Markdown())
	}
}
