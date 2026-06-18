package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// seed creates a two-package Go repo with a C4 model. If declareEdge is true the
// model declares compa -> compb; otherwise it does not.
func seed(t *testing.T, declareEdge bool) (dir, base string) {
	t.Helper()
	dir = t.TempDir()
	git(t, dir, "init", "-q")
	write(t, dir, "go.mod", "module example.com/demo\n\ngo 1.25\n")
	write(t, dir, "a/a.go", "package a\n\n// A does a thing.\nfunc A() {}\n")
	write(t, dir, "b/b.go", "package b\n\nfunc B() {}\n")
	edge := ""
	if declareEdge {
		edge = "      compa -> compb \"uses\"\n"
	}
	dsl := "workspace \"demo\" {\n  model {\n    s = softwareSystem \"demo\" {\n" +
		"      compa = component \"A\" { properties { paths \"a/**\" } }\n" +
		"      compb = component \"B\" { properties { paths \"b/**\" } }\n" +
		edge +
		"    }\n  }\n}\n"
	write(t, dir, ".nugit/architecture/workspace.dsl", dsl)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")
	return dir, git(t, dir, "rev-parse", "HEAD")
}

func hasC4Fail(rep model.Report) *model.Finding {
	for i := range rep.Findings {
		f := &rep.Findings[i]
		if f.Check == "c4<->code" && f.Severity == model.SevFail {
			return f
		}
	}
	return nil
}

// AC2: an undeclared cross-component import is flagged as a fail.
func TestUndeclaredEdgeFails(t *testing.T) {
	dir, base := seed(t, false)
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat: a uses b")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	f := hasC4Fail(rep)
	if f == nil {
		t.Fatalf("expected c4<->code fail; findings=%+v", rep.Findings)
	}
	if !strings.Contains(f.Title, "compa") || !strings.Contains(f.Title, "compb") {
		t.Errorf("finding should name compa→compb, got %q", f.Title)
	}
	if rep.Significance.Tier != model.TierArchitectural {
		t.Errorf("undeclared edge should be architectural, got %s", rep.Significance.Tier)
	}
}

// AC1: the same import passes clean when the model declares the edge.
func TestDeclaredEdgePasses(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, "a/a.go", "package a\n\nimport _ \"example.com/demo/b\"\n\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "feat: a uses b (declared)")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if f := hasC4Fail(rep); f != nil {
		t.Fatalf("declared edge should not fail; got %q", f.Title)
	}
}

// AC3: a tiny single-component change is trivial.
func TestTrivialChange(t *testing.T) {
	dir, base := seed(t, false)
	write(t, dir, "a/a.go", "package a\n\n// A does a slightly different thing.\nfunc A() {}\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "docs: tweak comment")
	head := git(t, dir, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Significance.Tier != model.TierTrivial {
		t.Errorf("comment tweak should be trivial, got %s (%v)", rep.Significance.Tier, rep.Significance.Reasons)
	}
	if hasC4Fail(rep) != nil {
		t.Error("comment tweak should not produce a c4<->code fail")
	}
}
