// Package ratify promotes candidate-lane knowledge objects (`status:
// proposed`, ADR-0016) into the ratified corpus. The promotion is a surgical
// one-line edit — replace `status: proposed` with the kind's ratified status
// inside the front-matter fence, byte-preserving everything else — so the git
// diff is a single reviewable line and concurrent ratifies merge cleanly.
// This is the single permitted in-place mutation of a knowledge object: the
// ADR-0003 immutability contract attaches at ratification (ADR-0016).
package ratify

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// Result reports a single promotion.
type Result struct {
	ID   string
	Path string // repo-relative
	From model.Status
	To   model.Status
}

// statusLineRE matches an authored candidate status line (tolerates trailing
// spaces and CRLF; the capture preserves the original line ending).
var statusLineRE = regexp.MustCompile(`(?m)^status:[ \t]*proposed[ \t]*(\r?)$`)

// ratifiedStatus maps a kind to the status ratification grants it.
func ratifiedStatus(k model.Kind) (model.Status, error) {
	switch k {
	case model.KindDecision, model.KindSpec, model.KindContract:
		// A contract ratifies like a decision: `accepted` is what makes it able
		// to fire an obligation check, so a two-sided invariant gets reviewed
		// before it can fail anyone's build (ADR-0033 point 9, ADR-0016).
		return model.StatusAccepted, nil
	case model.KindLesson, model.KindReference:
		return model.StatusActive, nil
	}
	return "", fmt.Errorf("type %q cannot be ratified", k)
}

// Ratify promotes the proposed object with the given id. It refuses objects
// that are not authored `proposed`, and objects whose effective status is
// superseded/invalidated — supersession outranks promotion.
func Ratify(repoDir, id string) (Result, error) {
	objs, err := knowledge.Load(repoDir)
	if err != nil {
		return Result{}, err
	}
	// Collect EVERY match before choosing one. knowledge.Load is local-only, so
	// every object here has an empty Origin and a bare-id match is exactly an
	// (origin, id) match (ADR-0032) — the ambiguity below is a within-store
	// collision, never a peer's legitimate same-id record.
	var matches []*model.KnowledgeObject
	for i := range objs {
		if objs[i].ID == id {
			matches = append(matches, &objs[i])
		}
	}
	if len(matches) == 0 {
		return Result{}, fmt.Errorf("no knowledge object with id %q", id)
	}
	if len(matches) > 1 {
		// Refuse, never guess: promoting "the first one the walk found" would
		// silently ratify an arbitrary file and leave the other behind, looking
		// like success (ADR-0039).
		paths := make([]string, 0, len(matches))
		for _, m := range matches {
			paths = append(paths, m.Path)
		}
		sort.Strings(paths)
		return Result{}, fmt.Errorf("%s is ambiguous: %d files carry that id (%s) — ratifying one would leave the other behind; give one of them its own id (see `nugit explain duplicate-knowledge-id`)",
			id, len(paths), strings.Join(paths, ", "))
	}
	obj := matches[0]
	if obj.EffectiveStatus == model.StatusSuperseded || obj.EffectiveStatus == model.StatusInvalidated {
		return Result{}, fmt.Errorf("%s is %s — supersession outranks promotion", id, obj.EffectiveStatus)
	}
	if obj.Status != model.StatusProposed {
		return Result{}, fmt.Errorf("%s is %q, not proposed — nothing to ratify", id, obj.Status)
	}
	to, err := ratifiedStatus(obj.Type)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", id, err)
	}

	abs := filepath.Join(repoDir, obj.Path)
	content, err := os.ReadFile(abs)
	if err != nil {
		return Result{}, err
	}
	promoted, err := promote(string(content), to)
	if err != nil {
		return Result{}, fmt.Errorf("%s (%s): %w", id, obj.Path, err)
	}
	if err := os.WriteFile(abs, []byte(promoted), 0o644); err != nil {
		return Result{}, err
	}
	return Result{ID: id, Path: obj.Path, From: model.StatusProposed, To: to}, nil
}

// promote rewrites exactly one `status: proposed` line inside the leading
// front-matter fence. Anything else — zero matches, several matches, no
// fence — is an error, never a guess.
func promote(content string, to model.Status) (string, error) {
	if !strings.HasPrefix(content, "---") {
		return "", fmt.Errorf("no front-matter fence")
	}
	end := strings.Index(content[3:], "\n---")
	if end < 0 {
		return "", fmt.Errorf("unterminated front-matter fence")
	}
	head, tail := content[:3+end], content[3+end:]
	switch n := len(statusLineRE.FindAllString(head, -1)); n {
	case 1:
	case 0:
		return "", fmt.Errorf("no `status: proposed` line in front-matter")
	default:
		return "", fmt.Errorf("%d `status: proposed` lines in front-matter", n)
	}
	return statusLineRE.ReplaceAllString(head, "status: "+string(to)+"$1") + tail, nil
}

// Listing is what `nugit ratify -list` shows: the candidate lane, plus the id
// collisions that make `nugit ratify <id>` refuse.
//
// Duplicates are carried alongside Pending rather than folded into them because
// the collision's other half is usually NOT a candidate — the pilot case was one
// `accepted` and one `proposed` file under the same id, so the candidate filter
// showed one file and the operator had no way to learn the second existed.
type Listing struct {
	Pending    []model.KnowledgeObject
	Duplicates []knowledge.DuplicateID
}

// List reads the store once and returns both halves of the ratify view.
func List(repoDir string) (Listing, error) {
	objs, err := knowledge.Load(repoDir)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Pending: pendingOf(objs), Duplicates: knowledge.DuplicateIDs(objs)}, nil
}

// Pending lists proposed objects awaiting ratification, sorted by id.
// Superseded/invalidated candidates and malformed (id-less) objects are
// skipped — the former are dead, the latter are doctor's problem.
func Pending(repoDir string) ([]model.KnowledgeObject, error) {
	objs, err := knowledge.Load(repoDir)
	if err != nil {
		return nil, err
	}
	return pendingOf(objs), nil
}

func pendingOf(objs []model.KnowledgeObject) []model.KnowledgeObject {
	var out []model.KnowledgeObject
	for _, o := range objs {
		if o.ID == "" || o.Status != model.StatusProposed {
			continue
		}
		if o.EffectiveStatus == model.StatusSuperseded || o.EffectiveStatus == model.StatusInvalidated {
			continue
		}
		out = append(out, o)
	}
	// Sorted by id, then by path: two candidates sharing an id must still order
	// deterministically, and the path is what tells them apart on screen.
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Path < out[j].Path
	})
	return out
}
