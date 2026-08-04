// Package knowledge reads the in-tree durable knowledge objects under .nugit/**.
//
// Effective status is DERIVED from the supersedes graph at load time, never read
// back from a mutated file — this is how nugit keeps records immutable and
// content-addressable while still supporting "supersede, don't edit"
// (.nugit/decisions/0003-supersede-without-mutation.md).
package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/model"
	"gopkg.in/yaml.v3"
)

// Load walks repoDir/.nugit and returns every parsed knowledge object, with
// EffectiveStatus already resolved across the set.
func Load(repoDir string) ([]model.KnowledgeObject, error) {
	root := filepath.Join(repoDir, ".nugit")
	var objs []model.KnowledgeObject
	err := filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if de.IsDir() {
			if de.Name() == ".cache" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(de.Name(), ".md") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoDir, path)
		obj, ok := ParseObject(rel, string(b))
		if ok {
			objs = append(objs, *obj)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	ResolveEffectiveStatus(objs)
	ResolveAmendedBy(objs)
	return objs, nil
}

// LoadAtRef reads every knowledge object under the nugit root's .nugit/ tree at
// ref — never from the working tree — with EffectiveStatus resolved across the
// set, mirroring Load. A PR-time analyzer must read every artifact from the
// reviewed ref (LESSON-read-from-reviewed-ref); this is the ref-addressed
// counterpart of Load for the engine/pr-render path. prefix is the nugit root
// within the git repo, slash-terminated ("" when it IS the git root); returned
// Paths are nugit-root-relative, byte-identical to Load's.
func LoadAtRef(repo gitutil.Repo, ref, prefix string) ([]model.KnowledgeObject, error) {
	paths, err := repo.ListTree(ref)
	if err != nil {
		return nil, err
	}
	root := prefix + ".nugit/"
	var objs []model.KnowledgeObject
	for _, p := range paths {
		if !strings.HasPrefix(p, root) || !strings.HasSuffix(p, ".md") {
			continue
		}
		rel := p[len(prefix):]
		if strings.Contains(rel, "/.cache/") {
			continue // derived caches are not knowledge (mirrors Load's SkipDir)
		}
		src, err := repo.ShowFile(ref, p)
		if err != nil {
			return nil, err
		}
		if obj, ok := ParseObject(rel, src); ok {
			objs = append(objs, *obj)
		}
	}
	ResolveEffectiveStatus(objs)
	ResolveAmendedBy(objs)
	return objs, nil
}

// ParseObject splits a markdown file into front-matter + body. It returns
// ok=false for files with no `---` front-matter block (e.g. a bare glossary.md),
// which callers treat as "not a typed object".
func ParseObject(relPath, content string) (*model.KnowledgeObject, bool) {
	fm, body, ok := splitFrontMatter(content)
	if !ok {
		return nil, false
	}
	var front model.FrontMatter
	if err := yaml.Unmarshal([]byte(fm), &front); err != nil {
		// Malformed front-matter: surface as an untyped object so the renderer
		// can still show the file changed, but don't crash the render.
		return &model.KnowledgeObject{Path: relPath, Body: body}, true
	}
	obj := &model.KnowledgeObject{FrontMatter: front, Path: relPath, Body: body}
	obj.EffectiveStatus = front.Status
	obj.Rejected = RejectedSection(body)
	return obj, true
}

func splitFrontMatter(content string) (front, body string, ok bool) {
	s := strings.TrimPrefix(content, "\ufeff") // strip UTF-8 BOM
	if !strings.HasPrefix(s, "---") {
		return "", content, false
	}
	rest := s[len("---"):]
	rest = strings.TrimPrefix(rest, "\r")
	rest = strings.TrimPrefix(rest, "\n")
	// find the closing fence at the start of a line
	end := indexClosingFence(rest)
	if end < 0 {
		return "", content, false
	}
	front = rest[:end]
	body = rest[end:]
	// drop the closing fence line from the body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		body = body[i+1:]
	} else {
		body = ""
	}
	return front, strings.TrimLeft(body, "\n"), true
}

func indexClosingFence(s string) int {
	// look for a line that is exactly "---"
	offset := 0
	for {
		line := s[offset:]
		nl := strings.IndexByte(line, '\n')
		var cur string
		if nl < 0 {
			cur = line
		} else {
			cur = line[:nl]
		}
		if strings.TrimRight(cur, "\r") == "---" {
			return offset
		}
		if nl < 0 {
			return -1
		}
		offset += nl + 1
		if offset >= len(s) {
			return -1
		}
	}
}

// ResolveEffectiveStatus marks any object that another object supersedes as
// Superseded, in place on the slice.
func ResolveEffectiveStatus(objs []model.KnowledgeObject) {
	superseded := map[string]bool{}
	for _, o := range objs {
		if o.Supersedes != "" {
			superseded[o.Supersedes] = true
		}
	}
	for i := range objs {
		if objs[i].EffectiveStatus == "" {
			objs[i].EffectiveStatus = objs[i].Status
		}
		if superseded[objs[i].ID] && objs[i].EffectiveStatus != model.StatusInvalidated {
			objs[i].EffectiveStatus = model.StatusSuperseded
		}
	}
}

// ResolveAmendedBy computes each object's AmendedBy from reverse `amends:`
// edges, in place on the slice (ADR-0015). Unlike supersession this never
// changes the target's status — an amended record stays live, annotated so it
// is read together with what overrides part of it. Superseded/invalidated
// amenders don't annotate (a dead amendment amends nothing).
func ResolveAmendedBy(objs []model.KnowledgeObject) {
	amenders := map[string][]string{}
	for _, o := range objs {
		if o.ID == "" || o.EffectiveStatus == model.StatusSuperseded || o.EffectiveStatus == model.StatusInvalidated {
			continue
		}
		for _, e := range o.RelatesTo {
			if edge := ParseEdge(e); edge.Relation == "amends" && edge.Target != "" {
				amenders[edge.Target] = append(amenders[edge.Target], o.ID)
			}
		}
	}
	for i := range objs {
		if ids := amenders[objs[i].ID]; len(ids) > 0 {
			sort.Strings(ids)
			objs[i].AmendedBy = ids
		}
	}
}

// ParseEdge parses a relates_to entry like "constrains:render" into its parts.
// Entries with no relation prefix are returned with an empty Relation.
func ParseEdge(s string) model.Edge {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return model.Edge{Relation: s[:i], Target: s[i+1:], Raw: s}
	}
	return model.Edge{Target: s, Raw: s}
}

// RejectedSection extracts the "Rejected" rationale from a decision/lesson body —
// the anti-hallucination field. It supports two forms:
//
//	## Rejected            (a heading; body runs until the next heading)
//	**Rejected:** text…    (a bold lead-in; runs until a blank line / next lead-in)
func RejectedSection(body string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		low := strings.ToLower(t)
		isHeading := strings.HasPrefix(low, "#") && strings.Contains(low, "rejected")
		isBold := strings.HasPrefix(low, "**rejected")
		if !isHeading && !isBold {
			continue
		}
		if isBold {
			var parts []string
			if inline := boldInline(t); inline != "" {
				parts = append(parts, inline)
			}
			for _, l := range lines[i+1:] {
				lt := strings.TrimSpace(l)
				if lt == "" || strings.HasPrefix(lt, "#") || strings.HasPrefix(lt, "**") {
					break // blank line, next heading, or next bold lead-in ends it
				}
				parts = append(parts, lt)
			}
			return strings.TrimSpace(strings.Join(parts, " "))
		}
		// heading form: collect until the next heading
		var sb strings.Builder
		for _, l := range lines[i+1:] {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				break
			}
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		return strings.TrimSpace(sb.String())
	}
	return ""
}

// boldInline returns the text after a "**Rejected:**" / "**Rejected**" lead-in.
func boldInline(t string) string {
	if !strings.HasPrefix(t, "**") {
		return ""
	}
	if close := strings.Index(t[2:], "**"); close >= 0 {
		rest := strings.TrimSpace(t[2+close+2:])
		return strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	}
	return ""
}
