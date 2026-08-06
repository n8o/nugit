package modelfacts

import (
	"os/exec"
	"strings"
	"testing"
)

// git runs git in dir with identity/signing pinned so the test is hermetic.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	base := []string{"-C", dir,
		"-c", "user.name=test", "-c", "user.email=test@example.com",
		"-c", "commit.gpgsign=false", "-c", "init.defaultBranch=main"}
	if out, err := exec.Command("git", append(base, args...)...).CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// unitDirs indexes an inventory by directory.
func unitDirs(units []Unit) map[string]Unit {
	got := map[string]Unit{}
	for _, u := range units {
		got[u.Dir] = u
	}
	return got
}

// realTree writes the committed shape shared by the tracking tests: a
// deployable C++ service plus a Go package.
func realTree(t *testing.T, root string) {
	t.Helper()
	write(t, root, "apps/svc/CMakeLists.txt",
		"add_executable(svc main.cpp)\ninstall(TARGETS svc RUNTIME DESTINATION bin)\n")
	write(t, root, "docker/Dockerfile.svc",
		"FROM runtime\nCOPY --from=builder build/apps/svc/svc /usr/local/bin/\n")
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	write(t, root, "pkg/kept/kept.go", "package kept\n\nfunc Kept() {}\n")
}

// scratchTree writes an untracked copy of the repo's own shape — the shape a
// working directory picks up from a stray merge-test checkout or a build output
// dir. Its Dockerfile deliberately sorts BEFORE the real one and names the same
// source dir, which is how the pilot's real units ended up citing evidence
// inside a scratch copy.
func scratchTree(t *testing.T, root, dir string) {
	t.Helper()
	write(t, root, dir+"/docker/Dockerfile.a_svc",
		"FROM runtime\nCOPY --from=builder build/apps/svc/svc /usr/local/bin/\n")
	write(t, root, dir+"/libs/ghost/CMakeLists.txt", "add_library(ghost STATIC ghost.cpp)\n")
	write(t, root, dir+"/pkg/ghost/ghost.go", "package ghost\n\nfunc Ghost() {}\n")
}

// Detection must consider only what git tracks: an untracked scratch tree
// mints no units, and cannot steal a real unit's evidence either.
func TestUnitsAdmitOnlyTrackedEvidence(t *testing.T) {
	root := t.TempDir()
	realTree(t, root)
	git(t, root, "init")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "real tree")
	scratchTree(t, root, "merge-test") // written AFTER the commit: untracked

	got := unitDirs(Units(root))
	if u, ok := got["apps/svc"]; !ok || u.Kind != "deployable" {
		t.Fatalf("the tracked deployable unit must still be detected, got %+v", got)
	} else if u.Evidence != "dockerfile docker/Dockerfile.svc" {
		t.Errorf("evidence must cite the TRACKED Dockerfile, got %q", u.Evidence)
	}
	if _, ok := got["pkg/kept"]; !ok {
		t.Errorf("the tracked Go package must still be detected, got %+v", got)
	}
	for dir := range got {
		if strings.HasPrefix(dir, "merge-test/") {
			t.Errorf("untracked scratch tree must mint no units, got %q", dir)
		}
	}
}

// A gitignored tree is not tracked either — same rule, no special case.
func TestUnitsSkipIgnoredTrees(t *testing.T) {
	root := t.TempDir()
	realTree(t, root)
	write(t, root, ".gitignore", "generated/\n")
	git(t, root, "init")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "real tree")
	scratchTree(t, root, "generated")

	for dir := range unitDirs(Units(root)) {
		if strings.HasPrefix(dir, "generated/") {
			t.Errorf("ignored tree must mint no units, got %q", dir)
		}
	}
}

// A nested `git worktree` checkout is tracked by ITS OWN index, never the
// parent's — so it contributes nothing to the parent's inventory.
func TestUnitsSkipNestedWorktree(t *testing.T) {
	root := t.TempDir()
	realTree(t, root)
	git(t, root, "init")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "real tree")
	git(t, root, "worktree", "add", "-q", "-b", "side", "wt/copy")

	got := unitDirs(Units(root))
	if _, ok := got["apps/svc"]; !ok {
		t.Fatalf("the parent's own units must survive, got %+v", got)
	}
	for dir := range got {
		if strings.HasPrefix(dir, "wt/") {
			t.Errorf("a nested worktree must mint no units in the parent, got %q", dir)
		}
	}
}

// Degrade safely #1: outside a git repo there is no tracking information, so
// detection keeps its filesystem behaviour. Finding nothing would be worse
// than finding too much.
func TestUnitsFallBackOutsideGitRepo(t *testing.T) {
	root := t.TempDir()
	realTree(t, root)
	scratchTree(t, root, "merge-test")

	got := unitDirs(Units(root))
	if _, ok := got["apps/svc"]; !ok {
		t.Fatalf("a non-git directory must still detect units, got %+v", got)
	}
	if _, ok := got["merge-test/libs/ghost"]; !ok {
		t.Errorf("with no tracking information every candidate is admitted, got %+v", got)
	}
}

// Degrade safely #2: the git call itself failing (here: no git binary on PATH)
// is indistinguishable from "no tracking information" — same fallback.
func TestUnitsFallBackWhenGitFails(t *testing.T) {
	root := t.TempDir()
	realTree(t, root)
	git(t, root, "init")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-m", "real tree")
	scratchTree(t, root, "merge-test")

	t.Setenv("PATH", "") // git becomes unresolvable
	got := unitDirs(Units(root))
	if _, ok := got["apps/svc"]; !ok {
		t.Fatalf("a failed git call must not silence detection, got %+v", got)
	}
	if _, ok := got["merge-test/libs/ghost"]; !ok {
		t.Errorf("a failed git call falls back to the filesystem walk, got %+v", got)
	}
}

// Units must inventory deployable containers, CMake target dirs, and Go
// package dirs, deduped by directory with deployable evidence winning.
func TestUnitsInventoriesDetectors(t *testing.T) {
	root := t.TempDir()
	write(t, root, "apps/foo_service/CMakeLists.txt",
		"add_executable(foo_service main.cpp)\ninstall(TARGETS foo_service RUNTIME DESTINATION bin)\n")
	write(t, root, "docker/Dockerfile.foo_service",
		"FROM runtime\nCOPY --from=builder build/apps/foo_service/foo_service /usr/local/bin/\n")
	write(t, root, "libs/bar/CMakeLists.txt", "add_library(bar STATIC bar.cpp)\n")
	write(t, root, "go.mod", "module example.com/m\n\ngo 1.25\n")
	write(t, root, "pkg/baz/baz.go", "package baz\n\nfunc Baz() {}\n")
	write(t, root, "pkg/onlytest/x_test.go", "package onlytest\n") // test-only: not a unit

	units := Units(root)
	got := map[string]Unit{}
	for _, u := range units {
		got[u.Dir] = u
	}
	if u, ok := got["apps/foo_service"]; !ok || u.Kind != "deployable" {
		t.Errorf("apps/foo_service should be a deployable unit (deploy beats cmake on dedup), got %+v", got)
	}
	if u, ok := got["libs/bar"]; !ok || u.Kind != "cmake" {
		t.Errorf("libs/bar should be a cmake unit, got %+v", got)
	}
	if u, ok := got["pkg/baz"]; !ok || u.Kind != "go" {
		t.Errorf("pkg/baz should be a go unit, got %+v", got)
	}
	if _, ok := got["pkg/onlytest"]; ok {
		t.Error("a dir with only _test.go files is not a unit")
	}
	if _, ok := got["docker"]; ok {
		t.Error("the docker/ image dir is not a source unit")
	}
}

// Unmodeled: a path-mapped unit and a name-matched element are both modeled;
// everything else is drift.
func TestUnmodeledFiltersMappedAndNamed(t *testing.T) {
	units := []Unit{
		{Dir: "libs/mapped", Name: "mapped", Kind: "cmake"},
		{Dir: "libs/foo_lib", Name: "foo_lib", Kind: "cmake"},
		{Dir: "libs/orphan", Name: "orphan", Kind: "cmake"},
	}
	resolve := func(dir string) string {
		if dir == "p/libs/mapped" {
			return "mapped_c"
		}
		return ""
	}
	// foo-lib matches libs/foo_lib after _ -> - normalization: an element
	// that exists but lacks paths is a binding gap, not absence.
	out := Unmodeled(units, "p/", resolve, []string{"foo-lib", "Something Else"})
	if len(out) != 1 || out[0].Dir != "libs/orphan" {
		t.Fatalf("want only libs/orphan unmodeled, got %+v", out)
	}
}
