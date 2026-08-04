// Package reinforce mints the recurrence answer to right-but-too-narrow
// knowledge (ADR-0019): a NEW appended-only record that re-confirms a live
// target after its failure class recurred and widens the keywords retrieval
// matches on. The target is never mutated — its bytes, status and history are
// untouched (ADR-0003), so concurrent reinforcements are disjoint new files
// that merge cleanly, and the loader derives ReinforcedBy from the reverse
// `reinforces:` edge exactly as it derives AmendedBy (ADR-0015).
package reinforce

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// Options configure one reinforcement.
type Options struct {
	RepoDir  string
	ID       string   // target object id (required)
	Text     string   // the reinforcement insight — what the recurrence taught (required)
	Keywords []string // widen the retrieval surface of the target
	// Status is the minted status: "proposed" (default — candidate lane,
	// ADR-0016) or "ratified" (lands active, mirroring distill's escape hatch).
	Status string
	Now    string // ISO timestamp for created: (testable); "" -> time.Now()
}

// Result reports the minted reinforcement.
type Result struct {
	ID     string // new object id (<target>-R<n>)
	Path   string // repo-relative path of the new file
	Target string // reinforced object id
}

// Reinforce writes the reinforcement object next to its target. It refuses
// unknown ids, untyped/glossary targets, and dead (superseded/invalidated)
// targets — a recurrence against a dead record means its successor needs the
// reinforcement.
func Reinforce(opt Options) (Result, error) {
	if strings.TrimSpace(opt.ID) == "" {
		return Result{}, fmt.Errorf("target id is required")
	}
	if strings.TrimSpace(opt.Text) == "" {
		return Result{}, fmt.Errorf("-text is required (what did the recurrence teach?)")
	}
	status := model.StatusProposed
	switch opt.Status {
	case "", "proposed":
	case "ratified":
		status = model.StatusActive
	default:
		return Result{}, fmt.Errorf("unknown status %q (want proposed or ratified)", opt.Status)
	}

	objs, err := knowledge.Load(opt.RepoDir)
	if err != nil {
		return Result{}, err
	}
	var target *model.KnowledgeObject
	taken := map[string]bool{}
	for i := range objs {
		if objs[i].ID != "" {
			taken[objs[i].ID] = true
		}
		if objs[i].ID == opt.ID {
			target = &objs[i]
		}
	}
	if target == nil {
		return Result{}, fmt.Errorf("no knowledge object with id %q", opt.ID)
	}
	if target.Type == model.KindGlossary || target.Type == "" {
		return Result{}, fmt.Errorf("%s is a %q object — only lessons/decisions/specs/references can be reinforced", opt.ID, target.Type)
	}
	if target.EffectiveStatus == model.StatusSuperseded || target.EffectiveStatus == model.StatusInvalidated {
		return Result{}, fmt.Errorf("%s is %s — reinforce its successor instead", opt.ID, target.EffectiveStatus)
	}

	// Next free -R<n>, in both id-space and path-space (concurrent
	// reinforcements on different branches may collide; the loop just walks on).
	base := strings.TrimSuffix(filepath.Base(target.Path), filepath.Ext(target.Path))
	dir := filepath.Dir(target.Path)
	var id, rel string
	for n := len(target.ReinforcedBy) + 1; ; n++ {
		id = fmt.Sprintf("%s-R%d", target.ID, n)
		rel = filepath.Join(dir, fmt.Sprintf("%s-r%d.md", base, n))
		_, statErr := os.Stat(filepath.Join(opt.RepoDir, rel))
		if !taken[id] && statErr != nil {
			break // id unused and no file in the way
		}
	}

	now := opt.Now
	if now == "" {
		now = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	content := body(id, target, status, now, opt.Text, opt.Keywords)
	abs := filepath.Join(opt.RepoDir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Result{}, err
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return Result{}, err
	}
	return Result{ID: id, Path: rel, Target: target.ID}, nil
}

// body renders the reinforcement record: a small lesson-typed object whose
// own body carries the widened keywords (retrieval keyword-matches an
// object's body, then follows the reinforces: edge one hop to the target).
func body(id string, target *model.KnowledgeObject, status model.Status, now, text string, keywords []string) string {
	scope := target.Scope
	if scope == "" {
		scope = "global"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: lesson\nscope: %s\nstatus: %s\ncreated: %s\n",
		id, scope, status, now)
	fmt.Fprintf(&b, "relates_to:\n  - reinforces:%s\n", target.ID)
	fmt.Fprintf(&b, "provenance:\n  commit: reinforce\n  citation: nugit reinforce %s\nconfidence: medium\n---\n\n", target.ID)
	fmt.Fprintf(&b, "# Reinforcement — %s (%s)\n\n", target.ID, now[:10])
	fmt.Fprintf(&b, "**Insight:** %s\n", text)
	if len(keywords) > 0 {
		fmt.Fprintf(&b, "\n**Keywords:** %s\n", strings.Join(keywords, ", "))
	}
	return b.String()
}
