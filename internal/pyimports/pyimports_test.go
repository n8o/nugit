package pyimports

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
	wf(t, dir, "app/main.py", "import lib.core\nfrom lib.util import helper\n")
	wf(t, dir, "lib/core/__init__.py", "")
	wf(t, dir, "lib/util/__init__.py", "from ..core import x\n")
	wf(t, dir, "lib/util/extra.py", "import os  # stdlib, not a component\n")

	g, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]bool{}
	for _, d := range g.Dirs {
		dirs[d] = true
	}
	for _, w := range []string{"app", "lib/core", "lib/util"} {
		if !dirs[w] {
			t.Errorf("missing package dir %q; got %v", w, g.Dirs)
		}
	}
	edges := map[[2]string]bool{}
	for _, e := range g.Edges {
		edges[e] = true
	}
	want := [][2]string{{"app", "lib/core"}, {"app", "lib/util"}, {"lib/util", "lib/core"}}
	for _, w := range want {
		if !edges[w] {
			t.Errorf("missing edge %v; got %v", w, g.Edges)
		}
	}
	// stdlib import (os) must not be an edge
	if len(g.Edges) != 3 {
		t.Errorf("want 3 edges (stdlib dropped), got %v", g.Edges)
	}
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	if Detect(dir) {
		t.Error("empty dir should not detect as python")
	}
	wf(t, dir, "pkg/m.py", "x=1\n")
	if !Detect(dir) {
		t.Error("a .py file should detect as python")
	}
}
