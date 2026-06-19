package bootstrap

import "testing"

func TestDiscoverStructuralContainer(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "apps/svc-a/main.go", "package main\n")
	wf(t, dir, "apps/svc-b/x.cpp", "int main(){}\n") // non-Go is fine
	wf(t, dir, "libs/core/core.hpp", "// core\n")
	wf(t, dir, "common/util.py", "x=1\n")
	wf(t, dir, "third_party/foo/bar.cpp", "") // must be skipped

	g, err := DiscoverStructural(dir, StructuralOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Structural || len(g.Edges) != 0 {
		t.Errorf("structural model must be edges-free and flagged: edges=%d structural=%v", len(g.Edges), g.Structural)
	}
	got := map[string]string{}
	for _, c := range g.Components {
		got[c.Dir] = c.Glob
	}
	// container descent (apps/*, libs/*) + top-level (common); third_party skipped
	for _, w := range []string{"apps/svc-a", "apps/svc-b", "libs/core", "common"} {
		if _, ok := got[w]; !ok {
			t.Errorf("missing component %q; got %v", w, got)
		}
	}
	if got["apps/svc-a"] != "apps/svc-a/**" {
		t.Errorf("glob = %q, want apps/svc-a/**", got["apps/svc-a"])
	}
	if _, ok := got["third_party"]; ok {
		t.Error("third_party must be skipped")
	}
}

func TestDiscoverStructuralToplevel(t *testing.T) {
	dir := t.TempDir()
	wf(t, dir, "apps/svc/main.go", "package main\n")
	wf(t, dir, "libs/core/c.go", "package core\n")
	g, err := DiscoverStructural(dir, StructuralOptions{Layout: "toplevel"})
	if err != nil {
		t.Fatal(err)
	}
	dirs := map[string]bool{}
	for _, c := range g.Components {
		dirs[c.Dir] = true
	}
	if !dirs["apps"] || !dirs["libs"] {
		t.Errorf("toplevel should yield apps,libs; got %v", dirs)
	}
	if dirs["apps/svc"] {
		t.Error("toplevel layout must not descend into containers")
	}
}
