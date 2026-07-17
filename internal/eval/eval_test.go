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

// The adversarial set is hand-built spoofs: zero tolerance — every case must
// pass exactly (no precision/recall slack).
func TestAdversarial(t *testing.T) {
	m := RunSet(adversarial)
	for _, r := range m.Cases {
		if !r.Pass() {
			t.Errorf("%s: tier=%v(ok=%v) clean=%v miss=%v extra=%v err=%v",
				r.Name, r.GotTier, r.TierOK, r.CleanOK, r.MissChecks, r.ExtraChecks, r.Err)
		}
	}
}
