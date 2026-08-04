package knowledge

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// files builds a FileReader over an in-memory map. A path absent from the map
// reads ok=false — "no such file", distinct from an empty one.
func files(m map[string]string) FileReader {
	return func(p string) (string, bool) {
		s, ok := m[p]
		return s, ok
	}
}

func contract(id, origin string, parties ...model.Party) model.KnowledgeObject {
	return model.KnowledgeObject{
		FrontMatter: model.FrontMatter{
			ID: id, Type: model.KindContract, Scope: "global",
			Status: model.StatusAccepted, Parties: parties,
		},
		Path:            ".nugit/contracts/" + strings.ToLower(id) + ".md",
		EffectiveStatus: model.StatusAccepted,
		Origin:          origin,
	}
}

func TestEvalMustPositiveAndNegated(t *testing.T) {
	read := files(map[string]string{
		"third_party/versions.env": "TRANSPORT_TAG=v0.3.7\n",
		"apps/gateway/server.cpp":  "auto f = makeRawFactory();\n",
	})
	cases := []struct {
		name    string
		must    model.Must
		wantMet bool
	}{
		{"anchored match", model.Must{Name: "pin", File: "third_party/versions.env", Matches: `^TRANSPORT_TAG=v0\.3\.`}, true},
		{"anchored miss", model.Must{Name: "pin", File: "third_party/versions.env", Matches: `^TRANSPORT_TAG=v0\.4\.`}, false},
		{"absent satisfied", model.Must{Name: "no raw factory", File: "third_party/versions.env", Matches: "makeRawFactory", Absent: true}, true},
		{"absent violated", model.Must{Name: "no raw factory", File: "apps/gateway/server.cpp", Matches: "makeRawFactory", Absent: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			met, reason := EvalMust(tc.must, read)
			if met != tc.wantMet {
				t.Fatalf("met = %v, want %v (reason %q)", met, tc.wantMet, reason)
			}
			if !met && reason == "" {
				t.Error("an unmet obligation must always carry a reason")
			}
		})
	}
}

// A `must` naming a file that does not resolve is UNMET, never an error: an
// assertion nobody can evaluate has not been satisfied (ADR-0033 point 2).
// The same holds for a malformed pattern and a half-authored entry.
func TestUnevaluableMustIsUnmetNeverAnError(t *testing.T) {
	read := files(map[string]string{"present.txt": "hello"})
	cases := []struct {
		name       string
		must       model.Must
		wantReason string
	}{
		{"missing file", model.Must{Name: "n", File: "gone.txt", Matches: "x"}, "does not exist here"},
		{"missing file, negated", model.Must{Name: "n", File: "gone.txt", Matches: "x", Absent: true}, "does not exist here"},
		{"invalid RE2", model.Must{Name: "n", File: "present.txt", Matches: "([unclosed"}, "not a valid RE2 pattern"},
		{"no file declared", model.Must{Name: "n", Matches: "x"}, "declares no `file`"},
		{"no pattern declared", model.Must{Name: "n", File: "present.txt"}, "declares no `matches`"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			met, reason := EvalMust(tc.must, read)
			if met {
				t.Fatal("an unevaluable obligation must be UNMET, never silently met")
			}
			if !strings.Contains(reason, tc.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", reason, tc.wantReason)
			}
		})
	}
}

// Only the party matching this repo's configured identity is evaluated. Another
// party's obligations are not this repo's business and can never fire here.
func TestOnlyThisReposPartyIsEvaluated(t *testing.T) {
	objs := []model.KnowledgeObject{contract("CONTRACT-0001", "producer",
		model.Party{Repo: "producer-service", Must: []model.Must{
			{Name: "producer side", File: "gone-in-both.txt", Matches: "x"}}},
		model.Party{Repo: "consumer-gateway", Must: []model.Must{
			{Name: "consumer side", File: "gone-in-both.txt", Matches: "x"}}},
	)}
	obs := Obligations(objs, "consumer-gateway", files(nil))
	if len(obs) != 1 {
		t.Fatalf("got %d obligations, want exactly this repo's 1: %+v", len(obs), obs)
	}
	if obs[0].Must.Name != "consumer side" {
		t.Errorf("evaluated the WRONG party's obligation: %q", obs[0].Must.Name)
	}
	if obs[0].Party != "consumer-gateway" {
		t.Errorf("party = %q", obs[0].Party)
	}
}

// No identity ⇒ inert. nugit never guesses which party it is (ADR-0033 point 3).
func TestNoIdentityIsInert(t *testing.T) {
	objs := []model.KnowledgeObject{contract("CONTRACT-0001", "producer",
		model.Party{Repo: "consumer-gateway", Must: []model.Must{{Name: "n", File: "gone.txt", Matches: "x"}}})}
	if obs := Obligations(objs, "", files(nil)); obs != nil {
		t.Fatalf("with no org.repo the check must be inert, got %+v", obs)
	}
	if got := ContractsNaming(objs, ""); got != nil {
		t.Fatalf("an empty identity must match no party, got %+v", got)
	}
}

// An unratified contract is a candidate, not an obligation (ADR-0016).
func TestUnratifiedContractNeverFires(t *testing.T) {
	for _, st := range []model.Status{model.StatusProposed, model.StatusSuperseded, model.StatusInvalidated} {
		o := contract("CONTRACT-0001", "", model.Party{Repo: "me",
			Must: []model.Must{{Name: "n", File: "gone.txt", Matches: "x"}}})
		o.Status, o.EffectiveStatus = st, st
		if obs := Obligations([]model.KnowledgeObject{o}, "me", files(nil)); len(obs) != 0 {
			t.Errorf("status %s fired %d obligation(s); only ratified contracts may fire", st, len(obs))
		}
	}
}

// ADR-0032 identity: a peer's CONTRACT-0001 and a local CONTRACT-0001 are two
// different contracts. Neither may shadow, dedupe, or absorb the other.
func TestSameIDLocalAndPeerContractsStayDistinct(t *testing.T) {
	local := contract("CONTRACT-0001", "", model.Party{Repo: "me",
		Must: []model.Must{{Name: "local obligation", File: "gone.txt", Matches: "x"}}})
	peer := contract("CONTRACT-0001", "producer", model.Party{Repo: "me",
		Must: []model.Must{{Name: "peer obligation", File: "gone.txt", Matches: "x"}}})

	obs := Obligations([]model.KnowledgeObject{local, peer}, "me", files(nil))
	if len(obs) != 2 {
		t.Fatalf("got %d obligations, want 2 (one per distinct contract): %+v", len(obs), obs)
	}
	qual := map[string]string{}
	for _, ob := range obs {
		qual[ob.QualifiedID()] = ob.Must.Name
	}
	if qual["CONTRACT-0001"] != "local obligation" {
		t.Errorf("local contract lost or mislabeled: %+v", qual)
	}
	if qual["producer:CONTRACT-0001"] != "peer obligation" {
		t.Errorf("peer contract must render qualified and stay distinct: %+v", qual)
	}
	if got := obs[0].OriginLabel(); got != "local" {
		t.Errorf("local origin label = %q", got)
	}
	if got := obs[1].OriginLabel(); got != "peer producer" {
		t.Errorf("peer origin label = %q", got)
	}
}

// The peer admission gate (ADR-0032) now admits contracts — a contract only its
// author can read enforces nothing — while everything else about the gate holds.
func TestPeerEligibleAdmitsContractsAndNothingElseNew(t *testing.T) {
	admit := func(k model.Kind, scope string, st model.Status) bool {
		o := model.KnowledgeObject{FrontMatter: model.FrontMatter{Type: k, Scope: scope, Status: st}, EffectiveStatus: st}
		return PeerEligible(&o)
	}
	if !admit(model.KindContract, "global", model.StatusAccepted) {
		t.Error("a global ratified peer contract must be admitted — being read by the counterparty IS its purpose")
	}
	if admit(model.KindContract, "transport", model.StatusAccepted) {
		t.Error("a component-scoped peer contract must stay out: scope ids are compared by string equality across unrelated repos")
	}
	if admit(model.KindContract, "global", model.StatusProposed) {
		t.Error("an unratified peer contract must stay out — nobody here can ratify a foreign draft")
	}
	if admit(model.KindSpec, "global", model.StatusAccepted) || admit(model.KindGlossary, "global", model.StatusActive) {
		t.Error("the spec slot and glossary must stay local (ADR-0032)")
	}
}

// A regexp is matched against the file's bytes with Go's RE2 engine, so a
// pattern shaped like the classic catastrophic-backtracking bomb still returns
// promptly — the property that makes reading a pattern from ANOTHER repo safe.
func TestPatternIsLinearTimeRE2(t *testing.T) {
	read := files(map[string]string{"f": strings.Repeat("a", 40) + "b"})
	met, reason := EvalMust(model.Must{Name: "n", File: "f", Matches: `^(a+)+$`}, read)
	if met {
		t.Fatalf("unexpected match (reason %q)", reason)
	}
}
