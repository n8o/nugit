package nudge

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/config"
)

// mustGit runs git in dir with identity/signing pinned so the test is hermetic.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false"}
	cmd := exec.Command("git", append(base, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// write writes a file under dir, creating parents.
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

// repoWithBase creates a git repo with one baseline commit.
func repoWithBase(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	mustGit(t, dir, "init")
	write(t, dir, "README.md", "hello\n")
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "root")
	return dir
}

// stageSignificant stages a change that exceeds the default trivial thresholds
// (3 files, ~60 lines).
func stageSignificant(t *testing.T, dir string) {
	t.Helper()
	lines := strings.Repeat("line of code\n", 20)
	write(t, dir, "internal/alpha/alpha.go", lines)
	write(t, dir, "internal/alpha/alpha_util.go", lines)
	write(t, dir, "internal/beta/beta.go", lines)
	mustGit(t, dir, "add", ".")
}

func nudgeCfg() config.Config {
	c := config.Default()
	c.Capture.CommitMsg = "nudge"
	return c
}

func TestNudgeFiresOnSignificantStagedDiff(t *testing.T) {
	dir := repoWithBase(t)
	stageSignificant(t, dir)

	out := ForStagedCommit(dir, "feat(alpha): add alpha and beta\n", nudgeCfg())
	if out == "" {
		t.Fatal("significant staged diff without trailers must nudge")
	}
	for _, want := range []string{"learned:", "keywords:", "no capture trailer"} {
		if !strings.Contains(out, want) {
			t.Errorf("nudge output missing %q:\n%s", want, out)
		}
	}
	// keywords are seeded from path names when no model maps the files
	if !strings.Contains(out, "alpha") || !strings.Contains(out, "beta") {
		t.Errorf("keywords should be seeded from touched paths:\n%s", out)
	}
}

func TestSilentOnTrivialDiff(t *testing.T) {
	dir := repoWithBase(t)
	write(t, dir, "README.md", "hello\nworld\n") // 1 file, 1 line
	mustGit(t, dir, "add", ".")

	if out := ForStagedCommit(dir, "docs: fix typo\n", nudgeCfg()); out != "" {
		t.Fatalf("trivial staged diff must not nudge, got:\n%s", out)
	}
}

func TestSilentWhenTrailerPresent(t *testing.T) {
	dir := repoWithBase(t)
	stageSignificant(t, dir)

	msg := "feat(alpha): add alpha\n\nlearned: something durable\nkeywords: alpha\n"
	if out := ForStagedCommit(dir, msg, nudgeCfg()); out != "" {
		t.Fatalf("already-trailered commit must not nudge, got:\n%s", out)
	}
}

func TestSilentWhenModeIsNotNudge(t *testing.T) {
	dir := repoWithBase(t)
	stageSignificant(t, dir)

	for _, mode := range []string{"warn", "block", "off"} {
		cfg := config.Default()
		cfg.Capture.CommitMsg = mode
		if out := ForStagedCommit(dir, "feat: x\n", cfg); out != "" {
			t.Errorf("mode %q must not nudge, got:\n%s", mode, out)
		}
	}
}

func TestSilentOnInternalError(t *testing.T) {
	// Not a git repo at all: the bounded git call fails; the nudge must
	// degrade to silence, never to an error or a panic.
	if out := ForStagedCommit(t.TempDir(), "feat: x\n", nudgeCfg()); out != "" {
		t.Fatalf("non-repo must be silent, got:\n%s", out)
	}
}

func TestSilentOnUnbornHeadOrEmptyStage(t *testing.T) {
	// Fresh repo, nothing staged, no HEAD yet.
	dir := t.TempDir()
	mustGit(t, dir, "init")
	if out := ForStagedCommit(dir, "feat: x\n", nudgeCfg()); out != "" {
		t.Fatalf("unborn HEAD / empty stage must be silent, got:\n%s", out)
	}
	// Repo with a commit but a clean index.
	dir2 := repoWithBase(t)
	if out := ForStagedCommit(dir2, "feat: x\n", nudgeCfg()); out != "" {
		t.Fatalf("empty stage must be silent, got:\n%s", out)
	}
}

func TestSilentOnMergeAndFixupSubjects(t *testing.T) {
	dir := repoWithBase(t)
	stageSignificant(t, dir)

	for _, subj := range []string{
		"Merge branch 'feat/x' into main\n",
		"fixup! feat: earlier\n",
		"squash! feat: earlier\n",
	} {
		if out := ForStagedCommit(dir, subj, nudgeCfg()); out != "" {
			t.Errorf("subject %q must not nudge, got:\n%s", strings.TrimSpace(subj), out)
		}
	}
}

func TestSilentWhenOnlyNugitFilesStaged(t *testing.T) {
	dir := repoWithBase(t)
	lines := strings.Repeat("knowledge line\n", 30)
	write(t, dir, ".nugit/decisions/0001-a.md", lines)
	write(t, dir, ".nugit/decisions/0002-b.md", lines)
	write(t, dir, ".nugit/decisions/0003-c.md", lines)
	mustGit(t, dir, "add", ".")

	if out := ForStagedCommit(dir, "docs(nugit): add decisions\n", nudgeCfg()); out != "" {
		t.Fatalf("a .nugit-only commit IS capture and must not be nudged, got:\n%s", out)
	}
}

func TestKeywordsSeededFromModelComponents(t *testing.T) {
	dir := repoWithBase(t)
	write(t, dir, ".nugit/architecture/workspace.dsl", `workspace "t" "t" {
  model {
    s = softwareSystem "s" "s" {
      payments = component "Payments" "d" "Go" {
        properties {
          "paths" "internal/alpha/**"
        }
      }
      ledger = component "Ledger" "d" "Go" {
        properties {
          "paths" "internal/beta/**"
        }
      }
    }
  }
}
`)
	stageSignificant(t, dir)

	out := ForStagedCommit(dir, "feat: cross-component change\n", nudgeCfg())
	if out == "" {
		t.Fatal("expected a nudge")
	}
	if !strings.Contains(out, "payments") || !strings.Contains(out, "ledger") {
		t.Errorf("keywords should be seeded from mapped component ids:\n%s", out)
	}
	if !strings.Contains(out, "span 2 C4 components") {
		t.Errorf("reasons should surface the component span:\n%s", out)
	}
}

func TestSkipsHugeDiffs(t *testing.T) {
	dir := repoWithBase(t)
	for i := 0; i <= maxStagedFiles; i++ { // maxStagedFiles+1 files
		write(t, dir, fmt.Sprintf("bulk/f%04d.txt", i), "x\n")
	}
	mustGit(t, dir, "add", ".")

	if out := ForStagedCommit(dir, "feat: vendored import\n", nudgeCfg()); out != "" {
		t.Fatalf("huge staged diffs must skip the nudge (stay fast), got:\n%s", out)
	}
}
