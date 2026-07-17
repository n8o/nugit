package model

import "testing"

// leveled: containers X{x1,x2}, Y{y1}, Z{z1}; flat component f (no container).
// Declared edges vary per case via rels.
func leveled(rels ...[2]string) Model {
	m := Model{
		Components: []Component{
			{ID: "x1", Container: "X"},
			{ID: "x2", Container: "X"},
			{ID: "y1", Container: "Y"},
			{ID: "z1", Container: "Z"},
			{ID: "f"},
		},
		Containers: []Container{
			{ID: "X"}, {ID: "Y", Paths: []string{"ycore/**"}}, {ID: "Z"},
		},
	}
	for _, r := range rels {
		m.Relationships = append(m.Relationships, Relationship{Src: r[0], Dst: r[1]})
	}
	return m
}

func TestSameLineage(t *testing.T) {
	m := leveled()
	cases := []struct {
		a, b string
		want bool
	}{
		{"x1", "x1", true},  // identity
		{"x1", "X", true},   // component -> its container
		{"X", "x1", true},   // container -> its component
		{"x1", "x2", false}, // siblings are NOT lineage
		{"x1", "Y", false},  // foreign container
		{"x1", "y1", false},
		{"X", "Y", false},
		{"f", "x1", false},
		{"f", "f", true},
	}
	for _, c := range cases {
		if got := m.SameLineage(c.a, c.b); got != c.want {
			t.Errorf("SameLineage(%s,%s) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// TestCovered walks the full level-interaction table (ADR-0017).
func TestCovered(t *testing.T) {
	cases := []struct {
		name     string
		rels     [][2]string
		src, dst string
		want     bool
	}{
		// -- same container: only a literal component edge covers --
		{"intra-comp-edge", [][2]string{{"x1", "x2"}}, "x1", "x2", true},
		{"intra-uncovered", nil, "x1", "x2", false},
		{"intra-NOT-covered-by-container-self-edge", [][2]string{{"X", "X"}}, "x1", "x2", false},
		{"intra-NOT-covered-by-container-edge", [][2]string{{"X", "Y"}}, "x1", "x2", false},
		// -- cross container: component, container, or mixed edge covers --
		{"cross-comp-edge", [][2]string{{"x1", "y1"}}, "x1", "y1", true},
		{"cross-container-rollup", [][2]string{{"X", "Y"}}, "x1", "y1", true},
		{"cross-mixed-src-container", [][2]string{{"X", "y1"}}, "x1", "y1", true},
		{"cross-mixed-dst-container", [][2]string{{"x1", "Y"}}, "x1", "y1", true},
		{"cross-uncovered", nil, "x1", "y1", false},
		{"cross-wrong-direction", [][2]string{{"Y", "X"}}, "x1", "y1", false},
		{"cross-unrelated-container-edge", [][2]string{{"X", "Y"}}, "x1", "z1", false},
		// -- container endpoints (container-own paths resolve to container ids) --
		{"container-src-covered-by-container-edge", [][2]string{{"Y", "Z"}}, "Y", "z1", true},
		{"container-src-covered-by-mixed-edge", [][2]string{{"Y", "z1"}}, "Y", "z1", true},
		{"container-src-uncovered", nil, "Y", "z1", false},
		{"container-to-own-child", nil, "Y", "y1", true}, // lineage
		{"child-to-own-container", nil, "y1", "Y", true}, // lineage
		{"container-to-container-literal", [][2]string{{"X", "Y"}}, "X", "Y", true},
		{"comp-to-container-literal", [][2]string{{"x1", "Z"}}, "x1", "Z", true},
		// -- mixed with flat component: scopes differ, probes still work --
		{"flat-to-comp-via-comp-edge", [][2]string{{"f", "y1"}}, "f", "y1", true},
		{"flat-to-comp-via-container-edge", [][2]string{{"f", "Y"}}, "f", "y1", true},
		{"flat-to-comp-uncovered", nil, "f", "y1", false},
	}
	for _, c := range cases {
		m := leveled(c.rels...)
		if got := m.Covered(c.src, c.dst); got != c.want {
			t.Errorf("%s: Covered(%s,%s) = %v, want %v", c.name, c.src, c.dst, got, c.want)
		}
	}
}

// Flat models must reduce Covered exactly to HasRelationship.
func TestCoveredFlatReducesToHasRelationship(t *testing.T) {
	m := Model{
		Components:    []Component{{ID: "a"}, {ID: "b"}, {ID: "c"}},
		Relationships: []Relationship{{Src: "a", Dst: "b"}},
	}
	pairs := [][2]string{{"a", "b"}, {"b", "a"}, {"a", "c"}, {"c", "b"}, {"a", "a"}}
	for _, p := range pairs {
		want := m.HasRelationship(p[0], p[1]) || p[0] == p[1]
		if got := m.Covered(p[0], p[1]); got != want {
			t.Errorf("flat Covered(%s,%s) = %v, want %v", p[0], p[1], got, want)
		}
	}
}

func TestContainerLookups(t *testing.T) {
	m := leveled()
	if _, ok := m.Container("X"); !ok {
		t.Error("Container(X) not found")
	}
	if _, ok := m.Container("x1"); ok {
		t.Error("Container(x1) must not match a component id")
	}
	if got := m.ContainerOf("x1"); got != "X" {
		t.Errorf("ContainerOf(x1) = %q", got)
	}
	if got := m.ContainerOf("X"); got != "" {
		t.Errorf("ContainerOf on a container id must be \"\", got %q", got)
	}
	if got := m.ContainerOf("f"); got != "" {
		t.Errorf("ContainerOf(flat) = %q", got)
	}
	if got := m.ContainerOf("nope"); got != "" {
		t.Errorf("ContainerOf(unknown) = %q", got)
	}
}
