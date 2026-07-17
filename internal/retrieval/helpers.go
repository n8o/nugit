package retrieval

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

func readFile(repoDir, rel string) (string, error) {
	b, err := os.ReadFile(filepath.Join(repoDir, rel))
	return string(b), err
}

// c4Slice returns the component and its immediate (one-hop) relationships.
func c4Slice(m model.Model, comp string) C4Slice {
	s := C4Slice{Component: comp}
	if comp == "" {
		return s
	}
	for _, r := range m.Relationships {
		switch comp {
		case r.Src:
			s.DependsOn = append(s.DependsOn, r.Dst)
		case r.Dst:
			s.DependedOnBy = append(s.DependedOnBy, r.Src)
		}
	}
	sort.Strings(s.DependsOn)
	sort.Strings(s.DependedOnBy)
	return s
}

// inScope reports whether an object applies to the scope chain: comp itself,
// comp's parent container, or global. Flat models pass parent == "".
func inScope(o *model.KnowledgeObject, comp, parent string) bool {
	switch o.Scope {
	case "", "global":
		return true
	case comp:
		return true
	}
	return parent != "" && o.Scope == parent
}

// relevant reports whether a spec is the active spec for the scope chain
// (scoped or linked to the component or its parent container), not just any
// global spec.
func relevant(o *model.KnowledgeObject, comp, parent string) bool {
	for _, s := range []string{comp, parent} {
		if s == "" {
			continue
		}
		if o.Scope == s {
			return true
		}
		for _, e := range o.RelatesTo {
			if knowledge.ParseEdge(e).Target == s {
				return true
			}
		}
	}
	// A global/unscoped spec serves a path that has no component-scoped spec.
	return o.Scope == "" || o.Scope == "global"
}

func toItem(o *model.KnowledgeObject, via string) Item {
	it := Item{
		ID:        o.ID,
		Type:      string(o.Type),
		Scope:     o.Scope,
		Status:    string(effectiveStatus(o)),
		Tier:      string(o.Evidence),
		Path:      o.Path,
		Summary:   summary(o),
		Rejected:  o.Rejected,
		Via:       via,
		AmendedBy: o.AmendedBy,
	}
	it.tokens = tokensOf(it.Summary) + tokensOf(it.Rejected) + tokensOf(it.ID) +
		tokensOf(strings.Join(it.AmendedBy, " ")) + 8
	return it
}

func effectiveStatus(o *model.KnowledgeObject) model.Status {
	if o.EffectiveStatus != "" {
		return o.EffectiveStatus
	}
	return o.Status
}

// summary is the object's first markdown heading (its title), or the first
// non-empty body line.
func summary(o *model.KnowledgeObject) string {
	for _, line := range strings.Split(o.Body, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		t = strings.TrimLeft(t, "# ")
		if len(t) > 160 {
			t = t[:160] + "…"
		}
		return t
	}
	return o.ID
}

// keywords lowercases and tokenizes the task into a set.
func keywords(task string) map[string]bool {
	if strings.TrimSpace(task) == "" {
		return nil
	}
	kw := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(task), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	}) {
		if len(w) >= 3 { // skip stop-ish short tokens
			kw[w] = true
		}
	}
	return kw
}

// matches reports whether a lesson overlaps the task keywords (in its body/id).
func matches(o *model.KnowledgeObject, kw map[string]bool) bool {
	hay := strings.ToLower(o.Body + " " + o.ID)
	for w := range kw {
		if strings.Contains(hay, w) {
			return true
		}
	}
	return false
}

// glossaryTerms returns the "- **term** — def" bullets in play (matching the
// task/path), or all of them when no task is given.
func glossaryTerms(o *model.KnowledgeObject, task, path string) []string {
	kw := keywords(task + " " + path)
	var out []string
	for _, line := range strings.Split(o.Body, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "- **") {
			continue
		}
		if len(kw) == 0 || lineMatches(t, kw) {
			out = append(out, t)
		}
	}
	return out
}

func lineMatches(line string, kw map[string]bool) bool {
	low := strings.ToLower(line)
	for w := range kw {
		if strings.Contains(low, w) {
			return true
		}
	}
	return false
}

// sortItems orders nearer-scope (component-scoped) before global, current status
// before superseded, then by ID — so truncation drops the least-relevant first.
func sortItems(items []Item) {
	sort.SliceStable(items, func(i, j int) bool {
		gi, gj := scopeRank(items[i]), scopeRank(items[j])
		if gi != gj {
			return gi < gj
		}
		si, sj := statusRank(items[i].Status), statusRank(items[j].Status)
		if si != sj {
			return si < sj
		}
		return items[i].ID < items[j].ID
	})
}

func scopeRank(it Item) int {
	if it.Scope == "" || it.Scope == "global" {
		return 1
	}
	return 0 // component-scoped is nearer
}

func statusRank(s string) int {
	switch s {
	case "superseded", "invalidated":
		return 2
	case "proposed", "draft":
		return 1
	default: // active, accepted
		return 0
	}
}

func dedup(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// tokensOf is a coarse rune-based token estimate (~4 chars/token), rune-counted
// so multibyte content (em-dashes, unicode) isn't over-counted ~3x.
func tokensOf(s string) int { return utf8.RuneCountInString(s)/4 + 1 }

func firstWord(s string) string {
	f := strings.Fields(s)
	if len(f) > 0 {
		return f[0]
	}
	return s
}
