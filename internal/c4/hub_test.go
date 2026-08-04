package c4

import "testing"

// ADR-0035 point 2 narrows ADR-0034 point 3: with a hub designated, the hub's
// landscape wins outright over any number of other peers, and the ambiguity that
// ADR-0034 had to fail closed on stops existing. Failing closed was right when no
// peer was privileged; a hub IS privileged by an explicit act of designation.

func src(system string) string {
	return `workspace { model { ` + system + ` = softwareSystem "S" { properties { "nugit_owner" "` + system + `" } } } }`
}

func TestHubLandscapeBeatsOtherPeers(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Name: "alpha", Path: "a.dsl", Src: src("alpha")},
		{Name: "hub", Path: "h.dsl", Src: src("hub"), Hub: true},
		{Name: "beta", Path: "b.dsl", Src: src("beta")},
	})
	if !res.Found {
		t.Fatal("three claimants including a hub resolved to nothing — the hub is the tie-break (ADR-0035)")
	}
	if res.From != "hub" || !res.FromHub {
		t.Errorf("From = %q FromHub = %v, want the hub", res.From, res.FromHub)
	}
	if len(res.Ambiguous) != 0 {
		t.Errorf("Ambiguous = %v, want none — a designated hub is not a tie", res.Ambiguous)
	}
}

// The local landscape still wins over the hub: designation buys tie-breaking
// authority over OTHER PEERS, not over this repo (ADR-0034 point 3.1 stands).
func TestLocalStillBeatsTheHub(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Name: "", Path: "local.dsl", Src: src("mine")},
		{Name: "hub", Path: "h.dsl", Src: src("hub"), Hub: true},
	})
	if !res.Found || res.From != "" || res.FromHub {
		t.Errorf("local did not win: From=%q FromHub=%v", res.From, res.FromHub)
	}
}

// A hub that declares NO landscape must not suppress the single peer that does —
// an absent file is not a claim.
func TestAbsentHubLandscapeFallsBackToTheOldRule(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Name: "hub", Path: "h.dsl", Src: `workspace { model { } }`, Hub: true},
		{Name: "alpha", Path: "a.dsl", Src: src("alpha")},
	})
	if !res.Found || res.From != "alpha" {
		t.Errorf("From = %q, want the single declaring peer", res.From)
	}
	if res.FromHub {
		t.Error("FromHub set for a landscape the hub did not declare")
	}
}

// With NO hub designated the ADR-0034 rule is untouched: two claimants are
// ambiguous and nothing is used.
func TestWithoutAHubAmbiguityStillFailsClosed(t *testing.T) {
	res := ResolveLandscape([]LandscapeSource{
		{Name: "alpha", Path: "a.dsl", Src: src("alpha")},
		{Name: "beta", Path: "b.dsl", Src: src("beta")},
	})
	if res.Found {
		t.Error("two non-hub claimants resolved to something — ADR-0034 point 3.3 must still hold")
	}
	if len(res.Ambiguous) != 2 {
		t.Errorf("Ambiguous = %v, want both claimants named", res.Ambiguous)
	}
}
