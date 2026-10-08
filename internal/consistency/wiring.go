package consistency

import (
	"strings"

	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/wiring"
)

// checkWiringDrift reports ADR-0026 wiring drift at PR time.
//
// `nugit doctor` has scanned for this since ADR-0026 and will keep doing so.
// The reason it is also here: doctor is a pre-flight, it is advisory, and
// nobody runs it on a Tuesday. On the pilot an install pin sat stale in one of
// five surfaces for four weeks while the other four were aligned by hand —
// doctor reported it correctly the whole time, into a terminal nobody opened.
// ADR-0039 made exactly this argument for duplicate ids; it holds here too
// (ADR-0041).
//
// Scoped to PRs that TOUCH a wiring artifact, for the same reason
// checkDuplicateID scopes to touched objects: pre-existing drift is doctor's
// job, and a check that fires on every unrelated PR is one people mute. Warn,
// not fail — the drift is in documentation and CI plumbing, and a repo
// mid-upgrade legitimately passes through a mixed state.
func checkWiringDrift(in Input) []model.Finding {
	if in.Wiring.Clean() {
		return nil
	}
	touched := false
	for _, f := range in.Code.Files {
		if wiring.IsWiringPath(strings.TrimPrefix(f.Path, in.Prefix)) {
			touched = true
			break
		}
	}
	if !touched {
		return nil
	}
	var fs []model.Finding
	if len(in.Wiring.WeakFailOn) > 0 {
		fs = append(fs, model.Finding{
			Check: "wiring-drift", Severity: model.SevWarn,
			Title:  "CI enforcement is weaker than config.yml declares",
			Detail: in.Wiring.FailOnDetail(in.WiringCfg) + ". A workflow that overrides the declared policy cancels it silently, so the repo reports a gate it is not running.",
		})
	}
	if !in.Wiring.PinsAgree() {
		fs = append(fs, model.Finding{
			Check: "wiring-drift", Severity: model.SevWarn,
			Title:  "nugit install pins disagree across the repo's wiring",
			Detail: in.Wiring.PinDetail() + ". A local self-check and the CI gate then run different versions and can legitimately disagree about the same diff — which reads as a flaky gate, not a stale pin.",
		})
	}
	if len(in.Wiring.C4Contradictions) > 0 {
		fs = append(fs, model.Finding{
			Check: "wiring-drift", Severity: model.SevWarn,
			Title:  "a skill file contradicts config.yml",
			Detail: in.Wiring.C4Detail() + ". Skill prose is what an agent reads instead of the config, so a stale claim steers every session that trusts it.",
		})
	}
	return fs
}
