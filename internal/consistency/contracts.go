package consistency

import (
	"fmt"
	"strings"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// ContractOpts configures the cross-repo obligation check (ADR-0033). The zero
// value is INERT: no identity means nugit cannot know which party this repo is,
// and it never guesses (fail closed to doing nothing).
type ContractOpts struct {
	// OrgRepo is this repo's stable org-wide id (config `org.repo`), matched by
	// string equality against a contract's `parties[].repo`.
	OrgRepo string
	// Fail promotes unmet obligations from warn to fail (config
	// `contracts.mode: fail`). Default warn — see ADR-0033 point 7.
	Fail bool
	// Off disables the check entirely (`contracts.mode: off`).
	Off bool
	// PeerContracts are the contracts read from peer stores, already filtered by
	// the ADR-0032 admission rule (global + ratified) and stamped with Origin.
	// Local contracts are NOT here — they arrive at the reviewed ref inside
	// Input.AllObjects.
	PeerContracts []model.KnowledgeObject
}

// checkContractObligations flags every obligation THIS repo owes under a
// ratified contract — local or from a peer — that the reviewed ref does not
// satisfy (ADR-0033).
//
// This is the one check whose input can come from another repo, so its gates
// are deliberate and all three must be opened here: `peers:` configured
// (ADR-0032), `org.repo` configured, and a ratified contract that names this
// repo by that id. Other parties' obligations are ignored — they are not this
// repo's business and cannot be fixed in this repo's PR.
func checkContractObligations(in Input) []model.Finding {
	opt := in.Contracts
	if opt.Off || opt.OrgRepo == "" {
		return nil
	}
	// Local contracts come from the store AT THE REVIEWED REF (AllObjects);
	// peer contracts are read from the peer's checkout, because this repo has no
	// ref that addresses another repo's history. Every file the assertion reads
	// is still ref-addressed below.
	objs := append(append([]model.KnowledgeObject{}, in.AllObjects...), opt.PeerContracts...)
	obs := knowledge.Obligations(objs, opt.OrgRepo, refReader(in.Repo, in.Head))

	sev := model.SevWarn
	if opt.Fail {
		sev = model.SevFail
	}
	var fs []model.Finding
	for _, ob := range knowledge.UnmetObligations(obs) {
		fs = append(fs, model.Finding{
			Check:    "contract-obligation",
			Severity: sev,
			Title: fmt.Sprintf("unmet obligation under %s (%s): %s",
				ob.QualifiedID(), ob.OriginLabel(), ob.Must.Name),
			Detail: contractDetail(ob),
		})
	}
	return fs
}

// contractDetail words the finding so it is actionable WITHOUT opening the
// contract: which contract and where it lives, the party id that selected this
// obligation, the must's own name, the asserted file, the pattern, and why it
// is unmet.
func contractDetail(ob knowledge.Obligation) string {
	where := ob.RecordPath
	if ob.Origin != "" {
		where = "peer " + ob.Origin + ":" + ob.RecordPath
	}
	return fmt.Sprintf("%s (%s) names this repo as party %q and requires %q — %s. "+
		"Satisfy it here, or supersede the contract where it is declared.",
		ob.QualifiedID(), where, ob.Party, ob.Must.Name, ob.Reason)
}

// refReader reads asserted files from the REVIEWED REF, never the working tree
// (LESSON-read-from-reviewed-ref, ADR-0029). Existence comes from the tree
// listing, because ShowFile reports a missing path as empty content — and
// "empty file" and "no file" are different verdicts for an obligation.
//
// The tree listing is computed once, lazily, and only when an obligation is
// actually evaluated: a repo with no contracts naming it pays nothing.
func refReader(repo gitutil.Repo, ref string) knowledge.FileReader {
	var tree map[string]bool
	return func(path string) (string, bool) {
		if tree == nil {
			tree = map[string]bool{}
			paths, err := repo.ListTree(ref)
			if err != nil {
				// An unreadable tree cannot demonstrate an obligation is met;
				// UNMET is the honest verdict, never an error that fails the render.
				return "", false
			}
			for _, p := range paths {
				tree[p] = true
			}
		}
		p := strings.TrimPrefix(path, "./")
		if !tree[p] {
			return "", false
		}
		src, err := repo.ShowFile(ref, p)
		if err != nil {
			return "", false
		}
		return src, true
	}
}
