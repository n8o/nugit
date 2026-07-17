// Package mapping resolves a repo-relative file path to the C4 component that
// owns it, using the `paths` globs declared on each component in workspace.dsl.
//
// This is the load-bearing primitive the review flagged as missing: three of
// four deltas and two of five consistency checks need a deterministic
// file->component function. Resolution is total and explainable — every path
// maps to exactly one component id or to "" (orphan), never ambiguously.
package mapping

import (
	"path"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/n8o/nugit/internal/model"
)

type rule struct {
	comp    string
	pattern string
	// specificity = length of the literal prefix before the first wildcard.
	// Longer literal prefix wins on overlap (most-specific-glob rule).
	spec int
	// level: 0 = component, 1 = container. At equal literal-prefix specificity
	// a component glob beats a container glob (the finer element owns the file).
	level int
}

// InvalidPattern names a component or container glob that failed validation.
type InvalidPattern struct {
	Comp    string
	Pattern string
	// Kind is "component" or "container" — which element declared the glob.
	Kind string
}

// Mapper resolves paths to components.
type Mapper struct {
	rules   []rule
	invalid []InvalidPattern
}

// New builds a Mapper from a parsed model. Both component and container path
// globs are ingested — a path owned only by a container resolves to the
// container id. Rules are pre-sorted by descending specificity (component
// before container on ties) so Resolve can return the first match
// deterministically. Globs that fail validation are dropped and recorded in
// InvalidPatterns().
func New(m model.Model) *Mapper {
	var rules []rule
	var invalid []InvalidPattern
	for _, c := range m.Components {
		for _, g := range c.Paths {
			if doublestar.ValidatePattern(g) {
				rules = append(rules, rule{comp: c.ID, pattern: g, spec: literalPrefixLen(g)})
			} else {
				invalid = append(invalid, InvalidPattern{Comp: c.ID, Pattern: g, Kind: "component"})
			}
		}
	}
	for _, ct := range m.Containers {
		for _, g := range ct.Paths {
			if doublestar.ValidatePattern(g) {
				rules = append(rules, rule{comp: ct.ID, pattern: g, spec: literalPrefixLen(g), level: 1})
			} else {
				invalid = append(invalid, InvalidPattern{Comp: ct.ID, Pattern: g, Kind: "container"})
			}
		}
	}
	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].spec != rules[j].spec {
			return rules[i].spec > rules[j].spec
		}
		if rules[i].level != rules[j].level {
			return rules[i].level < rules[j].level
		}
		if rules[i].pattern != rules[j].pattern {
			return rules[i].pattern < rules[j].pattern
		}
		return rules[i].comp < rules[j].comp
	})
	return &Mapper{rules: rules, invalid: invalid}
}

// InvalidPatterns returns component globs that failed validation (and therefore
// match nothing). Sorted for deterministic output.
func (mp *Mapper) InvalidPatterns() []InvalidPattern {
	out := append([]InvalidPattern(nil), mp.invalid...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Comp != out[j].Comp {
			return out[i].Comp < out[j].Comp
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
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

// ResolveDir returns the component that owns an imported package directory. It
// probes a synthetic source file (for `dir/**` globs), then the bare dir, then
// falls back to matching the directory portion of a single-file glob.
func (mp *Mapper) ResolveDir(dir string) string {
	// path.Clean collapses a nested module-root import like "apps/op/." -> "apps/op"
	// (and "." -> ".") so the root package resolves; no-op for clean inputs.
	dir = path.Clean(dir)
	if c := mp.Resolve(dir + "/__pkg__.go"); c != "" {
		return c
	}
	if c := mp.Resolve(dir); c != "" {
		return c
	}
	// fallback: a component bound by an exact-file glob like "a/a.go" owns dir
	// "a" — match the literal directory portion of each rule (most-specific first).
	for _, r := range mp.rules {
		if patternDir(r.pattern) == dir {
			return r.comp
		}
	}
	return ""
}

// patternDir returns the literal directory portion of a glob (up to the last
// slash before the first wildcard).
func patternDir(g string) string {
	lit := g[:literalPrefixLen(g)]
	if i := strings.LastIndexByte(lit, '/'); i >= 0 {
		return lit[:i]
	}
	return ""
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
