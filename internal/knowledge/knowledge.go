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
	"strings"

	"github.com/burrowfarm/nugit/internal/model"
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

// ParseEdge parses a relates_to entry like "constrains:render" into its parts.
// Entries with no relation prefix are returned with an empty Relation.
func ParseEdge(s string) model.Edge {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return model.Edge{Relation: s[:i], Target: s[i+1:], Raw: s}
	}
	return model.Edge{Target: s, Raw: s}
}

// RejectedSection extracts the "Rejected" rationale from a decision body — the
// anti-hallucination field. It matches a markdown heading or bold lead-in.
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
		var sb strings.Builder
		for _, l := range lines[i+1:] {
			if strings.HasPrefix(strings.TrimSpace(l), "#") {
				break
			}
			sb.WriteString(l)
			sb.WriteString("\n")
		}
		out := strings.TrimSpace(sb.String())
		if out == "" {
			// bold lead-in with inline text on the same line
			if isBold {
				return strings.TrimSpace(t[len("**rejected"):])
			}
		}
		return out
	}
	return ""
}
