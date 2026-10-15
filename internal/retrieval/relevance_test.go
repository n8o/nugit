package retrieval

import (
	"fmt"
	"strings"
	"testing"
)

// manyRender adds n render-scoped decisions ADR-N000.. to the setup() store.
func manyRender(t *testing.T, dir string, n int) {
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("ADR-N%03d", i)
		body := "a render decision"
		if i%10 == 9 {
			body = "a render decision about caching"
		}
		wf(t, dir, ".nugit/decisions/"+id+".md", obj(id, "decision", "render", body, ""))
	}
}

// The relevance cap stops the section at DefaultMaxDecisions whatever the
// budget, counts what it cut, and says so in the footer (ADR-0043). The CLI
// path (no MaxChars / MaxBudgetTokens) is never clamped.
func TestRelevanceCapIsIndependentOfBudget(t *testing.T) {
	dir := setup(t)
	manyRender(t, dir, 40)
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", BudgetTokens: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Decisions) != DefaultMaxDecisions {
		t.Fatalf("want %d decisions, got %d", DefaultMaxDecisions, len(b.Decisions))
	}
	// 40 + ADR-R render + ADR-G global (no task: globals are in scope).
	if want := 42 - DefaultMaxDecisions; b.Omitted["decision"] != want {
		t.Errorf("omitted %v, want %d decisions", b.Omitted, want)
	}
	if b.Clamped || b.BudgetTokens != 100000 {
		t.Errorf("CLI path must not clamp: clamped=%v budget=%d", b.Clamped, b.BudgetTokens)
	}
	if ids(b.Decisions)["ADR-G"] {
		t.Error("a global decision must not displace component-scoped ones under the cap")
	}
	md := b.Markdown()
	if !strings.Contains(md, "30 more in-scope decisions omitted") {
		t.Errorf("markdown footer must name the omission:\n%s", md)
	}
}

// Within a tier, a task-matching decision outranks a non-matching one, so the
// cap keeps the ones the task is about.
func TestTaskMatchRanksFirstWithinTier(t *testing.T) {
	dir := setup(t)
	manyRender(t, dir, 40)
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go", Task: "fix caching"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if !strings.HasSuffix(b.Decisions[i].ID, "9") {
			t.Errorf("decision %d = %s; want the caching decisions (ADR-N__9) first: %v", i, b.Decisions[i].ID, b.Decisions)
		}
	}
}

func TestRelevanceCapsAreConfigurable(t *testing.T) {
	dir := setup(t)
	manyRender(t, dir, 10)
	wf(t, dir, ".nugit/config.yml", "retrieval:\n  max_decisions: 3\n")
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Decisions) != 3 || b.Omitted["decision"] != 9 {
		t.Errorf("max_decisions: 3 → want 3 kept / 9 omitted, got %d / %v", len(b.Decisions), b.Omitted)
	}
}

// A superseded decision scoped to the component was served directly (only the
// traversal excluded it). Known-wrong is never live context, by either route.
func TestSupersededDecisionNeverSurfacesDirectly(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/decisions/new.md", strings.Replace(
		obj("ADR-R2", "decision", "render", "render decision, revised", ""),
		"provenance:", "supersedes: ADR-R\nprovenance:", 1))
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	d := ids(b.Decisions)
	if d["ADR-R"] || !d["ADR-R2"] {
		t.Errorf("want ADR-R2 and not superseded ADR-R: %v", d)
	}
}

// A one-hop pull from a seed the cap omitted is not admitted: a hop from a
// record too weak to show is weaker still.
func TestHopsOnlyFromShownSeeds(t *testing.T) {
	dir := setup(t)
	wf(t, dir, ".nugit/config.yml", "retrieval:\n  max_decisions: 1\n")
	// ADR-R sorts first (component scope, id order before ADR-Y); ADR-Y is cut
	// by the cap and links to ADR-U, which must therefore not be pulled.
	wf(t, dir, ".nugit/decisions/y.md", obj("ADR-Y", "decision", "render", "another", "see:ADR-U"))
	b, err := Context(Options{RepoDir: dir, Path: "internal/render/render.go"})
	if err != nil {
		t.Fatal(err)
	}
	if d := ids(b.Decisions); d["ADR-U"] || !d["ADR-R"] {
		t.Errorf("want ADR-R only, no hop from omitted ADR-Y: %v", d)
	}
}
