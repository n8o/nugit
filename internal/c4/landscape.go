package c4

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/n8o/nugit/internal/model"
)

// This file implements the ORG-LEVEL landscape (ADR-0034): a SEPARATE artifact
// that sits above the per-repo C4 model and describes systems the org's repos
// share, who owns them, and which paths configure them.
//
// It is deliberately a distinct parse entry point with distinct output types.
// `Parse` — the per-repo model parser — is untouched and keeps treating
// `softwareSystem` as transparent (ADR-0017's single-system subset). The only
// thing the two share is the tokenizer. Nothing here reaches Covered(), the
// mapper, gen-rules, the C4 delta, or the c4<->code gate; feeding a
// landscape.dsl to Parse yields an EMPTY model, which is pinned by a test.

// LandscapePath is where a repo's landscape lives, relative to the nugit root.
// Fixed on purpose: unlike `c4.dsl` this is an ORG-level artifact, and a
// per-repo knob for its location would be one more thing two repos can disagree
// about (ADR-0034 point 1).
const LandscapePath = ".nugit/architecture/landscape.dsl"

// Landscape property keys. Kept as constants because doctor, retrieval, and the
// ownership check all have to agree on the exact spelling.
const (
	propRepo  = "nugit_repo"
	propOwner = "nugit_owner"
	propPaths = "nugit_paths"
)

// ParseLandscape parses landscape.dsl at the SYSTEM level only. Tolerant like
// Parse: unknown statements are skipped and malformed input degrades to
// whatever was understood, never a panic and never an error — an org artifact
// this repo may not own must not be able to break this repo's tooling.
func ParseLandscape(src string) model.Landscape {
	lp := &landscapeParser{toks: lex(src)}
	lp.parse()
	return lp.l
}

type landscapeParser struct {
	toks []token
	pos  int
	l    model.Landscape
}

func (p *landscapeParser) peek() (token, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos], true
	}
	return token{}, false
}

func (p *landscapeParser) next() (token, bool) {
	t, ok := p.peek()
	if ok {
		p.pos++
	}
	return t, ok
}

// parse walks the token stream, descending transparently through
// workspace/model headers (exactly as Parse does) and recording only
// softwareSystem declarations and system-level relationships.
func (p *landscapeParser) parse() {
	for {
		t, ok := p.next()
		if !ok {
			return
		}
		if t.kind != tWord {
			continue
		}
		// views/configuration/styles carry their own arrows and element refs;
		// they must never leak into the model (the Parse regression, reproduced
		// here because the landscape has the same hazard).
		if t.val == "views" || t.val == "configuration" || t.val == "styles" {
			if nt, ok := p.peek(); ok && nt.kind == tLBrace {
				p.next()
				p.skipLandscapeBlock()
			}
			continue
		}
		if nt, ok := p.peek(); ok && nt.kind == tEq {
			p.next() // '='
			kw, ok := p.next()
			if !ok || kw.kind != tWord {
				continue
			}
			if kw.val == "softwareSystem" {
				p.parseSystem(t.val)
				continue
			}
			// Anything else at this level (person, deploymentNode, a stray
			// container) is not part of the landscape subset: consume its
			// positional args and skip its body wholesale, so nothing inside a
			// non-system element is mistaken for a system-level fact.
			p.landscapeStrings()
			if b, ok := p.peek(); ok && b.kind == tLBrace {
				p.next()
				p.skipLandscapeBlock()
			}
			continue
		}
		if nt, ok := p.peek(); ok && nt.kind == tArrow {
			p.next()
			p.parseLandscapeRel(t.val)
		}
	}
}

// parseSystem records one `id = softwareSystem "Name" ["Desc"]` and its block.
func (p *landscapeParser) parseSystem(id string) {
	strs := p.landscapeStrings()
	s := model.LandscapeSystem{ID: id}
	if len(strs) > 0 {
		s.Name = strs[0]
	}
	if len(strs) > 1 {
		s.Desc = strs[1]
	}
	if t, ok := p.peek(); ok && t.kind == tLBrace {
		p.next()
		p.parseSystemBody(&s)
	}
	p.l.Systems = append(p.l.Systems, s)
}

// parseSystemBody reads a system's block: its properties, its description, and
// relationships authored inside it. Containers and components nested in a
// system are SKIPPED — the landscape is a system-level artifact, and the
// per-repo model is the only place those levels are modelled (ADR-0034 point 2).
func (p *landscapeParser) parseSystemBody(s *model.LandscapeSystem) {
	for {
		t, ok := p.next()
		if !ok || t.kind == tRBrace {
			return
		}
		if t.kind != tWord {
			if t.kind == tLBrace {
				p.skipLandscapeBlock()
			}
			continue
		}
		switch t.val {
		case "description":
			if v, ok := p.peek(); ok && v.kind == tStr {
				p.next()
				s.Desc = v.val
			}
		case "properties":
			p.parseSystemProperties(s)
		default:
			if a, ok := p.peek(); ok && a.kind == tArrow {
				p.next()
				p.parseLandscapeRel(t.val)
				continue
			}
			// `x = container ...`, `tags "..."`, anything else: consume any
			// positional strings and skip a body if one opens.
			p.landscapeStrings()
			if b, ok := p.peek(); ok && b.kind == tLBrace {
				p.next()
				p.skipLandscapeBlock()
			}
		}
	}
}

// parseSystemProperties reads the nugit_* keys. Unknown keys are ignored (never
// merged into anything): the landscape has no model-properties escape hatch,
// so a typo'd key is inert rather than silently meaningful somewhere else.
func (p *landscapeParser) parseSystemProperties(s *model.LandscapeSystem) {
	t, ok := p.peek()
	if !ok || t.kind != tLBrace {
		return
	}
	p.next() // '{'
	for {
		k, ok := p.next()
		if !ok || k.kind == tRBrace {
			return
		}
		// Structurizr requires quoted property keys; the bare form nugit has
		// always also accepted is honored here too.
		if k.kind != tWord && k.kind != tStr {
			continue
		}
		v, ok := p.peek()
		if !ok || v.kind != tStr {
			continue
		}
		p.next()
		switch k.val {
		case propRepo:
			s.Repo = strings.TrimSpace(v.val)
		case propOwner:
			s.Owner = strings.TrimSpace(v.val)
		case propPaths:
			s.Paths = append(s.Paths, splitCommaList(v.val)...)
		}
	}
}

func (p *landscapeParser) parseLandscapeRel(src string) {
	dst, ok := p.next()
	if !ok || (dst.kind != tWord && dst.kind != tStr) {
		return
	}
	rel := model.LandscapeRel{Src: src, Dst: dst.val}
	if d, ok := p.peek(); ok && d.kind == tStr {
		p.next()
		rel.Desc = d.val
	}
	for { // consume any further positional strings (technology/tags)
		s, ok := p.peek()
		if !ok || s.kind != tStr {
			break
		}
		p.next()
	}
	p.l.Rels = append(p.l.Rels, rel)
}

func (p *landscapeParser) landscapeStrings() []string {
	var strs []string
	for {
		t, ok := p.peek()
		if !ok || t.kind != tStr {
			break
		}
		p.next()
		strs = append(strs, t.val)
	}
	return strs
}

func (p *landscapeParser) skipLandscapeBlock() {
	depth := 1
	for depth > 0 {
		t, ok := p.next()
		if !ok {
			return
		}
		switch t.kind {
		case tLBrace:
			depth++
		case tRBrace:
			depth--
		}
	}
}

// ---- path binding ----

// LandscapeGlob names a system glob that failed validation, mirroring
// mapping.InvalidPattern: a syntactically invalid glob matches nothing, so the
// system's binding is dead and must be REPORTED, never silently dropped
// (ADR-0020 point 5's discipline, applied one layer up).
type LandscapeGlob struct {
	System  string
	Pattern string
}

// LandscapeInvalidGlobs returns the systems' invalid `nugit_paths` globs,
// sorted for deterministic output.
func LandscapeInvalidGlobs(l model.Landscape) []LandscapeGlob {
	var out []LandscapeGlob
	for _, s := range l.Systems {
		for _, g := range s.Paths {
			if !doublestar.ValidatePattern(g) {
				out = append(out, LandscapeGlob{System: s.ID, Pattern: g})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return out[i].System < out[j].System
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

// ConfiguresPath reports whether path matches any of the system's nugit_paths
// globs. Invalid globs match nothing (they are reported separately), exactly as
// in internal/mapping.
func ConfiguresPath(s model.LandscapeSystem, path string) bool {
	path = strings.TrimPrefix(path, "./")
	for _, g := range s.Paths {
		if !doublestar.ValidatePattern(g) {
			continue
		}
		if ok, _ := doublestar.Match(g, path); ok {
			return true
		}
	}
	return false
}

// Configuring returns the landscape systems that path configures, sorted by id.
// Every system is a candidate, shared or not — callers decide what to do with
// ownership.
func Configuring(l model.Landscape, path string) []model.LandscapeSystem {
	var out []model.LandscapeSystem
	for _, s := range l.Systems {
		if ConfiguresPath(s, path) {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ConfiguringAny returns, for each system, the subset of paths that configure
// it — the shape the ownership finding needs (it must name the matched files).
// Systems are sorted by id and each system's matched paths are sorted.
func ConfiguringAny(l model.Landscape, paths []string) map[string][]string {
	out := map[string][]string{}
	for _, s := range l.Systems {
		var hit []string
		for _, p := range paths {
			if ConfiguresPath(s, p) {
				hit = append(hit, p)
			}
		}
		if len(hit) > 0 {
			sort.Strings(hit)
			out[s.ID] = dedupStrings(hit)
		}
	}
	return out
}

func dedupStrings(in []string) []string {
	var out []string
	var prev string
	for i, s := range in {
		if i > 0 && s == prev {
			continue
		}
		prev = s
		out = append(out, s)
	}
	return out
}

// ---- resolution (ADR-0011 single-writer) ----

// LandscapeSource is one candidate landscape: a namespace ("" for this repo,
// else the peer's display name) and the DSL source already read from wherever
// it lives. Callers read the LOCAL one at the reviewed git ref where they can
// and a peer's from its checkout — this package never touches git.
type LandscapeSource struct {
	Name string
	Path string
	Src  string
	// Hub marks this source as the organization's designated canonical store
	// (config `org.hub`, ADR-0035). A hub's landscape wins outright over any
	// other peer's — see ResolveLandscape.
	Hub bool
}

// LandscapeResolution is the single authoritative landscape for one repo's
// view, plus what was rejected getting there.
type LandscapeResolution struct {
	Landscape model.Landscape
	Found     bool
	// From is "" for the local landscape, else the peer name it came from.
	From string
	// Path is where the winning landscape was read from.
	Path string
	// Ambiguous names every peer that declared a landscape when this repo has
	// none and more than one peer claimed it. When set, Found is FALSE and
	// nothing is used — see ResolveLandscape.
	Ambiguous []string
	// FromHub reports that the winning landscape came from the designated org
	// hub (ADR-0035) rather than from an ordinary peer.
	FromHub bool
}

// ResolveLandscape picks the one authoritative landscape (ADR-0034 point 3, as
// narrowed by ADR-0035 point 1):
//
//  1. a LOCAL landscape (Name == "") always wins;
//  2. otherwise, the designated HUB's landscape wins outright — over any number
//     of other peers, with no ambiguity;
//  3. otherwise, exactly one peer declaring one wins;
//  4. otherwise — two or more non-hub peers each declaring one — NOTHING is used
//     and every claimant is named in Ambiguous.
//
// Rule 4 is what rule 2 exists to retire. ADR-0034 had to fail closed on two
// claimants because no peer was privileged: picking the first in configured
// order would have made the org's shared model depend on the READER's private,
// reorderable peer list. A hub IS privileged, by an explicit act of designation
// in this repo's own config, so "which of these is canonical" stops being a tie
// for nugit to break and becomes a fact the reader stated. Without a hub the old
// rule stands unchanged, and a hub that declares no landscape (or is not checked
// out) resolves exactly as if it were an ordinary peer.
//
// A source whose Src parses to zero systems does not count as a declaration:
// an empty or unparseable file is not a claim. That applies to the hub too — a
// hub with no landscape.dsl does not suppress a single other peer's.
func ResolveLandscape(srcs []LandscapeSource) LandscapeResolution {
	var res LandscapeResolution
	type cand struct {
		src LandscapeSource
		l   model.Landscape
	}
	var peers []cand
	for _, s := range srcs {
		l := ParseLandscape(s.Src)
		if l.Empty() {
			continue
		}
		if s.Name == "" {
			l.Path = s.Path
			return LandscapeResolution{Landscape: l, Found: true, Path: s.Path}
		}
		peers = append(peers, cand{s, l})
	}
	for _, c := range peers {
		if c.src.Hub {
			l := c.l
			l.Origin, l.Path = c.src.Name, c.src.Path
			return LandscapeResolution{Landscape: l, Found: true, From: l.Origin, Path: l.Path, FromHub: true}
		}
	}
	switch len(peers) {
	case 0:
		return res
	case 1:
		l := peers[0].l
		l.Origin, l.Path = peers[0].src.Name, peers[0].src.Path
		return LandscapeResolution{Landscape: l, Found: true, From: l.Origin, Path: l.Path}
	}
	for _, c := range peers {
		res.Ambiguous = append(res.Ambiguous, c.src.Name)
	}
	sort.Strings(res.Ambiguous)
	return res
}

// ReadLandscape reads a checkout's landscape.dsl from disk. ok is false when
// there is none — which is the normal state for most repos and every repo that
// has not adopted phase 3.
func ReadLandscape(dir string) (path, src string, ok bool) {
	rel := filepath.Join(dir, filepath.FromSlash(LandscapePath))
	b, err := os.ReadFile(rel)
	if err != nil {
		return "", "", false
	}
	return LandscapePath, string(b), true
}

// LandscapeDir is one checkout a landscape may be read from: a namespace ("" =
// this repo, else the peer's display name) and the nugit root it lives in.
type LandscapeDir struct {
	Name string
	Dir  string
	// Hub marks the org's designated canonical store (ADR-0035).
	Hub bool
}

// LandscapeSourcesFromDirs builds ResolveLandscape's input by reading each
// checkout from DISK, local first. An absent landscape (or an absent peer
// checkout entirely) contributes nothing and can never error — the normal CI
// state is that only the repo under review is checked out (ADR-0032 point 3).
//
// PR-time callers do NOT use this for the local source: they read their own
// landscape at the reviewed ref and prepend it themselves.
func LandscapeSourcesFromDirs(dirs []LandscapeDir) []LandscapeSource {
	var out []LandscapeSource
	for _, d := range dirs {
		path, src, ok := ReadLandscape(d.Dir)
		if !ok {
			continue
		}
		out = append(out, LandscapeSource{Name: d.Name, Path: path, Src: src, Hub: d.Hub})
	}
	return out
}

// ---- render ----

// landscapeEdgeEsc extends the Mermaid label escaping for EDGE labels, where a
// pipe additionally closes the label and breaks GitHub's parser.
var landscapeEdgeEsc = strings.NewReplacer("|", "&#124;")

// LandscapeMermaid renders the landscape as a Mermaid `graph LR`, following
// render.go's conventions: deterministic ordering, entity-escaped labels, no
// styling that depends on a theme. Shared systems (those with a declared owner)
// get the subroutine node shape and carry their owner in the label, so "who
// owns this" is readable straight off the diagram.
func LandscapeMermaid(l model.Landscape) string {
	systems := append([]model.LandscapeSystem(nil), l.Systems...)
	sort.Slice(systems, func(i, j int) bool { return systems[i].ID < systems[j].ID })
	rels := append([]model.LandscapeRel(nil), l.Rels...)
	sort.Slice(rels, func(i, j int) bool {
		if rels[i].Src != rels[j].Src {
			return rels[i].Src < rels[j].Src
		}
		if rels[i].Dst != rels[j].Dst {
			return rels[i].Dst < rels[j].Dst
		}
		return rels[i].Desc < rels[j].Desc
	})

	var b strings.Builder
	b.WriteString("graph LR\n")
	for _, s := range systems {
		label := mermaidLabel(landscapeLabel(s))
		if s.Shared() {
			b.WriteString("  " + s.ID + "[[\"" + label + "\"]]\n")
			continue
		}
		b.WriteString("  " + s.ID + "[\"" + label + "\"]\n")
	}
	for _, r := range rels {
		if r.Desc == "" {
			b.WriteString("  " + r.Src + " --> " + r.Dst + "\n")
			continue
		}
		b.WriteString("  " + r.Src + " -->|" +
			landscapeEdgeEsc.Replace(mermaidLabel(r.Desc)) + "| " + r.Dst + "\n")
	}
	return b.String()
}

// landscapeLabel says, in one line, what the system is and who is accountable.
func landscapeLabel(s model.LandscapeSystem) string {
	name := s.Name
	if name == "" {
		name = s.ID
	}
	switch {
	case s.Shared():
		return name + " (shared · owned by " + s.Owner + ")"
	case s.Repo != "":
		return name + " (repo " + s.Repo + ")"
	}
	return name
}
