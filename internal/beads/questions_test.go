package beads

import (
	"reflect"
	"testing"
)

func TestOpenQuestions(t *testing.T) {
	st := Store{Issues: ParseJSONL([]byte(`{"id":"acme-1","title":"Export [NEEDS CLARIFICATION: CSV or JSON?]","description":"Who sees it? [NEEDS CLARIFICATION:   which\n roles ] and [NEEDS CLARIFICATION: CSV or JSON?]","acceptance_criteria":"AC-1: done [NEEDS CLARIFICATION:]"}
{"id":"acme-2","title":"plain","description":"needs clarification, but not a marker"}`))}
	a, _ := st.Find("acme-1")
	want := []string{"CSV or JSON?", "which roles", "(no question written)"}
	if got := OpenQuestions(a); !reflect.DeepEqual(got, want) {
		t.Errorf("OpenQuestions = %q, want %q", got, want)
	}
	b, _ := st.Find("acme-2")
	if got := OpenQuestions(b); got != nil {
		t.Errorf("plain bead has questions: %q", got)
	}
}
