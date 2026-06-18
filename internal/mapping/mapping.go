// Package mapping resolves a repo-relative file path to the C4 component that
// owns it, using the `paths` globs declared on each component in workspace.dsl.
//
// This is the load-bearing primitive the review flagged as missing: three of
// four deltas and two of five consistency checks need a deterministic
// file->component function. Resolution is total and explainable — every path
// maps to exactly one component id or to "" (orphan), never ambiguously.
package mapping

import (
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/burrowfarm/nugit/internal/model"
)

type rule struct {
	comp    string
	pattern string
	// specificity = length of the literal prefix before the first wildcard.
	// Longer literal prefix wins on overlap (most-specific-glob rule).
	spec int
}

// Mapper resolves paths to components.
type Mapper struct {
	rules []rule
}

// New builds a Mapper from a parsed model. Rules are pre-sorted by descending
// specificity so Resolve can return the first match deterministically.
func New(m model.Model) *Mapper {
	var rules []rule
	for _, c := range m.Components {
		for _, g := range c.Paths {
			rules = append(rules, rule{comp: c.ID, pattern: g, spec: literalPrefixLen(g)})
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].spec != rules[j].spec {
			return rules[i].spec > rules[j].spec
		}
		if rules[i].pattern != rules[j].pattern {
			return rules[i].pattern < rules[j].pattern
		}
		return rules[i].comp < rules[j].comp
	})
	return &Mapper{rules: rules}
}

// Resolve returns the owning component id for path, or "" if no glob matches.
func (mp *Mapper) Resolve(path string) string {
	path = strings.TrimPrefix(path, "./")
	for _, r := range mp.rules {
		if ok, _ := doublestar.Match(r.pattern, path); ok {
			return r.comp
		}
	}
	return ""
}

// ResolveDir returns the component that owns an imported package directory, by
// probing a synthetic source file inside it against the path globs.
func (mp *Mapper) ResolveDir(dir string) string {
	dir = strings.TrimPrefix(dir, "./")
	if c := mp.Resolve(dir + "/__pkg__.go"); c != "" {
		return c
	}
	return mp.Resolve(dir)
}

// Empty reports whether the model declared no path bindings at all (in which
// case component grouping and the C4<->code check are not yet meaningful).
func (mp *Mapper) Empty() bool { return len(mp.rules) == 0 }

// literalPrefixLen counts characters before the first glob metacharacter.
func literalPrefixLen(g string) int {
	for i := 0; i < len(g); i++ {
		switch g[i] {
		case '*', '?', '[', '{':
			return i
		}
	}
	return len(g)
}
