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
	"regexp"
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

// RawFrontMatter parses the front-matter block generically (no schema): the
// top-level YAML mapping, or ok=false when no parsable block exists. Doctor
// uses it to diagnose files the typed schema rejects (e.g. `supersedes:`
// authored as a list) and to inspect keys the schema silently drops.
func RawFrontMatter(content string) (map[string]any, bool) {
	fm, _, ok := splitFrontMatter(content)
	if !ok {
		return nil, false
	}
	var raw map[string]any
	if yaml.Unmarshal([]byte(fm), &raw) != nil || raw == nil {
		return nil, false
	}
	return raw, true
}

// ProseSupersession is a supersession an object declares only in body prose —
// no front-matter edge, so the target's EffectiveStatus never derives to
// superseded and retrieval keeps serving BOTH texts as live (ADR-0022; found
// on the pilot, where a stale protocol-draft reference kept surfacing next to
// the decision that replaced it).
type ProseSupersession struct {
	ObjectID   string // the object whose prose declares the supersession
	ObjectPath string
	Target     string // the store object the prose names
	TargetPath string
}

// proseSupersedeRe matches a prose supersession declaration: the word
// "supersedes" (optionally with a colon) followed by a dashed identifier
// (ADR-0003, REF-transport-14, …). The id grammar is permissive on purpose —
// resolution against the store is the precision filter.
var proseSupersedeRe = regexp.MustCompile(`(?i)\bsupersedes:?[ \t]+([A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)+)`)

// ProseSupersessionTargets returns the candidate ids a body declares it
// supersedes in prose, in order of first appearance. Fenced code blocks and
// inline code spans never match — documentation *quoting* `supersedes: <id>`
// is not a declaration.
func ProseSupersessionTargets(body string) []string {
	var ids []string
	seen := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		for _, m := range proseSupersedeRe.FindAllStringSubmatch(stripCodeSpans(line), -1) {
			if id := m[1]; !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids
}

// stripCodeSpans removes `inline code` spans from a line. An unbalanced
// backtick drops the tail: better to under-match prose than to read quoted
// schema examples as declarations.
func stripCodeSpans(s string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '`')
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.IndexByte(s[i+1:], '`')
		if j < 0 {
			return b.String()
		}
		s = s[i+1+j+1:]
	}
}

// ProseOnlySupersessions finds live typed objects whose prose declares a
// supersession of another live store object without the front-matter
// `supersedes:`/`amends:` edge that would derive the target's status
// (ADR-0022). Targets already superseded or invalidated never report — the
// edge exists elsewhere, so the drift is resolved. Callers must pass a slice
// that ResolveEffectiveStatus has run over (Load does).
func ProseOnlySupersessions(objs []model.KnowledgeObject) []ProseSupersession {
	byID := map[string]*model.KnowledgeObject{}
	for i := range objs {
		if objs[i].ID != "" {
			byID[objs[i].ID] = &objs[i]
		}
	}
	var out []ProseSupersession
	for i := range objs {
		o := &objs[i]
		if o.ID == "" || o.Type == "" || deadStatus(o.EffectiveStatus) {
			continue
		}
		for _, id := range ProseSupersessionTargets(o.Body) {
			target, ok := byID[id]
			if !ok || id == o.ID || deadStatus(target.EffectiveStatus) {
				continue
			}
			if o.Supersedes == id || hasAmendsEdge(o, id) {
				continue
			}
			out = append(out, ProseSupersession{
				ObjectID: o.ID, ObjectPath: o.Path,
				Target: id, TargetPath: target.Path,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObjectID != out[j].ObjectID {
			return out[i].ObjectID < out[j].ObjectID
		}
		return out[i].Target < out[j].Target
	})
	return out
}

func deadStatus(s model.Status) bool {
	return s == model.StatusSuperseded || s == model.StatusInvalidated
}

func hasAmendsEdge(o *model.KnowledgeObject, target string) bool {
	for _, e := range o.RelatesTo {
		if edge := ParseEdge(e); edge.Relation == "amends" && edge.Target == target {
			return true
		}
	}
	return false
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
