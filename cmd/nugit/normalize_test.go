package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "core.hooksPath=/dev/null"}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

// normalize runs `nugit plan normalize` with its stdout captured.
func normalize(t *testing.T, args ...string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = f
	rc := cmdPlanNormalize(args)
	os.Stdout = old
	f.Close()
	out, _ := os.ReadFile(f.Name())
	if rc != 0 {
		t.Fatalf("plan normalize %v exited %d:\n%s", args, rc, out)
	}
	return string(out)
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// lineOf returns the line of a JSONL file that holds bead id, or "".
func lineOf(src, id string) string {
	for _, l := range strings.Split(src, "\n") {
		if strings.Contains(l, `"id":"`+id+`"`) {
			return l
		}
	}
	return ""
}

// The merge base is nugit's to write (ADR-0064): -write replaces the
// -merge-base file with the next base, and an export that names a plan later
// still carries what an earlier scoped export left out of it.
func TestPlanNormalizeSavesTheNextBase(t *testing.T) {
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	head := map[string]string{
		".beads/plans/acme-rift.jsonl": `{"id":"acme-rift-1","status":"open","title":"cut"}` + "\n" + `{"id":"acme-rift-2","status":"open","title":"seal"}` + "\n",
		".beads/plans/acme-dock.jsonl": `{"id":"acme-dock-1","status":"open","title":"dock"}` + "\n",
	}
	for p, s := range head {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "store")

	base := filepath.Join(t.TempDir(), "base.jsonl")
	if err := os.WriteFile(base, []byte(head[".beads/plans/acme-rift.jsonl"]+head[".beads/plans/acme-dock.jsonl"]), 0o644); err != nil {
		t.Fatal(err)
	}
	// The database: this session closed acme-rift-2; another session started
	// acme-dock-1 and filed acme-dock-2. bd exports it all, as one file.
	export := func() {
		t.Helper()
		for p := range head {
			os.Remove(filepath.Join(dir, p))
		}
		db := `{"id":"acme-rift-1","status":"open","title":"cut"}
{"id":"acme-rift-2","status":"closed","title":"seal"}
{"id":"acme-dock-1","status":"in_progress","title":"dock"}
{"id":"acme-dock-2","status":"open","title":"undock"}
`
		if err := os.WriteFile(filepath.Join(dir, ".beads/store.jsonl"), []byte(db), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dock := filepath.Join(dir, ".beads/plans/acme-dock.jsonl")

	export()
	before := readFile(t, base)
	normalize(t, "-C", dir, "-split", "-merge", "HEAD", "-merge-base", base, "-plans", "acme-rift")
	if readFile(t, base) != before {
		t.Fatal("a dry run rewrote the base")
	}
	out := normalize(t, "-C", dir, "-split", "-merge", "HEAD", "-merge-base", base, "-plans", "acme-rift", "-write")
	if !strings.Contains(out, "saved the next merge base") {
		t.Errorf("no word of the base:\n%s", out)
	}
	if got := readFile(t, dock); got != head[".beads/plans/acme-dock.jsonl"] {
		t.Errorf("a plan outside the scope changed:\n%s", got)
	}
	next := readFile(t, base)
	if l := lineOf(next, "acme-rift-2"); !strings.Contains(l, `"closed"`) {
		t.Errorf("the base does not hold the export's line in scope: %q", l)
	}
	if l := lineOf(next, "acme-dock-1"); !strings.Contains(l, `"open"`) {
		t.Errorf("the base took an edit the export left out: %q", l)
	}
	if l := lineOf(next, "acme-dock-2"); l != "" {
		t.Errorf("the base holds a bead the export did not write: %q", l)
	}

	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", "export")
	export()
	normalize(t, "-C", dir, "-split", "-merge", "HEAD", "-merge-base", base, "-plans", "acme-rift,acme-dock", "-write")
	got := readFile(t, dock)
	if lineOf(got, "acme-dock-2") == "" {
		t.Errorf("the bead filed in the other plan was dropped once the plan was named:\n%s", got)
	}
	if l := lineOf(got, "acme-dock-1"); !strings.Contains(l, `"in_progress"`) {
		t.Errorf("the edit in the other plan was reverted once the plan was named: %q", l)
	}
}
