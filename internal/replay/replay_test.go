package replay

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type fixture struct {
	t    *testing.T
	dir  string
	tick time.Time
}

func (f *fixture) git(args ...string) string {
	f.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", f.dir}, args...)...)
	date := f.tick.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func (f *fixture) write(rel, content string) {
	f.t.Helper()
	p := filepath.Join(f.dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// commit stages everything and commits one day after the previous commit, so
// every timestamp (and therefore churn_90d) is deterministic.
func (f *fixture) commit(msg string) string {
	f.t.Helper()
	f.tick = f.tick.Add(24 * time.Hour)
	f.git("add", "-A")
	f.git("commit", "-q", "-m", msg)
	return f.git("rev-parse", "HEAD")
}

func decision(id, scope, extra, body string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: " + scope +
		"\nstatus: accepted\ncreated: 2026-01-01T00:00:00Z\n" + extra +
		"provenance:\n  commit: seed\nconfidence: high\n---\n\n" + body
}

const dsl = `workspace "w" "" {
    model {
        s = softwareSystem "s" "" {
            app = container "app" "" "Go" {
                core = component "Core" "" "Go" {
                    properties {
                        "paths" "src/**"
                    }
                }
            }
        }
    }
}
`

func beadLine(id, title, status, desc, closeReason string) string {
	s := `{"id":"` + id + `","title":"` + title + `","status":"` + status + `","issue_type":"task","description":"` + desc + `"`
	if closeReason != "" {
		s += `,"close_reason":"` + closeReason + `","closed_at":"2026-01-09T00:00:00Z"`
	}
	return s + "}\n"
}

// buildRepo lays out the history the export mines:
//
//	c1 store + model + code + plan        (gr-12-1 open, description at base)
//	c2 unrelated code change              ← base of gr-12-1
//	c3 feat: the gr-12-1 solution (squash) — code + test + doc + a NEW decision
//	c4 chore(beads): close gr-12-1        — plan only; edits the description
//	c5 docs: gr-12-3                      — docs only
//	m  merge of a side branch whose commit names gr-12-5
//	f  a later follow-up that names gr-12-1 again (must not widen its range)
//	r1 r2 two adjacent commits naming gr-12-6 (one contiguous range)
func buildRepo(t *testing.T) (dir string, shas map[string]string) {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), tick: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	f.git("init", "-q", "-b", "main")
	shas = map[string]string{}

	f.write(".nugit/architecture/workspace.dsl", dsl)
	f.write(".nugit/decisions/0001-one.md", decision("ADR-0001", "global", "",
		"# ADR-0001 — One\n\n## Decision\n\nDo one.\n\n## Rejected\n\n- Do two: it costs too much.\n"))
	f.write(".nugit/decisions/0002-two.md", decision("ADR-0002", "global", "", "# ADR-0002 — Two\n\nNo alternatives.\n"))
	f.write(".nugit/decisions/0003-three.md", decision("ADR-0003", "core", "", "# ADR-0003 — Three\n"))
	f.write("src/calc.go", "package calc\n\nfunc Add(a, b int) int { return a + b }\n")
	f.write(".beads/plans/gr-12.jsonl",
		beadLine("gr-12", "Calculator epic", "closed", "The epic.", "")+
			beadLine("gr-12-1", "Add Sub", "open", "Add Sub to calc. Done when calc_test passes.", "")+
			beadLine("gr-12-2", "Never implemented", "closed", "Nothing.", "dropped")+
			beadLine("gr-12-3", "Write docs", "open", "Document calc.", "")+
			beadLine("gr-12-4", "Still open", "open", "Later.", "")+
			beadLine("gr-12-5", "Handle zero", "open", "Handle zero.", "")+
			beadLine("gr-12-6", "Multiply", "open", "Add Mul.", ""))
	shas["c1"] = f.commit("chore: seed")

	f.write("src/calc.go", "package calc\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n")
	shas["c2"] = f.commit("refactor(calc): document Add")

	f.write("src/calc.go", "package calc\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n\n// Sub subtracts.\nfunc Sub(a, b int) int { return a - b }\n")
	f.write("src/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {}\n")
	f.write("README.md", "calc\n")
	f.write(".nugit/decisions/0004-four.md", decision("ADR-0004", "core", "relates_to:\n  - informs:ADR-0002\n  - constrains:core\n",
		"# ADR-0004 — Four\n\n## Rejected\n\n- Something the solution invented.\n"))
	shas["c3"] = f.commit("feat(calc): add Sub (closes gr-12-1)\n\nPer ADR-0001 and ADR-0004; ADR-0099 does not exist.\n\ndecision: add Sub\nlearned: x\nkeywords: calc")

	f.write(".beads/plans/gr-12.jsonl",
		beadLine("gr-12", "Calculator epic", "closed", "The epic.", "")+
			beadLine("gr-12-1", "Add Sub", "closed", "LEAKED: Sub is a - b in calc.go.", "implemented Sub")+
			beadLine("gr-12-2", "Never implemented", "closed", "Nothing.", "dropped")+
			beadLine("gr-12-3", "Write docs", "closed", "Document calc.", "done")+
			beadLine("gr-12-4", "Still open", "open", "Later.", "")+
			beadLine("gr-12-5", "Handle zero", "closed", "Handle zero.", "done")+
			beadLine("gr-12-6", "Multiply", "closed", "Add Mul.", "done"))
	shas["c4"] = f.commit("chore(beads): close gr-12-1")

	f.write("docs/calc.md", "# calc\n")
	shas["c5"] = f.commit("docs: describe calc (bead gr-12-3)")

	f.git("checkout", "-q", "-b", "side")
	f.write("src/zero.go", "package calc\n\nfunc IsZero(a int) bool { return a == 0 }\n")
	f.write("tests/zero_test.py", "def test_zero():\n    pass\n")
	shas["side"] = f.commit("fix(calc): handle zero\n\nBead gr-12-5.")
	f.git("checkout", "-q", "main")
	f.tick = f.tick.Add(24 * time.Hour)
	f.git("merge", "-q", "--no-ff", "-m", "Merge branch 'side'", "side")
	shas["merge"] = f.git("rev-parse", "HEAD")

	f.write("src/calc.go", "package calc\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n\n// Sub subtracts b from a.\nfunc Sub(a, b int) int { return a - b }\n")
	shas["f"] = f.commit("fix(calc): tidy Sub, a follow-up to gr-12-1 per ADR-0003")

	f.write("src/mul.go", "package calc\n")
	shas["r1"] = f.commit("feat(calc): stub Mul (gr-12-6)")
	f.write("src/mul.go", "package calc\n\nfunc Mul(a, b int) int { return a * b }\n")
	shas["r2"] = f.commit("feat(calc): implement Mul (gr-12-6)")
	return f.dir, shas
}

func TestExportTasks(t *testing.T) {
	dir, shas := buildRepo(t)
	tasks, rep, err := Export(Options{RepoDir: dir, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("want 3 tasks, got %d: %+v (report %+v)", len(tasks), tasks, rep)
	}
	// Newest solution first.
	mul, zero, sub := tasks[0], tasks[1], tasks[2]
	if mul.ID != "gr-12-6" || zero.ID != "gr-12-5" || sub.ID != "gr-12-1" {
		t.Fatalf("order = %v", ids(tasks))
	}

	// --- the squash layout, every field ---
	if want := "Add Sub\n\nAdd Sub to calc. Done when calc_test passes.\n\n" + PromptSuffix; sub.Prompt != want {
		t.Errorf("prompt = %q, want %q (the bead at BASE, never the tip's edited text)", sub.Prompt, want)
	}
	if strings.Contains(sub.Prompt, "LEAKED") {
		t.Error("prompt read the tip's post-implementation description")
	}
	if sub.Path != "src/calc.go" {
		t.Errorf("path = %q", sub.Path)
	}
	if sub.Base != shas["c2"] {
		t.Errorf("base = %s, want c2 %s", sub.Base, shas["c2"])
	}
	if sub.Verify != "" {
		t.Errorf("verify = %q, want empty", sub.Verify)
	}
	wantSrc := Source{
		Bead: "gr-12-1", Commits: []string{shas["c3"], shas["c4"], shas["f"]}, Solution: shas["c3"],
		Layout: "squash", Span: 1, SolutionTime: "2026-01-04T00:00:00Z", PromptRef: "base",
		ClosedAt: "2026-01-09T00:00:00Z", CloseReason: "implemented Sub",
	}
	if !reflect.DeepEqual(sub.Source, wantSrc) {
		t.Errorf("source = %+v\nwant     %+v", sub.Source, wantSrc)
	}
	if want := []string{"README.md", "src/calc.go"}; !reflect.DeepEqual(sub.SolutionFiles, want) {
		t.Errorf("solution_files = %v, want %v (no .nugit/ or .beads/ paths, no tests)", sub.SolutionFiles, want)
	}
	if want := []string{"src/calc_test.go"}; !reflect.DeepEqual(sub.HiddenTests, want) {
		t.Errorf("hidden_tests = %v", sub.HiddenTests)
	}
	// ADR-0001: cited in the message and present at base. ADR-0002: the
	// relates_to target of a decision the solution added. ADR-0004: cited, but
	// the solution WROTE it — it did not exist at base. ADR-0099: never existed.
	if want := []string{"ADR-0001", "ADR-0002"}; !reflect.DeepEqual(sub.OracleKeys, want) {
		t.Errorf("oracle_keys = %v, want %v", sub.OracleKeys, want)
	}
	wantTags := Tags{Kind: "feat", Files: 2, DecisionDensity: 3, DecisionDensityScoped: 1,
		Churn90d: 2, HasRejected: true, HasHiddenTests: true}
	if sub.Tags != wantTags {
		t.Errorf("tags = %+v\nwant   %+v", sub.Tags, wantTags)
	}
	if len(sub.Rejected) != 1 || sub.Rejected[0].Key != "ADR-0001" ||
		!strings.Contains(sub.Rejected[0].Text, "it costs too much") {
		t.Errorf("rejected = %+v, want only ADR-0001's prose (never the added ADR-0004's)", sub.Rejected)
	}

	// --- the merge layout ---
	if zero.Base != shas["c5"] || zero.Source.Solution != shas["merge"] || zero.Source.Layout != "merge" {
		t.Errorf("merge task: base=%s solution=%s layout=%s", zero.Base, zero.Source.Solution, zero.Source.Layout)
	}
	if zero.Tags.Kind != "fix" {
		t.Errorf("merge kind = %q, want fix (from the side commit; the merge subject is not conventional)", zero.Tags.Kind)
	}
	if !reflect.DeepEqual(zero.HiddenTests, []string{"tests/zero_test.py"}) || zero.Path != "src/zero.go" {
		t.Errorf("merge task: path=%s hidden=%v", zero.Path, zero.HiddenTests)
	}
	if zero.OracleKeys == nil || zero.Rejected == nil {
		t.Error("empty lists must serialize as [], never null")
	}

	// --- a contiguous range ---
	if mul.Base != shas["f"] || mul.Source.Solution != shas["r2"] || mul.Source.Layout != "range" || mul.Source.Span != 2 {
		t.Errorf("range task: base=%s solution=%s layout=%s span=%d", mul.Base, mul.Source.Solution, mul.Source.Layout, mul.Source.Span)
	}

	// --- accounting ---
	wantSkip := map[string]int{SkipNoLinkedCommit: 2, SkipDocsOnly: 1} // gr-12 (epic), gr-12-2; gr-12-3
	if !reflect.DeepEqual(rep.Skipped, wantSkip) {
		t.Errorf("skipped = %v, want %v", rep.Skipped, wantSkip)
	}
	if rep.Closed != 6 || rep.NotClosed != 1 || rep.Emitted != 3 {
		t.Errorf("report = %+v", rep)
	}
}

func TestExportFilters(t *testing.T) {
	dir, _ := buildRepo(t)
	tasks, rep, err := Export(Options{RepoDir: dir, Branch: "main", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].ID != "gr-12-6" || rep.Filtered["limit"] != 2 {
		t.Errorf("limit: %v %+v", ids(tasks), rep.Filtered)
	}
	tasks, rep, _ = Export(Options{RepoDir: dir, Branch: "main", Since: time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)})
	if len(tasks) != 2 || tasks[1].ID != "gr-12-5" || rep.Filtered["since"] != 1 {
		t.Errorf("since: %v %+v", ids(tasks), rep.Filtered)
	}
	// Default branch resolution falls through to `main` with no remote.
	tasks, rep, _ = Export(Options{RepoDir: dir, RequireTests: true})
	if len(tasks) != 2 || rep.Branch != "main" || rep.Filtered["require-tests"] != 1 {
		t.Errorf("require-tests/default branch: %v on %q", ids(tasks), rep.Branch)
	}
}

func ids(ts []Task) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.ID)
	}
	return out
}

func TestIsTestFile(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/x/x_test.go":     true,
		"web/a.test.tsx":           true,
		"web/a.spec.js":            true,
		"pkg/test_thing.py":        true,
		"pkg/thing_test.py":        true,
		"src/FooTest.cpp":          true,
		"src/foo_test.cc":          true,
		"tests/fixtures/data.json": true,
		"spec/models/user.rb":      true,
		"internal/x/x.go":          false,
		"web/a.tsx":                false,
		"pkg/testing.py":           false,
		"src/contest.cpp":          false,
		"src/latest/thing.go":      false,
	} {
		if got := IsTestFile(p); got != want {
			t.Errorf("IsTestFile(%q) = %v, want %v", p, got, want)
		}
	}
}
