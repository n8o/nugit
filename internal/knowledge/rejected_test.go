package knowledge

import "testing"

func TestRejectedSectionForms(t *testing.T) {
	// heading form
	heading := "# X\n\n## Rejected\n\n- bad option, because reasons\n"
	if got := RejectedSection(heading); got != "- bad option, because reasons" {
		t.Errorf("heading form = %q", got)
	}

	// bold-inline form: keep the lead-in text + continuation, stop before the
	// next bold lead-in (a previous bug pulled in the following **Keywords** line
	// and dropped the inline text).
	bold := "**Rejected:** read from disk because it is simpler — it couples\n" +
		"the analysis to the working tree.\n\n" +
		"**Keywords:** ci, determinism\n"
	got := RejectedSection(bold)
	want := "read from disk because it is simpler — it couples the analysis to the working tree."
	if got != want {
		t.Errorf("bold-inline form =\n  %q\nwant\n  %q", got, want)
	}
}
