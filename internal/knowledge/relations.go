package knowledge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// RelationKind classifies a `relates_to` verb by what nugit actually DOES with
// it. The classification is the point: before ADR-0041 the vocabulary existed
// only as examples in prose, and AGENTS.md told authors to write two verbs —
// `prevents:` and `satisfies:` — that no code path reads. An author cannot tell
// a load-bearing edge from a decorative one by looking at the store, and
// nothing said so.
type RelationKind int

const (
	// RelationUnknown is a verb nugit has no rule for. It still resolves in
	// retrieval's one-hop pull (the traversal keys on the target), but nothing
	// else will ever consult it — so a typo'd `ammends:` is indistinguishable
	// from a working amendment until someone reads the rendered lifecycle.
	RelationUnknown RelationKind = iota
	// RelationSemantic drives engine behaviour: derived status, amendment and
	// reinforcement annotations, reference attribution.
	RelationSemantic
	// RelationScope contributes a governed component, widening what the object
	// is retrieved for.
	RelationScope
	// RelationAnnotation is read by retrieval's traversal and nothing else. A
	// legitimate way to say "see also"; just not a lifecycle claim.
	RelationAnnotation
)

func (k RelationKind) String() string {
	switch k {
	case RelationSemantic:
		return "semantic"
	case RelationScope:
		return "scope"
	case RelationAnnotation:
		return "annotation"
	}
	return "unknown"
}

// relations is the declared vocabulary. Adding a verb here without a code path
// that reads it recreates exactly the gap this table closes, so an entry is a
// claim that something consumes it.
var relations = map[string]RelationKind{
	// Semantic — consumed by ResolveAmendedBy, ResolveReinforcedBy, and
	// retrieval's reference attribution.
	"amends":     RelationSemantic,
	"reinforces": RelationSemantic,
	"informs":    RelationSemantic,
	// Scope — consumed by evidence.scopedComponents.
	"constrains": RelationScope,
	"affects":    RelationScope,
	"governs":    RelationScope,
	// Annotation — traversal only. These are grandfathered deliberately:
	// `prevents` and `satisfies` were documented in AGENTS.md for months, and
	// `elaborates` is used seven times in THIS repo's own store — the first run
	// of the check that introduced this table found them. Demoting honest
	// authorship to "unknown" would produce a wall of warnings and teach people
	// to mute the check. They are real "see also" edges; they are simply not
	// lifecycle claims, and the docs now say which verbs are.
	"prevents":   RelationAnnotation,
	"satisfies":  RelationAnnotation,
	"refines":    RelationAnnotation,
	"elaborates": RelationAnnotation,
	"relates":    RelationAnnotation,
	"see":        RelationAnnotation,
}

// misplaced names fields that are front-matter keys in their own right, and so
// mean nothing inside `relates_to`. `supersedes:` is the one that matters: it
// is where ADR-0003 derives effective status from, so writing it as an edge
// looks like a supersession and produces none.
var misplaced = map[string]string{
	"supersedes":       "a top-level `supersedes:` front-matter field",
	"applies_to_paths": "a top-level `applies_to_paths:` front-matter field",
	"scope":            "the top-level `scope:` front-matter field",
}

// RelationOf classifies a verb.
func RelationOf(verb string) RelationKind { return relations[strings.ToLower(strings.TrimSpace(verb))] }

// KnownRelations lists the declared verbs, sorted, each with its kind — for
// error messages and docs that must not drift from the table above.
func KnownRelations() []string {
	out := make([]string, 0, len(relations))
	for v, k := range relations {
		out = append(out, fmt.Sprintf("%s (%s)", v, k))
	}
	sort.Strings(out)
	return out
}

// EdgeProblem is one `relates_to` entry nugit cannot act on.
type EdgeProblem struct {
	Path string // the file carrying it
	ID   string // that object's id
	Raw  string // the entry as written
	Verb string
	// Misplaced is non-empty when the verb names a front-matter field instead
	// (the message explains where it belongs).
	Misplaced string
}

// EdgeStats summarises a store's `relates_to` usage.
type EdgeStats struct {
	Total      int
	Bare       int // no verb at all
	BareFiles  int // objects carrying at least one bare edge
	ByKind     map[RelationKind]int
	Problems   []EdgeProblem
	TotalFiles int
}

// BarePercent is the share of edges carrying no verb, 0 when there are none.
func (s EdgeStats) BarePercent() int {
	if s.Total == 0 {
		return 0
	}
	return s.Bare * 100 / s.Total
}

// EdgeAudit classifies every `relates_to` entry in objs. Pure grouping over
// objects the caller already loaded — no I/O.
func EdgeAudit(objs []model.KnowledgeObject) EdgeStats {
	st := EdgeStats{ByKind: map[RelationKind]int{}, TotalFiles: len(objs)}
	bareFiles := map[string]bool{}
	for _, o := range objs {
		if o.Foreign() {
			continue // a peer's vocabulary is the peer's business (ADR-0032)
		}
		for _, raw := range o.RelatesTo {
			e := ParseEdge(raw)
			st.Total++
			if e.Relation == "" {
				st.Bare++
				bareFiles[o.Path] = true
				continue
			}
			verb := strings.ToLower(strings.TrimSpace(e.Relation))
			if where, bad := misplaced[verb]; bad {
				st.Problems = append(st.Problems, EdgeProblem{
					Path: o.Path, ID: o.ID, Raw: raw, Verb: verb, Misplaced: where,
				})
				continue
			}
			kind := RelationOf(verb)
			st.ByKind[kind]++
			if kind == RelationUnknown {
				st.Problems = append(st.Problems, EdgeProblem{Path: o.Path, ID: o.ID, Raw: raw, Verb: verb})
			}
		}
	}
	st.BareFiles = len(bareFiles)
	sort.Slice(st.Problems, func(i, j int) bool {
		if st.Problems[i].Path != st.Problems[j].Path {
			return st.Problems[i].Path < st.Problems[j].Path
		}
		return st.Problems[i].Raw < st.Problems[j].Raw
	})
	return st
}
