package modelfacts

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFactsBundlesInventoryEdgesAndLibs(t *testing.T) {
	root := t.TempDir()
	// A C++ service that links a shared lib.
	write(t, root, "apps/foo_service/CMakeLists.txt",
		"add_executable(foo_service main.cpp)\ntarget_link_libraries(foo_service PRIVATE bar)\ninstall(TARGETS foo_service RUNTIME DESTINATION bin)\n")
	write(t, root, "docker/Dockerfile.foo_service",
		"FROM runtime\nCOPY --from=builder build/apps/foo_service/foo_service /usr/local/bin/\n")
	// A shared library → a component, never a container.
	write(t, root, "libs/bar/CMakeLists.txt", "add_library(bar STATIC bar.cpp)\n")

	b, err := Facts(root)
	if err != nil {
		t.Fatal(err)
	}

	var hasContainer bool
	for _, c := range b.Containers {
		if c.Name == "foo-service" {
			hasContainer = true
		}
	}
	if !hasContainer {
		t.Errorf("expected foo-service container, got %+v", b.Containers)
	}
	if len(b.Libs) != 1 || b.Libs[0] != "libs/bar" {
		t.Errorf("shared libs = %v, want [libs/bar]", b.Libs)
	}
	var hasEdge bool
	for _, e := range b.Edges {
		if e[0] == "apps/foo_service" && e[1] == "libs/bar" {
			hasEdge = true
		}
	}
	if !hasEdge {
		t.Errorf("expected dependency edge apps/foo_service->libs/bar, got %v", b.Edges)
	}
	if b.Instruction == "" {
		t.Error("agent instruction must be present (the do-not-contradict contract)")
	}
}
