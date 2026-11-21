package hygiene

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/usage"
)

func run(t *testing.T, dir string, env []string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

func put(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	_ = os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func lesson(id, created, extra, body string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: lesson\nscope: global\nstatus: active\ncreated: " + created +
		"\nprovenance:\n  commit: x\n" + extra + "---\n\n# Lesson — " + id + "\n\n" + body + "\n"
}

var now = time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)

func commitAt(t *testing.T, dir, msg string, at time.Time) {
	t.Helper()
	d := "GIT_COMMITTER_DATE=" + at.Format(time.RFC3339)
	run(t, dir, []string{d, "GIT_AUTHOR_DATE=" + at.Format(time.RFC3339)}, "add", "-A")
	run(t, dir, []string{d, "GIT_AUTHOR_DATE=" + at.Format(time.RFC3339)}, "commit", "-q", "-m", msg)
}

// Each signal fires on the rule it is about, and on no other.
func TestReport(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, nil, "init", "-q", "-b", "main")
	old := "2026-01-01T00:00:00Z"
	put(t, dir, "internal/store/store.go", "package store\n")
	put(t, dir, ".nugit/lessons/used.md", lesson("LESSON-used", old, "", "see `internal/store/store.go`"))
	put(t, dir, ".nugit/lessons/unused.md", lesson("LESSON-unused", old, "", "nobody asks"))
	put(t, dir, ".nugit/lessons/young.md", lesson("LESSON-young", "2026-11-01T00:00:00Z", "", "too new to judge"))
	put(t, dir, ".nugit/lessons/dead.md", lesson("LESSON-dead", old,
		"applies_to_paths: [\"internal/gone/**\"]\n", "see `internal/store/removed.go` and `other/repo/file.go`"))
	put(t, dir, ".nugit/lessons/fires.md", lesson("LESSON-fires", old,
		"diff_trigger:\n  paths: [\"internal/store/**\"]\n  added: \"Retry\"\ncheck: q?\n", "x"))
	put(t, dir, ".nugit/lessons/silent.md", lesson("LESSON-silent", old,
		"diff_trigger:\n  paths: [\"internal/store/**\"]\n  added: \"NEVER_WRITTEN\"\ncheck: q?\n", "x"))
	commitAt(t, dir, "seed", now.AddDate(0, -6, 0))
	put(t, dir, "internal/store/store.go", "package store\n\nfunc Retry() {}\n")
	commitAt(t, dir, "add retry", now.AddDate(0, 0, -10))

	// The log names served ids from before the window, so it covers it.
	_ = usage.Append(dir, usage.Record{Time: now.AddDate(0, 0, -120), Source: usage.SourceMCP,
		Path: "README.md", Served: []string{"ADR-0001"}})
	_ = usage.Append(dir, usage.Record{Time: now.AddDate(0, 0, -5), Source: usage.SourceMCP,
		Path: "internal/store/store.go", Served: []string{"LESSON-used"}})

	rep, err := Run(Options{RepoDir: dir, Now: now, Ref: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	ids := func(rs []Rule) string {
		var s []string
		for _, r := range rs {
			s = append(s, r.ID)
		}
		return strings.Join(s, ",")
	}
	// fires was matched in the window, used was served, young is too new.
	if got := ids(rep.Unused); got != "LESSON-dead,LESSON-silent,LESSON-unused" {
		t.Errorf("unused = %s", got)
	}
	if got := ids(rep.DeadPaths); got != "LESSON-dead" {
		t.Errorf("dead = %s", got)
	} else if d := rep.DeadPaths[0].Detail; !strings.Contains(d, "internal/gone/**") ||
		!strings.Contains(d, "internal/store/removed.go") || strings.Contains(d, "other/repo") {
		t.Errorf("dead detail names the binding and the cited file, not another tree's path: %s", d)
	}
	if got := ids(rep.NeverFired); got != "LESSON-silent" {
		t.Errorf("never fired = %s", got)
	}
	if rep.RetrievalSince.IsZero() || rep.Rules != 6 || rep.Items != 2 {
		t.Errorf("report header: %+v", rep)
	}
	if md := rep.Markdown(); !strings.Contains(md, "never fired") || !strings.Contains(md, "LESSON-unused") {
		t.Errorf("markdown:\n%s", md)
	}
}

// With no served ids in the log, a plain lesson cannot be called unused: the
// report says so instead of naming every lesson in the store.
func TestNoRetrievalDataNamesNoPlainLesson(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, nil, "init", "-q", "-b", "main")
	put(t, dir, ".nugit/lessons/a.md", lesson("LESSON-a", "2026-01-01T00:00:00Z", "", "x"))
	commitAt(t, dir, "seed", now.AddDate(0, -6, 0))
	rep, err := Run(Options{RepoDir: dir, Now: now, Ref: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unused) != 0 || !rep.RetrievalSince.IsZero() {
		t.Errorf("no data, no verdict: %+v", rep.Unused)
	}
}

// Served ids were first logged less than a day ago. "Unused in 90 days" would
// rest on hours of data, so the list is held, and the report says how much of
// the window the log covers and when it will cover all of it.
func TestUnusedIsHeldUntilTheLogCoversTheWindow(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, nil, "init", "-q", "-b", "main")
	old := "2026-01-01T00:00:00Z"
	put(t, dir, "internal/store/store.go", "package store\n")
	put(t, dir, ".nugit/lessons/a.md", lesson("LESSON-a", old, "", "x"))
	put(t, dir, ".nugit/lessons/b.md", lesson("LESSON-b", old, "", "y"))
	put(t, dir, ".nugit/lessons/item.md", lesson("LESSON-item", old,
		"diff_trigger:\n  paths: [\"internal/store/**\"]\n  added: \"NEVER_WRITTEN\"\ncheck: q?\n", "z"))
	commitAt(t, dir, "seed", now.AddDate(0, -6, 0))
	_ = usage.Append(dir, usage.Record{Time: now.Add(-12 * time.Hour), Source: usage.SourceMCP,
		Path: "internal/store/store.go", Served: []string{"LESSON-a"}})

	rep, err := Run(Options{RepoDir: dir, Now: now, Ref: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unused) != 0 {
		t.Errorf("half a day of served ids cannot call a rule unused in 90 days: %+v", rep.Unused)
	}
	if rep.UnusedHeld != 2 { // LESSON-b and LESSON-item; LESSON-a was served
		t.Errorf("held = %d, want 2", rep.UnusedHeld)
	}
	if rep.CoverageDays != 0.5 || !rep.JudgedFrom.Equal(now.Add(-12*time.Hour).AddDate(0, 0, 90)) {
		t.Errorf("coverage = %v days, judged from %v", rep.CoverageDays, rep.JudgedFrom)
	}
	md := rep.Markdown()
	for _, want := range []string{"held", "0.5 of 90 days", "2 rule(s)", "2027-02-18"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "Nothing to prune") {
		t.Errorf("a held list is not nothing to prune:\n%s", md)
	}
	// The other signals do not rest on the log, and still report.
	if len(rep.NeverFired) != 1 || rep.NeverFired[0].ID != "LESSON-item" {
		t.Errorf("never fired = %+v", rep.NeverFired)
	}
}

// With no served ids at all, a checklist item is not called unused on its
// commit matches alone: unused means neither served nor matched.
func TestNoRetrievalDataHoldsChecklistItems(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, nil, "init", "-q", "-b", "main")
	put(t, dir, "internal/store/store.go", "package store\n")
	put(t, dir, ".nugit/lessons/item.md", lesson("LESSON-item", "2026-01-01T00:00:00Z",
		"diff_trigger:\n  paths: [\"internal/store/**\"]\n  added: \"NEVER_WRITTEN\"\ncheck: q?\n", "z"))
	commitAt(t, dir, "seed", now.AddDate(0, -6, 0))
	rep, err := Run(Options{RepoDir: dir, Now: now, Ref: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Unused) != 0 || rep.UnusedHeld != 1 || rep.CoverageDays != 0 {
		t.Errorf("no served ids: unused=%+v held=%d coverage=%v", rep.Unused, rep.UnusedHeld, rep.CoverageDays)
	}
	if md := rep.Markdown(); !strings.Contains(md, "no served ids yet") {
		t.Errorf("markdown:\n%s", md)
	}
}

// From a nested nugit root a binding to a file that exists is not dead, and
// a cited file that is gone is.
func TestNestedRootDeadPaths(t *testing.T) {
	root := t.TempDir()
	run(t, root, nil, "init", "-q", "-b", "main")
	put(t, root, "README.md", "x\n")
	put(t, root, "apps/op/db/a.sql", "select 1;\n")
	op := filepath.Join(root, "apps", "op")
	put(t, op, ".nugit/lessons/live.md", lesson("LESSON-live", "2026-01-01T00:00:00Z",
		"applies_to_paths: [\"apps/op/db/**\"]\n", "see `db/a.sql`"))
	put(t, op, ".nugit/lessons/gone.md", lesson("LESSON-gone", "2026-01-01T00:00:00Z", "", "see `db/removed.sql`"))
	commitAt(t, root, "seed", now.AddDate(0, -6, 0))
	rep, err := Run(Options{RepoDir: op, Now: now, Ref: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DeadPaths) != 1 || rep.DeadPaths[0].ID != "LESSON-gone" {
		t.Fatalf("dead paths from a nested root: %+v", rep.DeadPaths)
	}
}

// A relative cite names a file by the tail of its path, written from the code
// its author was in. It is dead only when no tracked file has that tail.
func TestRelativeCitesResolveWhereTheirAuthorMeantThem(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, nil, "init", "-q", "-b", "main")
	old := "2026-01-01T00:00:00Z"
	put(t, dir, "tests/smoke.sh", "#!/bin/sh\n") // a top-level tests/ dir exists too
	put(t, dir, "helm/Chart.yaml", "name: acme\n")
	put(t, dir, "k8s/base/kustomization.yaml", "resources: []\n")
	put(t, dir, "apps/acme-panel/src/main.go", "package main\n")
	put(t, dir, "apps/acme-panel/tests/ui/README.md", "x\n")
	put(t, dir, "apps/acme-api/src/k8s/types.ts", "export {}\n")
	put(t, dir, "helm/acme/templates/acme-pool.yaml", "kind: Service\n")
	// A component-scoped lesson citing a path under its component's project dir.
	put(t, dir, ".nugit/lessons/panel.md", lesson("LESSON-panel", old, "",
		"the gestures are listed in `tests/ui/README.md`"))
	// A global lesson citing a file deep in one app.
	put(t, dir, ".nugit/lessons/crd.md", lesson("LESSON-crd", old, "",
		"the field is declared in `k8s/types.ts`"))
	// An elided path is unknown, not dead.
	put(t, dir, ".nugit/lessons/pool.md", lesson("LESSON-pool", old, "",
		"see `helm/.../acme-pool.yaml` and `helm/…/acme-pool.yaml`"))
	// Gone everywhere: still dead.
	put(t, dir, ".nugit/lessons/gone.md", lesson("LESSON-gone", old, "",
		"see `tests/ui/removed.md`"))
	commitAt(t, dir, "seed", now.AddDate(0, -6, 0))

	rep, err := Run(Options{RepoDir: dir, Now: now, Ref: "HEAD"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DeadPaths) != 1 || rep.DeadPaths[0].ID != "LESSON-gone" ||
		!strings.Contains(rep.DeadPaths[0].Detail, "tests/ui/removed.md") {
		t.Errorf("dead paths = %+v, want only LESSON-gone", rep.DeadPaths)
	}
}
