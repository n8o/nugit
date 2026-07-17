// Package c4 parses the subset of Structurizr DSL that nugit needs (components,
// their path bindings, and relationships) and computes the structural delta
// between two versions of workspace.dsl.
//
// The path binding — `properties { paths "internal/render/**" }` on each
// component — is nugit's answer to the review's missing primitive: Structurizr
// DSL has no native source-location field, so we carry globs in the generic
// properties block. See .nugit/decisions/0002-file-to-component-binding.md.
package c4

import (
	"strings"

	"github.com/n8o/nugit/internal/model"
)

// Parse parses workspace.dsl source into a Model. It is tolerant: unknown
// statements are skipped, so it degrades gracefully on richer real-world DSL.
func Parse(src string) model.Model {
	toks := lex(src)
	p := &parser{toks: toks}
	return p.parse()
}

// ---- tokenizer ----

type tokKind int

const (
	tWord tokKind = iota // identifier or keyword (may contain . _ -)
	tStr                 // quoted string (value without quotes)
	tLBrace
	tRBrace
	tEq
	tArrow
)

type token struct {
	kind tokKind
	val  string
}

func lex(src string) []token {
	var toks []token
	i, n := 0, len(src)
	for i < n {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < n && src[i+1] == '/':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '#':
			for i < n && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			i += 2
			for i+1 < n && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
		case c == '"':
			i++
			start := i
			var sb strings.Builder
			for i < n && src[i] != '"' {
				if src[i] == '\\' && i+1 < n {
					i++
				}
				sb.WriteByte(src[i])
				i++
			}
			_ = start
			i++ // closing quote
			toks = append(toks, token{tStr, sb.String()})
		case c == '{':
			toks = append(toks, token{tLBrace, "{"})
			i++
		case c == '}':
			toks = append(toks, token{tRBrace, "}"})
			i++
		case c == '=':
			toks = append(toks, token{tEq, "="})
			i++
		case c == '-' && i+1 < n && src[i+1] == '>':
			toks = append(toks, token{tArrow, "->"})
			i += 2
		default:
			start := i
			for i < n {
				d := src[i]
				if d == ' ' || d == '\t' || d == '\r' || d == '\n' ||
					d == '{' || d == '}' || d == '=' || d == '"' {
					break
				}
				if d == '-' && i+1 < n && src[i+1] == '>' {
					break
				}
				i++
			}
			if i > start {
				toks = append(toks, token{tWord, src[start:i]})
			} else {
				i++ // safety: never stall
			}
		}
	}
	return toks
}

// ---- parser ----

type parser struct {
	toks []token
	pos  int
	m    model.Model
}

func (p *parser) peek() (token, bool) {
	if p.pos < len(p.toks) {
		return p.toks[p.pos], true
	}
	return token{}, false
}

func (p *parser) next() (token, bool) {
	t, ok := p.peek()
	if ok {
		p.pos++
	}
	return t, ok
}

// `component` carries a path binding and is recorded as a nugit component.
// `container` is ALSO recorded — as a first-class model.Container in its own
// slice (never as a component, so flat consumers are untouched): the parser
// descends into its body, records the components inside with their parent
// container id, and routes container-level `properties` keys other than
// paths/path to the model properties (the transparent-container behavior
// Structural() has always relied on). softwareSystem/person and system-level
// `group` blocks stay transparent.
var elementKeywords = map[string]bool{
	"component": true,
}

func (p *parser) parse() model.Model {
	for {
		t, ok := p.next()
		if !ok {
			break
		}
		switch t.kind {
		case tWord:
			// The views/configuration blocks contain their own arrows and element
			// references (dynamic views, `include a->b` filters) that must NOT be
			// recorded as model relationships/components — skip the whole subtree.
			if t.val == "views" || t.val == "configuration" || t.val == "styles" {
				if nt, ok := p.peek(); ok && nt.kind == tLBrace {
					p.next()
					p.skipBlockBody()
				}
				continue
			}
			// A model/workspace-level `properties { key "val" }` block (component
			// properties are handled inside parseComponentBlock, not here).
			if t.val == "properties" {
				if nt, ok := p.peek(); ok && nt.kind == tLBrace {
					p.parseModelProperties()
				}
				continue
			}
			// Two interesting shapes start with a word:
			//   IDENT = component "Name" ...      (element declaration)
			//   IDENT -> IDENT ["desc"]           (relationship)
			if nt, ok := p.peek(); ok && nt.kind == tEq {
				p.parseAssignment(t.val)
			} else if nt, ok := p.peek(); ok && nt.kind == tArrow {
				p.next() // consume arrow
				p.parseRelationship(t.val)
			}
			// `workspace` / `model` / `softwareSystem` headers: fall through and
			// descend into their blocks via the main loop.
		case tArrow:
			// stray arrow; ignore
		}
	}
	return p.m
}

func (p *parser) parseAssignment(id string) {
	p.next() // consume '='
	kw, ok := p.next()
	if !ok || kw.kind != tWord {
		return
	}
	if kw.val == "container" {
		p.parseContainer(id)
		return
	}
	if !elementKeywords[kw.val] {
		// e.g. softwareSystem / person — descend but don't record as component.
		// Still consume an optional block so its inner components are seen.
		p.consumeOptionalBlock()
		return
	}
	p.m.Components = append(p.m.Components, p.parseComponent(id))
}

// parseComponent parses a component declaration after `id = component`.
func (p *parser) parseComponent(id string) model.Component {
	// collect positional strings: name [description] [technology]
	strs := p.positionalStrings()
	comp := model.Component{ID: id}
	if len(strs) > 0 {
		comp.Name = strs[0]
	}
	if len(strs) > 2 {
		comp.Tech = strs[2]
	}
	if len(strs) > 3 { // positional 4th arg is a comma-separated tag list
		comp.Tags = append(comp.Tags, splitCommaList(strs[3])...)
	}
	// optional block with technology/tags/properties
	if t, ok := p.peek(); ok && t.kind == tLBrace {
		p.parseComponentBlock(&comp)
	}
	return comp
}

// parseContainer parses a container declaration after `id = container` into a
// first-class model.Container (positional strings mirror parseComponent).
func (p *parser) parseContainer(id string) {
	strs := p.positionalStrings()
	ct := model.Container{ID: id}
	if len(strs) > 0 {
		ct.Name = strs[0]
	}
	if len(strs) > 2 {
		ct.Tech = strs[2]
	}
	if len(strs) > 3 { // positional 4th arg is a comma-separated tag list
		ct.Tags = append(ct.Tags, splitCommaList(strs[3])...)
	}
	if t, ok := p.peek(); ok && t.kind == tLBrace {
		p.next() // consume '{'
		p.parseContainerBody(&ct)
	}
	p.m.Containers = append(p.m.Containers, ct)
}

// parseContainerBody consumes a container block, recording nested components
// (tagged with the container id), relationships, and container metadata.
// `group` blocks inside a container are transparent — their contents belong to
// the same container.
func (p *parser) parseContainerBody(ct *model.Container) {
	for {
		t, ok := p.next()
		if !ok || t.kind == tRBrace {
			return
		}
		if t.kind != tWord {
			if t.kind == tLBrace {
				p.skipBlockBody()
			}
			continue
		}
		switch t.val {
		case "technology":
			if s, ok := p.peek(); ok && s.kind == tStr {
				p.next()
				ct.Tech = s.val
			}
		case "tags":
			for {
				s, ok := p.peek()
				if !ok || s.kind != tStr {
					break
				}
				p.next()
				ct.Tags = append(ct.Tags, splitCommaList(s.val)...)
			}
		case "properties":
			p.parseContainerProperties(ct)
		case "group":
			// A group inside a container is a transparent visual wrapper: recurse
			// into it as if its contents sat directly in the container body.
			p.positionalStrings()
			if b, ok := p.peek(); ok && b.kind == tLBrace {
				p.next()
				p.parseContainerBody(ct)
			}
		default:
			// IDENT = component ... — record the child with its parent container.
			if nt, ok := p.peek(); ok && nt.kind == tEq {
				p.next() // consume '='
				kw, ok := p.next()
				if !ok || kw.kind != tWord {
					continue
				}
				if kw.val == "component" {
					comp := p.parseComponent(t.val)
					comp.Container = ct.ID
					p.m.Components = append(p.m.Components, comp)
					continue
				}
				// Other nested elements (rare, e.g. a mis-nested system): descend
				// transparently so inner declarations are still seen.
				p.positionalStrings()
				if b, ok := p.peek(); ok && b.kind == tLBrace {
					p.next()
					p.parseContainerBody(ct)
				}
				continue
			}
			// Relationship inside the container: IDENT -> IDENT
			if a, ok := p.peek(); ok && a.kind == tArrow {
				p.next()
				p.parseRelationship(t.val)
				continue
			}
			// Unknown statement; if it opens a block, skip it.
			if b, ok := p.peek(); ok && b.kind == tLBrace {
				p.next()
				p.skipBlockBody()
			}
		}
	}
}

// parseContainerProperties reads a container-level properties block: paths/path
// keys bind the container to files; EVERY other key merges into the model
// properties, last-wins. The merge is a regression guard, not a convenience:
// `nugit init` emits `"nugit_structural" "true"` inside the container block,
// and Structural() must keep seeing it now that containers are parsed instead
// of fallen through.
func (p *parser) parseContainerProperties(ct *model.Container) {
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
		if k.kind != tWord && k.kind != tStr {
			continue
		}
		v, ok := p.peek()
		if !ok || v.kind != tStr {
			continue
		}
		p.next()
		if k.val == "paths" || k.val == "path" {
			ct.Paths = append(ct.Paths, splitCommaList(v.val)...)
			continue
		}
		if p.m.Properties == nil {
			p.m.Properties = map[string]string{}
		}
		p.m.Properties[k.val] = v.val
	}
}

// positionalStrings consumes and returns the run of quoted strings at the
// cursor (an element's positional name/description/technology/tags arguments).
func (p *parser) positionalStrings() []string {
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

func (p *parser) parseComponentBlock(comp *model.Component) {
	p.next() // consume '{'
	for {
		t, ok := p.next()
		if !ok || t.kind == tRBrace {
			return
		}
		if t.kind != tWord {
			if t.kind == tLBrace {
				p.skipBlockBody()
			}
			continue
		}
		switch t.val {
		case "technology":
			if s, ok := p.peek(); ok && s.kind == tStr {
				p.next()
				comp.Tech = s.val
			}
		case "tags":
			for {
				s, ok := p.peek()
				if !ok || s.kind != tStr {
					break
				}
				p.next()
				comp.Tags = append(comp.Tags, splitCommaList(s.val)...)
			}
		case "properties":
			p.parseProperties(comp)
		default:
			// Relationship inside the block: IDENT -> IDENT
			if a, ok := p.peek(); ok && a.kind == tArrow {
				p.next()
				p.parseRelationship(t.val)
				continue
			}
			// Unknown statement; if it opens a block, skip it.
			if b, ok := p.peek(); ok && b.kind == tLBrace {
				p.next()
				p.skipBlockBody()
			}
		}
	}
}

// parseModelProperties reads a model/workspace-level properties block into the
// model's Properties map.
func (p *parser) parseModelProperties() {
	p.next() // consume '{'
	for {
		k, ok := p.next()
		if !ok || k.kind == tRBrace {
			return
		}
		if k.kind != tWord && k.kind != tStr {
			continue
		}
		v, ok := p.peek()
		if !ok || v.kind != tStr {
			continue
		}
		p.next()
		if p.m.Properties == nil {
			p.m.Properties = map[string]string{}
		}
		p.m.Properties[k.val] = v.val
	}
}

func (p *parser) parseProperties(comp *model.Component) {
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
		// Structurizr requires quoted property keys (`"paths" "..."`); nugit also
		// accepts the bare key it historically emitted (`paths "..."`).
		if k.kind != tWord && k.kind != tStr {
			continue
		}
		v, ok := p.peek()
		if !ok || v.kind != tStr {
			continue
		}
		p.next()
		if k.val == "paths" || k.val == "path" {
			comp.Paths = append(comp.Paths, splitCommaList(v.val)...)
		}
	}
}

func (p *parser) parseRelationship(src string) {
	dst, ok := p.next()
	if !ok || (dst.kind != tWord && dst.kind != tStr) {
		return // destination must be an identifier or an inline string target
	}
	rel := model.Relationship{Src: src, Dst: dst.val}
	if d, ok := p.peek(); ok && d.kind == tStr {
		p.next()
		rel.Desc = d.val
	}
	// consume any further positional strings (technology/tags on the rel)
	for {
		s, ok := p.peek()
		if !ok || s.kind != tStr {
			break
		}
		p.next()
	}
	p.m.Relationships = append(p.m.Relationships, rel)
}

// consumeOptionalBlock skips an optional `{ ... }` but still lets the parser see
// nested element declarations inside it (so components inside a softwareSystem
// are recorded). It does this by NOT skipping — it just consumes the opening
// brace and returns, letting the main loop continue into the block body.
func (p *parser) consumeOptionalBlock() {
	if t, ok := p.peek(); ok && t.kind == tLBrace {
		p.next() // descend into the block; main loop handles its contents
	}
}

// skipBlockBody consumes tokens until the brace that was already opened closes.
func (p *parser) skipBlockBody() {
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

func splitCommaList(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}
