package usage

import (
	"testing"

	"github.com/n8o/nugit/internal/retrieval"
)

// ADR-0058: a record names what it served, peer ids qualified, so hygiene can
// tell a lesson nobody is shown from one that is.
func TestFromBundleRecordsServedIDs(t *testing.T) {
	b := retrieval.Bundle{
		Decisions: []retrieval.Item{{ID: "ADR-1"}},
		Lessons:   []retrieval.Item{{ID: "LESSON-x"}, {ID: "ADR-0020", Origin: "platform"}},
		Spec:      &retrieval.Item{ID: "SPEC-1"},
	}
	got := FromBundle(SourceMCP, "", b).Served
	want := []string{"ADR-1", "LESSON-x", "platform:ADR-0020", "SPEC-1"}
	if len(got) != len(want) {
		t.Fatalf("served = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("served[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
