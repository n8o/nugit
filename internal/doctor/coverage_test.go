package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

func cgit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// "68 of 92 components are uncovered" names no first move. Ranking the
// uncovered ones by recent churn does: the component that changed twenty times
// with nothing captured is where the next debugging session starts blind.
func TestThinHotspotsRankUncoveredComponentsByChurn(t *testing.T) {
	dir := t.TempDir()
	cgit(t, dir, "init", "-q")
	write := func(rel, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// hot changes three times, cold once; neither carries scoped knowledge.
	for i := 0; i < 3; i++ {
		write("hot/h.go", strings.Repeat("x", i+1))
		cgit(t, dir, "add", "-A")
		cgit(t, dir, "commit", "-q", "-m", "hot change")
	}
	write("cold/c.go", "y")
	cgit(t, dir, "add", "-A")
	cgit(t, dir, "commit", "-q", "-m", "cold change")

	m := model.Model{Components: []model.Component{
		{ID: "hot", Name: "Hot", Paths: []string{"hot/**"}},
		{ID: "cold", Name: "Cold", Paths: []string{"cold/**"}},
	}}
	got := thinHotspots(dir, m, nil, []string{"hot", "cold"})
	if len(got) != 2 {
		t.Fatalf("want both uncovered components ranked, got %v", got)
	}
	if !strings.HasPrefix(got[0], "hot ") {
		t.Errorf("the most-changed uncovered component must come first, got %v", got)
	}
	// A covered component never appears, however much it churns.
	if only := thinHotspots(dir, m, nil, []string{"cold"}); len(only) != 1 || !strings.HasPrefix(only[0], "cold ") {
		t.Errorf("only orphans are ranked, got %v", only)
	}
	// No orphans, nothing to say.
	if none := thinHotspots(dir, m, nil, nil); none != nil {
		t.Errorf("no orphans should produce no hotspots, got %v", none)
	}
}

// The store-wide edge audit: unusable entries are named, and a store whose
// edges are overwhelmingly bare is told so once rather than 368 times.
func TestEdgeVocabularyCheckReportsShapeNotEveryEdge(t *testing.T) {
	obj := func(id string, edges ...string) model.KnowledgeObject {
		return model.KnowledgeObject{
			FrontMatter: model.FrontMatter{ID: id, RelatesTo: edges},
			Path:        ".nugit/decisions/" + id + ".md",
		}
	}
	var objs []model.KnowledgeObject
	for i := 0; i < 20; i++ {
		objs = append(objs, obj("ADR-"+string(rune('a'+i)), "ADR-0001"))
	}
	objs = append(objs, obj("ADR-bad", "supersedes:ADR-0001", "ammends:ADR-0001"))

	c := edgeVocabularyCheck(objs)
	if c.OK {
		t.Error("unusable edges must make the check not-OK")
	}
	if !c.Advisory {
		t.Error("the vocabulary check stays advisory in a pre-flight")
	}
	for _, want := range []string{"supersedes:ADR-0001", "front-matter", "ammends:ADR-0001", "bare"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("missing %q in: %s", want, c.Detail)
		}
	}
	if !strings.Contains(c.Detail, "almost entirely unexpressed") {
		t.Errorf("a store that is ~90%% bare should say so once: %s", c.Detail)
	}
	// A clean store says so without inventing problems.
	clean := edgeVocabularyCheck([]model.KnowledgeObject{obj("ADR-1", "amends:ADR-0001")})
	if !clean.OK || strings.Contains(clean.Detail, "unusable") {
		t.Errorf("clean store: %+v", clean)
	}
}

// The classification table is a claim that something reads each verb. Guard the
// two that drive status resolution, and the misplaced-field catch.
func TestRelationVocabulary(t *testing.T) {
	if knowledge.RelationOf("amends") != knowledge.RelationSemantic {
		t.Error("amends drives ResolveAmendedBy and must be semantic")
	}
	if knowledge.RelationOf("constrains") != knowledge.RelationScope {
		t.Error("constrains widens scope")
	}
	if knowledge.RelationOf("prevents") != knowledge.RelationAnnotation {
		t.Error("prevents is documented and traversal-only — annotation, not unknown")
	}
	if knowledge.RelationOf("ammends") != knowledge.RelationUnknown {
		t.Error("a typo must be unknown")
	}
	st := knowledge.EdgeAudit([]model.KnowledgeObject{{
		FrontMatter: model.FrontMatter{ID: "X", RelatesTo: []string{"supersedes:Y"}},
		Path:        "p.md",
	}})
	if len(st.Problems) != 1 || st.Problems[0].Misplaced == "" {
		t.Errorf("supersedes as an edge must be reported as misplaced: %+v", st.Problems)
	}
}
