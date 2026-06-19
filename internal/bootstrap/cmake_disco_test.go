package bootstrap

import (
	"testing"

	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/mapping"
)

func TestDiscoverCMake(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "libs/core/CMakeLists.txt", "add_library(core core.cpp)\n")
	wf(t, dir, "apps/svc/CMakeLists.txt",
		"add_executable(svc m.cpp)\ntarget_link_libraries(svc PRIVATE core nlohmann::json)\n")

	g, err := DiscoverCMake(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Components) != 2 {
		t.Fatalf("want 2 components, got %+v", g.Components)
	}
	if len(g.Edges) != 1 {
		t.Fatalf("want exactly 1 edge (external dropped), got %+v", g.Edges)
	}

	// Green by construction: the generated DSL round-trips and the induced edge
	// is exactly what the c4<->code check would resolve.
	m := c4.Parse(GenerateDSL(g, "x"))
	mp := mapping.New(m)
	src := mp.Resolve("apps/svc/m.cpp")
	dst := mp.ResolveDir("libs/core")
	if src == "" || dst == "" {
		t.Fatalf("cmake components must map files/dirs: src=%q dst=%q", src, dst)
	}
	if !m.HasRelationship(src, dst) {
		t.Errorf("generated model missing the link edge %s -> %s; rels=%+v", src, dst, m.Relationships)
	}
}
