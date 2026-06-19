package cmake

import (
	"os"
	"path/filepath"
	"testing"
)

func wf(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "libs/core/CMakeLists.txt", "add_library(core core.cpp)\n")
	wf(t, dir, "libs/util/CMakeLists.txt", "add_library(util util.cpp)\ntarget_link_libraries(util PUBLIC core)\n")
	wf(t, dir, "apps/svc/CMakeLists.txt",
		"add_executable(svc main.cpp)\n"+
			"target_link_libraries(svc\n  PRIVATE\n  core\n  util\n  nlohmann_json::json\n  ${SOME_VAR})\n")
	wf(t, dir, "third_party/dep/CMakeLists.txt", "add_library(vendored x.cpp)\n") // must be skipped

	g, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]bool{}
	for _, d := range g.Dirs {
		dirs[d] = true
	}
	for _, w := range []string{"apps/svc", "libs/core", "libs/util"} {
		if !dirs[w] {
			t.Errorf("missing component dir %q; got %v", w, g.Dirs)
		}
	}
	if dirs["third_party/dep"] {
		t.Error("third_party must be skipped")
	}
	edges := map[[2]string]bool{}
	for _, e := range g.Edges {
		edges[e] = true
	}
	// multi-line link + keyword stripping + external/var filtering
	if !edges[[2]string{"apps/svc", "libs/core"}] || !edges[[2]string{"apps/svc", "libs/util"}] {
		t.Errorf("missing apps/svc edges; got %v", g.Edges)
	}
	if !edges[[2]string{"libs/util", "libs/core"}] {
		t.Errorf("missing util->core edge; got %v", g.Edges)
	}
	if len(g.Edges) != 3 {
		t.Errorf("want exactly 3 edges (external/var dropped), got %v", g.Edges)
	}
}
