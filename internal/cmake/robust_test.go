package cmake

import "testing"

// Comments, strings, and ALIAS/IMPORTED must not produce phantom targets/edges
// or corrupt paren-balancing (P2 review findings).
func TestSanitizeAndKeywords(t *testing.T) {
	files := []File{
		{Dir: "libs/core", Text: "add_library(core core.cpp)\n"},
		{Dir: "apps/svc", Text: `
# target_link_libraries(svc PRIVATE ghost)   # commented out — must NOT be an edge
add_executable(svc main.cpp)
message(STATUS "see target_link_libraries(svc PRIVATE phantom)")
target_link_libraries(svc PRIVATE core "core")  # real link )with a paren in a comment
add_library(My::Alias ALIAS core)
add_library(extlib STATIC IMPORTED)
`},
	}
	g := DiscoverFiles(files)

	edges := map[[2]string]bool{}
	for _, e := range g.Edges {
		edges[e] = true
	}
	if !edges[[2]string{"apps/svc", "libs/core"}] {
		t.Fatalf("real svc->core edge missing: %v", g.Edges)
	}
	if len(g.Edges) != 1 {
		t.Errorf("phantom edges leaked from comment/string: %v", g.Edges)
	}
	dirs := map[string]bool{}
	for _, d := range g.Dirs {
		dirs[d] = true
	}
	// ALIAS (My::Alias) and IMPORTED (extlib) must NOT define components.
	if dirs[""] {
		t.Error("a namespaced ALIAS registered a phantom dir")
	}
	// only libs/core and apps/svc are first-party
	if len(g.Dirs) != 2 {
		t.Errorf("want 2 first-party dirs, got %v", g.Dirs)
	}
}
