package retrieval

import "testing"

func setupLeveled(t *testing.T) string {
	dir := t.TempDir()
	wf(t, dir, ".nugit/architecture/workspace.dsl", `workspace "m" {
  model { sys = softwareSystem "m" {
    Y = container "Y" {
      properties { "paths" "ycore/**" }
      y1 = component "Y1" { properties { paths "y1/**" } }
    }
    Z = container "Z" {
      z1 = component "Z1" { properties { paths "z1/**" } }
    }
    Y -> Z
  } }
}`)
	wf(t, dir, ".nugit/decisions/y.md", obj("ADR-CT", "decision", "Y", "container decision", ""))
	wf(t, dir, ".nugit/decisions/y1.md", obj("ADR-Y1", "decision", "y1", "component decision", ""))
	wf(t, dir, ".nugit/decisions/z.md", obj("ADR-Z", "decision", "Z", "other container decision", ""))
	return dir
}

// Container-scoped knowledge surfaces for a child component's path (the scope
// chain {comp, ContainerOf(comp)}), and foreign-container knowledge stays out.
func TestContextContainerScopeChain(t *testing.T) {
	dir := setupLeveled(t)
	b, err := Context(Options{RepoDir: dir, Path: "y1/y1.go"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Component != "y1" {
		t.Fatalf("component = %q, want y1", b.Component)
	}
	d := ids(b.Decisions)
	if !d["ADR-Y1"] {
		t.Error("component-scoped decision missing")
	}
	if !d["ADR-CT"] {
		t.Error("parent-container-scoped decision must surface for a child-component path")
	}
	if d["ADR-Z"] {
		t.Error("foreign container's decision leaked into y1's bundle")
	}
}

// A container-owned path resolves to the container id and pulls its knowledge.
func TestContextContainerOwnedPath(t *testing.T) {
	dir := setupLeveled(t)
	b, err := Context(Options{RepoDir: dir, Path: "ycore/core.go"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Component != "Y" {
		t.Fatalf("component = %q, want Y (container-owned path)", b.Component)
	}
	d := ids(b.Decisions)
	if !d["ADR-CT"] {
		t.Error("container-scoped decision missing for the container's own path")
	}
	if d["ADR-Y1"] || d["ADR-Z"] {
		t.Errorf("scope chain leaked downward/sideways: %v", d)
	}
	// The c4 slice stays literal: the declared container edge is visible.
	if len(b.C4.DependsOn) != 1 || b.C4.DependsOn[0] != "Z" {
		t.Errorf("c4 slice depends_on = %v, want [Z]", b.C4.DependsOn)
	}
}
