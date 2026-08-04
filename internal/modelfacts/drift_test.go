package modelfacts

import "testing"

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
