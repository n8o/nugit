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
