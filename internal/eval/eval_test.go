package eval

import "testing"

// TestCorpus runs the labeled corpus and gates on the heuristics' accuracy. Run
// with -v to see the full per-case report.
func TestCorpus(t *testing.T) {
	m := Run()
	t.Log("\n" + m.Report())

	for _, r := range m.Cases {
		if r.Err != nil {
			t.Errorf("case %q errored: %v", r.Name, r.Err)
		}
	}
	// Deterministic heuristics on a correctly-labeled corpus should be exact;
	// gate with a little headroom so a single future edge case is a warning in
	// the report rather than a hard break, but a real regression still fails.
	if m.TierAccuracy < 0.95 {
		t.Errorf("significance accuracy %.0f%% below gate (95%%)", m.TierAccuracy*100)
	}
	if m.CheckPrecision < 0.95 {
		t.Errorf("check precision %.0f%% below gate (95%%) — false positives", m.CheckPrecision*100)
	}
	if m.CheckRecall < 0.95 {
		t.Errorf("check recall %.0f%% below gate (95%%) — false negatives", m.CheckRecall*100)
	}
}
