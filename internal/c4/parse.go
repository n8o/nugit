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

	"github.com/burrowfarm/nugit/internal/model"
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

var elementKeywords = map[string]bool{
	"component": true, "container": true,
}

func (p *parser) parse() model.Model {
	for {
		t, ok := p.next()
		if !ok {
			break
		}
		switch t.kind {
		case tWord:
			// Two interesting shapes start with a word:
			//   IDENT = component "Name" ...      (element declaration)
			//   IDENT -> IDENT ["desc"]           (relationship)
			if nt, ok := p.peek(); ok && nt.kind == tEq {
				p.parseAssignment(t.val)
			} else if nt, ok := p.peek(); ok && nt.kind == tArrow {
				p.next() // consume arrow
				p.parseRelationship(t.val)
			} else if t.val == "workspace" || t.val == "model" || t.val == "views" {
				// Containers we descend into implicitly by just continuing.
			}
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
	if !elementKeywords[kw.val] {
		// e.g. softwareSystem / person — descend but don't record as component.
		// Still consume an optional block so its inner components are seen.
		p.consumeOptionalBlock()
		return
	}
	// collect positional strings: name [description] [technology]
	var strs []string
	for {
		t, ok := p.peek()
		if !ok || t.kind != tStr {
			break
		}
		p.next()
		strs = append(strs, t.val)
	}
	comp := model.Component{ID: id}
	if len(strs) > 0 {
		comp.Name = strs[0]
	}
	if len(strs) > 2 {
		comp.Tech = strs[2]
	}
	// optional block with technology/tags/properties
	if t, ok := p.peek(); ok && t.kind == tLBrace {
		p.parseComponentBlock(&comp)
	}
	p.m.Components = append(p.m.Components, comp)
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
		if k.kind != tWord {
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
	if !ok || dst.kind != tWord {
		return
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
