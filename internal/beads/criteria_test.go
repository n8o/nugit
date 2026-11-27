package beads

import (
	"reflect"
	"testing"
)

func TestCriteria(t *testing.T) {
	got := Criteria("intro line\nAC-1: it builds\n- AC-3:  it ships \n* AC-1: a repeat keeps the first\nAC-0: zero is no number\nnot AC-2: mid-line\n  AC-12: indented")
	want := []Criterion{{1, "it builds"}, {3, "it ships"}, {12, "indented"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Criteria = %+v, want %+v", got, want)
	}
	if got[1].ID() != "AC-3" {
		t.Errorf("ID = %q", got[1].ID())
	}
	if Criteria("it works; it ships") != nil {
		t.Error("unnumbered criteria must yield none")
	}
}

func TestCriterionID(t *testing.T) {
	for in, want := range map[string][2]string{
		"AC-3": {"", "AC-3"}, "Ac-03": {"", "AC-3"}, " AC-1 ": {"", "AC-1"},
		"PROJ-7/AC-2": {"PROJ-7", "AC-2"},
	} {
		bead, id, ok := CriterionID(in)
		if !ok || bead != want[0] || id != want[1] {
			t.Errorf("CriterionID(%q) = %q %q %v, want %v", in, bead, id, ok, want)
		}
	}
	for _, in := range []string{"", "AC-", "AC-0", "AC-x", "FR-001", "3"} {
		if _, _, ok := CriterionID(in); ok {
			t.Errorf("CriterionID(%q) ok", in)
		}
	}
}
