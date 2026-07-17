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
	case model.KindDecision, model.KindSpec:
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
	var obj *model.KnowledgeObject
	for i := range objs {
		if objs[i].ID == id {
			obj = &objs[i]
			break
		}
	}
	if obj == nil {
		return Result{}, fmt.Errorf("no knowledge object with id %q", id)
	}
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

// Pending lists proposed objects awaiting ratification, sorted by id.
// Superseded/invalidated candidates and malformed (id-less) objects are
// skipped — the former are dead, the latter are doctor's problem.
func Pending(repoDir string) ([]model.KnowledgeObject, error) {
	objs, err := knowledge.Load(repoDir)
	if err != nil {
		return nil, err
	}
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
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
