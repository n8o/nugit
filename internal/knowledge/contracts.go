package knowledge

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/n8o/nugit/internal/model"
)

// FileReader reads one asserted file for a contract obligation. ok=false means
// "no such file here" — distinct from an empty file, which reads ok=true with
// "". Callers supply a ref-addressed reader at PR time (ADR-0029 /
// LESSON-read-from-reviewed-ref) and a working-tree reader for doctor.
type FileReader func(path string) (content string, ok bool)

// Obligation is one evaluated `must` a contract places on THIS repo (ADR-0033).
// It carries everything a finding needs, so a reader can act without opening
// the contract: which contract, from where, which party id matched, the must's
// own name, the file, and why it is unmet.
type Obligation struct {
	// ContractID is the bare id; Origin is "" for a local contract or the peer
	// name for a foreign one. Identity is the PAIR (ADR-0032) — every repo can
	// mint CONTRACT-0001.
	ContractID string
	Origin     string
	// RecordPath is the contract record's path (in the peer's checkout when
	// Origin != "").
	RecordPath string
	// Party is the org.repo id this obligation was selected by.
	Party string
	Must  model.Must
	Met   bool
	// Reason explains an unmet obligation in one clause; "" when Met.
	Reason string
}

// QualifiedID names the contract the way every renderer must: bare locally,
// `<peer>:<id>` for a foreign one (ADR-0032).
func (ob Obligation) QualifiedID() string { return model.QualifyID(ob.Origin, ob.ContractID) }

// OriginLabel spells out what the qualified prefix MEANS on a finding line.
func (ob Obligation) OriginLabel() string {
	if ob.Origin == "" {
		return "local"
	}
	return "peer " + ob.Origin
}

// Ratified reports whether an object's effective status is one a check may act
// on: accepted or active. A `proposed` contract is a candidate (ADR-0016) — a
// draft obligation is not an obligation — and superseded/invalidated is dead.
func Ratified(o *model.KnowledgeObject) bool {
	st := o.EffectiveStatus
	if st == "" {
		st = o.Status
	}
	return st == model.StatusAccepted || st == model.StatusActive
}

// PeerEligible is the admission rule for FOREIGN knowledge — the single
// definition ADR-0032 established and ADR-0033 extends. A peer object is a
// candidate only when all three hold:
//
//   - it is a decision, lesson, reference, or CONTRACT. The first three are the
//     kinds whose value is the recorded "why"; a contract is admitted because
//     being read by the counterparty is its entire purpose — a contract only the
//     declaring repo can see enforces nothing (ADR-0033). The single spec slot
//     and the glossary stay local: a peer's spec is not the active spec for a
//     path in THIS repo, and its glossary defines its own terms;
//   - it is GLOBALLY scoped. A peer's component-scoped record names a component
//     id that means nothing here — `scope: transport` in the sibling and
//     `transport` in this model are two unrelated strings that happen to match,
//     and scope is compared by string equality;
//   - it is RATIFIED. Someone else's unreviewed candidate is not context, and
//     nobody here can ratify a foreign draft.
func PeerEligible(o *model.KnowledgeObject) bool {
	switch o.Type {
	case model.KindDecision, model.KindLesson, model.KindReference, model.KindContract:
	default:
		return false
	}
	if o.Scope != "" && o.Scope != "global" {
		return false
	}
	return Ratified(o)
}

// Contracts selects the live, ratified contract objects in a set — local and
// foreign alike. Unratified contracts are filtered here, once, so no caller can
// forget (ADR-0033 point 9).
func Contracts(objs []model.KnowledgeObject) []model.KnowledgeObject {
	var out []model.KnowledgeObject
	for i := range objs {
		o := &objs[i]
		if o.Type == model.KindContract && o.ID != "" && Ratified(o) {
			out = append(out, *o)
		}
	}
	return out
}

// NamesParty reports whether the contract names repoID as one of its parties.
// An empty repoID never matches: with no configured identity, contract handling
// is inert and never guesses which party this repo is (ADR-0033 point 3).
func NamesParty(o *model.KnowledgeObject, repoID string) bool {
	if repoID == "" {
		return false
	}
	for _, p := range o.Parties {
		if p.Repo == repoID {
			return true
		}
	}
	return false
}

// ContractsNaming returns the live ratified contracts that name repoID as a
// party, sorted by (origin, id) so output is deterministic across a merged set.
func ContractsNaming(objs []model.KnowledgeObject, repoID string) []model.KnowledgeObject {
	var out []model.KnowledgeObject
	for _, o := range Contracts(objs) {
		if NamesParty(&o, repoID) {
			out = append(out, o)
		}
	}
	sortByKey(out)
	return out
}

// Obligations evaluates every `must` that the contracts in objs place on
// repoID, reading each asserted file through read.
//
// ONLY the party whose `repo` equals repoID is selected. Other parties'
// obligations are not this repo's business: it cannot verify a claim about a
// checkout that may be absent, has no ref to pin it to, and cannot fix it
// (ADR-0033 point 4).
//
// A contract from a peer and a local contract with the SAME id stay distinct —
// each obligation carries its own (Origin, ContractID) pair (ADR-0032).
func Obligations(objs []model.KnowledgeObject, repoID string, read FileReader) []Obligation {
	if repoID == "" || read == nil {
		return nil // inert without identity: fail closed to doing nothing
	}
	var out []Obligation
	for _, o := range ContractsNaming(objs, repoID) {
		for _, p := range o.Parties {
			if p.Repo != repoID {
				continue
			}
			for _, m := range p.Must {
				met, reason := EvalMust(m, read)
				out = append(out, Obligation{
					ContractID: o.ID,
					Origin:     o.Origin,
					RecordPath: o.Path,
					Party:      repoID,
					Must:       m,
					Met:        met,
					Reason:     reason,
				})
			}
		}
	}
	return out
}

// UnmetObligations filters to the ones that are not satisfied.
func UnmetObligations(obs []Obligation) []Obligation {
	var out []Obligation
	for _, ob := range obs {
		if !ob.Met {
			out = append(out, ob)
		}
	}
	return out
}

// EvalMust evaluates one obligation against the bytes read for its file.
//
// Every failure mode is UNMET with a reason, never an error and never a panic:
// a missing file, a malformed RE2 pattern, and a `must` missing `file` or
// `matches` all mean "nobody can show this was satisfied", and unverifiable is
// not met (ADR-0033 point 2). The pattern is compiled with Go's regexp — RE2,
// so matching is LINEAR in the input and a pathological pattern authored in
// another repo cannot burn this repo's CI.
func EvalMust(m model.Must, read FileReader) (met bool, reason string) {
	if m.File == "" {
		return false, "the obligation declares no `file`, so nothing can be checked"
	}
	if m.Matches == "" {
		return false, "the obligation declares no `matches` pattern, so nothing can be checked"
	}
	re, err := regexp.Compile(m.Matches)
	if err != nil {
		return false, fmt.Sprintf("`matches` is not a valid RE2 pattern (%v)", err)
	}
	src, ok := read(m.File)
	if !ok {
		return false, fmt.Sprintf("%s does not exist here", m.File)
	}
	found := re.MatchString(src)
	if m.Absent {
		if found {
			return false, fmt.Sprintf("%s matches /%s/, which the contract requires to be ABSENT", m.File, m.Matches)
		}
		return true, ""
	}
	if !found {
		return false, fmt.Sprintf("%s does not match /%s/", m.File, m.Matches)
	}
	return true, ""
}

// sortByKey orders objects by (origin, id) — the ADR-0032 identity pair — so a
// merged set renders deterministically and a peer's CONTRACT-0001 never sorts
// interchangeably with a local one.
func sortByKey(objs []model.KnowledgeObject) {
	sort.SliceStable(objs, func(i, j int) bool {
		if objs[i].Origin != objs[j].Origin {
			return objs[i].Origin < objs[j].Origin
		}
		return objs[i].ID < objs[j].ID
	})
}

// PeerContracts reads every peer's store and returns the contracts admissible
// here (PeerEligible), each stamped with its peer name as Origin. It is the
// narrow peer read the PR-time path uses: an absent peer contributes nothing
// and can never error, exactly as ADR-0032 requires.
func PeerContracts(srcs []PeerSource) []model.KnowledgeObject {
	var out []model.KnowledgeObject
	for _, src := range srcs {
		objs, load := LoadPeer(src)
		if !load.Reachable {
			continue
		}
		for i := range objs {
			o := &objs[i]
			if o.Type == model.KindContract && PeerEligible(o) {
				out = append(out, *o)
			}
		}
	}
	return out
}
