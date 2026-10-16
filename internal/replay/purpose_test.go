package replay

import (
	"reflect"
	"sort"
	"testing"
	"time"
)

// buildMultiRepo lays out one history per ADR-0045 shape:
//
//	ab-1, ab-2  one commit implements both (no closing verb) → multi-bead
//	ab-3        its commit also says "Closes #12"            → multi-bead
//	ab-4        "Fixes #13", and the bead itself cites #13   → kept
//	ab-5        bead about the scheduler; the commit also
//	            rewrote the ledger (bigger, so primary) and
//	            the only test is a ledger test               → tests-unrelated
//	ab-6        test next to the code, linked only by the
//	            code's added lines; the message names ab-1
//	            in passing                                   → kept
//	ab-7, ab-7a a step and its lettered sub-step, one commit → kept (one family)
func buildMultiRepo(t *testing.T) string {
	t.Helper()
	f := &fixture{t: t, dir: t.TempDir(), tick: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	f.git("init", "-q", "-b", "main")
	plan := func(status string) string {
		return beadLine("ab-1", "Add the export header", status, "Write a header line on export.", "") +
			beadLine("ab-2", "Add the export footer", status, "Write a footer line on export.", "") +
			beadLine("ab-3", "Rename the flag", status, "Rename -x to -extra.", "") +
			beadLine("ab-4", "Crash on empty input (#13)", status, "Empty input crashes the reader.", "") +
			beadLine("ab-5", "Tune the retry backoff in the scheduler", status, "Backoff doubles without a ceiling; cap it at a minute.", "") +
			beadLine("ab-6", "Make the parser accept trailing commas", status, "A trailing comma in a list is a syntax error today.", "") +
			beadLine("ab-7", "Widen the cache", status, "Grow the cache to 64 entries.", "") +
			beadLine("ab-7a", "Widen the cache: config knob", status, "Expose the size.", "")
	}
	f.write(".beads/plans/ab.jsonl", plan("open"))
	f.write("pkg/export/export.go", "package export\n")
	f.write("pkg/sched/backoff.go", "package sched\n")
	f.write("pkg/ledger/ledger.go", "package ledger\n")
	f.write("pkg/parse/parse.go", "package parse\n")
	f.write("pkg/cache/cache.go", "package cache\n")
	f.write("cmd/main.go", "package main\n")
	f.write("pkg/reader/reader.go", "package reader\n")
	f.commit("chore: seed")

	f.write("pkg/export/export.go", "package export\n\nfunc Header() string { return \"h\" }\n\nfunc Footer() string { return \"f\" }\n")
	f.write("pkg/export/export_test.go", "package export\n\nimport \"testing\"\n\nfunc TestHeaderFooter(t *testing.T) {}\n")
	f.commit("feat(export): header (ab-1) and footer (ab-2)")

	f.write("cmd/main.go", "package main\n\nvar extra = flag.Bool(\"extra\", false, \"\")\n")
	f.write("cmd/main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestExtraFlag(t *testing.T) {}\n")
	f.commit("refactor(cmd): rename the flag (ab-3)\n\nAlso fixes the unrelated timeout.\n\nCloses #12")

	f.write("pkg/reader/reader.go", "package reader\n\nfunc Read(b []byte) int { if len(b) == 0 { return 0 }; return 1 }\n")
	f.write("pkg/reader/reader_test.go", "package reader\n\nimport \"testing\"\n\nfunc TestEmptyInput(t *testing.T) {}\n")
	f.commit("fix(reader): empty input (ab-4)\n\nFixes #13")

	f.write("pkg/sched/backoff.go", "package sched\n\nconst maxDelay = 60\n")
	f.write("pkg/ledger/ledger.go", "package ledger\n\n// Balance sums postings.\nfunc Balance(p []int) int {\n\ts := 0\n\tfor _, x := range p {\n\t\ts += x\n\t}\n\treturn s\n}\n\n// Post appends.\nfunc Post(p []int, x int) []int { return append(p, x) }\n")
	f.write("pkg/ledger/ledger_test.go", "package ledger\n\nimport \"testing\"\n\nfunc TestBalance(t *testing.T) {\n\tif Balance([]int{1, 2}) != 3 {\n\t\tt.Fatal(\"sum of postings\")\n\t}\n}\n")
	f.commit("fix(sched): cap it (ab-5)")

	f.write("pkg/parse/parse.go", "package parse\n\n// Accepts allows one trailing comma before the closing bracket.\nfunc Accepts(s string) bool { return true }\n")
	f.write("pkg/parse/parse_test.go", "package parse\n\nimport \"testing\"\n\nfunc TestList(t *testing.T) {\n\tif !Accepts(\"[1,]\") {\n\t\tt.Fatal(\"x\")\n\t}\n}\n")
	f.commit("feat(parse): closes ab-6\n\nThe same lexer change ab-1 needed.")

	f.write("pkg/cache/cache.go", "package cache\n\nvar Size = 64\n")
	f.write("pkg/cache/cache_test.go", "package cache\n\nimport \"testing\"\n\nfunc TestCacheSize(t *testing.T) {}\n")
	f.commit("feat(cache): widen (closes ab-7)\n\n* beads: close ab-7a")

	f.write(".beads/plans/ab.jsonl", plan("closed"))
	f.commit("chore(beads): close ab-1 ab-2 ab-3 ab-4 ab-5 ab-6 ab-7 ab-7a")
	return f.dir
}

func TestOneBeadOneChange(t *testing.T) {
	dir := buildMultiRepo(t)
	tasks, rep, err := Export(Options{RepoDir: dir, Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	got := ids(tasks)
	sort.Strings(got)
	if want := []string{"ab-4", "ab-6", "ab-7", "ab-7a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kept %v, want %v (report %+v)", got, want, rep)
	}
	// ab-1 + ab-2 (one solution commit for both), ab-3 (closes #12 it never cites).
	if rep.Skipped[SkipMultiBead] != 3 {
		t.Errorf("multi-bead skips = %d, want 3 (%v)", rep.Skipped[SkipMultiBead], rep.Skipped)
	}
	// ab-5: the only hidden test belongs to the ledger, not the scheduler.
	if rep.Skipped[SkipTestsUnrelated] != 1 {
		t.Errorf("tests-unrelated skips = %d, want 1 (%v)", rep.Skipped[SkipTestsUnrelated], rep.Skipped)
	}
	for _, tk := range tasks {
		if tk.Tags.Multi || tk.Tags.TestsUnrelated {
			t.Errorf("%s kept without -allow-multi but tagged %+v", tk.ID, tk.Tags)
		}
	}

	// -allow-multi keeps every one of them, tagged.
	tasks, rep, err = Export(Options{RepoDir: dir, Branch: "main", AllowMulti: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 8 || rep.Skipped[SkipMultiBead] != 0 || rep.Skipped[SkipTestsUnrelated] != 0 {
		t.Fatalf("allow-multi: %v, skipped %v", ids(tasks), rep.Skipped)
	}
	tags := map[string]Tags{}
	for _, tk := range tasks {
		tags[tk.ID] = tk.Tags
	}
	for id, want := range map[string][2]bool{
		"ab-1": {true, false}, "ab-2": {true, false}, "ab-3": {true, false},
		"ab-4": {false, false}, "ab-5": {false, true}, "ab-6": {false, false},
		"ab-7": {false, false}, "ab-7a": {false, false},
	} {
		if got := [2]bool{tags[id].Multi, tags[id].TestsUnrelated}; got != want {
			t.Errorf("%s: multi,tests_unrelated = %v, want %v", id, got, want)
		}
	}
	if p := tasksByID(tasks)["ab-5"].Path; p != "pkg/ledger/ledger.go" {
		t.Errorf("ab-5 primary = %s; the fixture needs the unrelated change to be the larger one", p)
	}
}

func tasksByID(ts []Task) map[string]Task {
	m := map[string]Task{}
	for _, t := range ts {
		m[t.ID] = t
	}
	return m
}

func TestFamily(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"gr-12", "gr-12-1", true},
		{"gr-12-7", "gr-12-7a", true},
		{"GR-12-7A", "gr-12-7", true},
		{"gr-1", "gr-12", false},
		{"gr-12-7a", "gr-12-7b", false},
		{"gr-12-1", "gr-12-2", false},
	} {
		if got := family(c.a, c.b); got != c.want {
			t.Errorf("family(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestClosedRefs(t *testing.T) {
	known := map[string]bool{"xx-1": true, "xx-2": true, "xx-3": true, "xx-3a": true}
	msg := "feat: thing (#40)\n\nCloses xx-1, xx-2 and xx-3a. Follow-up to xx-3.\n\nFixes #7, #8 and owner/repo#9; see #10"
	if got, want := closedBeads(msg, "xx-1", known), []string{"xx-2", "xx-3a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("closedBeads = %v, want %v (never the bead itself, never a passing mention)", got, want)
	}
	if got := closedBeads(msg, "xx-3", known); !reflect.DeepEqual(got, []string{"xx-1", "xx-2"}) {
		t.Errorf("closedBeads(xx-3) = %v: its sub-step xx-3a is family", got)
	}
	if got, want := closedIssues(msg), []string{"7", "8", "9"}; !reflect.DeepEqual(got, want) {
		t.Errorf("closedIssues = %v, want %v (a PR number or a `see` is not a close)", got, want)
	}
	if !refsIssue("Crash on empty input (#13)", "13") || refsIssue("see #130", "13") {
		t.Error("refsIssue must match #13 whole")
	}
}

func TestTestsRelated(t *testing.T) {
	bead := "Cap the `retryBackoff` in the scheduler\n\nIt grows without bound."
	files := []string{"pkg/sched/core.go"} // a path with no bead term
	for _, c := range []struct {
		name   string
		hidden string
		src    string
		added  string
		want   bool
	}{
		{"backticked identifier in content", "pkg/x/x_test.go", "func TestA() { retryBackoff() }", "", true},
		{"distinctive word in content", "pkg/x/x_test.go", "// the scheduler loop", "", true},
		{"word in the test path", "pkg/x/scheduler_test.go", "", "", true},
		{"nothing in common", "pkg/x/x_test.go", "func TestLedger() {}", "", false},
		{"next to primary, diff shares one term only", "pkg/sched/a_test.go", "", "// scheduler\n", false},
		{"next to primary, diff shares two terms", "pkg/sched/a_test.go", "", "// scheduler bound\n", true},
	} {
		got := testsRelated(bead, []string{c.hidden}, map[string]string{c.hidden: c.src}, files, "pkg/sched/core.go", c.added)
		if got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	// Rule 2: the code next to the test is named for the bead.
	if !testsRelated(bead, []string{"pkg/y/y_test.go"}, nil, []string{"pkg/sched/core.go", "pkg/y/scheduler.go"}, "pkg/sched/core.go", "") {
		t.Error("a sibling solution file named for the bead links its test")
	}
	if !testsRelated(bead, nil, nil, files, "pkg/sched/core.go", "") {
		t.Error("no hidden tests is never unrelated")
	}
}
