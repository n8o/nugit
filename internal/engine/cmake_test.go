package engine

import (
	"testing"

	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/scaffold"
)

func hasCheck(rep model.Report, check string, sev model.Severity) bool {
	for _, f := range rep.Findings {
		if f.Check == check && f.Severity == sev {
			return true
		}
	}
	return false
}

// seedCMake makes a git repo with a CMake project (libs/core, apps/svc, no link
// yet), bootstraps the (edged, enforce) model, and returns (root, baseSHA).
func seedCMake(t *testing.T) (root, base string) {
	t.Helper()
	root = t.TempDir()
	git(t, root, "init", "-q")
	write(t, root, "CMakeLists.txt", "project(demo)\nadd_subdirectory(libs/core)\nadd_subdirectory(apps/svc)\n")
	write(t, root, "libs/core/CMakeLists.txt", "add_library(core core.cpp)\n")
	write(t, root, "libs/core/core.cpp", "int core(){return 0;}\n")
	write(t, root, "apps/svc/CMakeLists.txt", "add_executable(svc main.cpp)\n")
	write(t, root, "apps/svc/main.cpp", "int main(){return 0;}\n")
	if _, err := scaffold.Run(scaffold.Options{RepoDir: root, Mode: "enforce"}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "init + cmake")
	return root, git(t, root, "rev-parse", "HEAD")
}

// A new target_link_libraries link the model doesn't declare must be flagged.
func TestCMakeUndeclaredLinkFlagged(t *testing.T) {
	root, base := seedCMake(t)
	write(t, root, "apps/svc/CMakeLists.txt", "add_executable(svc main.cpp)\ntarget_link_libraries(svc PRIVATE core)\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "feat: svc links core")
	head := git(t, root, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: root, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCheck(rep, "cmake<->code", model.SevFail) {
		t.Fatalf("undeclared CMake link must fail in enforce mode; findings=%+v", rep.Findings)
	}
	if rep.Significance.Tier != model.TierArchitectural {
		t.Errorf("a new cross-component CMake link should be architectural, got %s", rep.Significance.Tier)
	}
}

// The same link is clean once the model declares it (green by construction).
func TestCMakeDeclaredLinkClean(t *testing.T) {
	root, base := seedCMake(t)
	// add the link AND declare it in the model
	write(t, root, "apps/svc/CMakeLists.txt", "add_executable(svc main.cpp)\ntarget_link_libraries(svc PRIVATE core)\n")
	// re-bootstrap the model so it now includes svc->core
	if _, err := scaffold.Run(scaffold.Options{RepoDir: root, Mode: "enforce", Force: true}); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "-A")
	git(t, root, "commit", "-q", "-m", "feat: svc links core (model updated)")
	head := git(t, root, "rev-parse", "HEAD")

	rep, err := BuildReport(Options{RepoDir: root, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rep.Findings {
		if f.Check == "cmake<->code" {
			t.Fatalf("a declared link must be clean; got %q", f.Title)
		}
	}
}
