// Package distill promotes the "why" captured in commit trailers into durable,
// reviewed knowledge objects (MADR decisions + lessons), written into the PR so
// the store fills itself and capture survives squash-merge (ADR-0005). Stable
// human keys, never content hashes (ADR-0001). Deterministic, no LLM.
package distill

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/trailers"
)

// Options configure a distill run.
type Options struct {
	RepoDir  string
	Base     string
	Head     string
	MinRecur int    // a `learned:` must recur ≥ this to promote (default 2)
	Now      string // ISO timestamp for created: (testable); "" -> time.Now()
}

// Result reports what was promoted.
type Result struct {
	Decisions []string // created decision paths
	Lessons   []string // created lesson paths
	Skipped   int      // candidates already present in the store
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// Distill reads trailers over (base, head] and writes promotable decisions and
// recurring lessons into .nugit/. Idempotent: a decision/lesson already in the
// store (by normalized text) is skipped.
func Distill(opt Options) (Result, error) {
	if opt.MinRecur <= 0 {
		opt.MinRecur = 2
	}
	now := opt.Now
	if now == "" {
		now = time.Now().UTC().Format("2006-01-02T15:04:05Z")
	}
	repo := gitutil.Repo{Dir: opt.RepoDir}
	commits, err := repo.Log(opt.Base, opt.Head)
	if err != nil {
		return Result{}, err
	}
	for i := range commits {
		commits[i].Trailer = trailers.Parse(commits[i].Body)
	}

	existing, _ := knowledge.Load(opt.RepoDir)
	// Dedup by substring: a promoted ADR/lesson body contains the verbatim
	// decision/learned text, so an already-promoted candidate is one whose text
	// appears in some existing object's (normalized) body.
	existingBodies := make([]string, 0, len(existing))
	maxADR := 0
	for _, o := range existing {
		existingBodies = append(existingBodies, norm(o.Body))
		if n := adrNum(o.ID); n > maxADR {
			maxADR = n
		}
	}
	already := func(text string) bool {
		n := norm(text)
		if n == "" {
			return true
		}
		for _, eb := range existingBodies {
			if strings.Contains(eb, n) {
				return true
			}
		}
		return false
	}

	var res Result
	learnedCount := map[string]int{}
	for _, c := range commits {
		if l := strings.TrimSpace(c.Trailer.Learned); l != "" {
			learnedCount[l]++
		}
	}

	// Promote decisions (a `decision:` trailer is a deliberate, significant act).
	seen := map[string]bool{}
	for _, c := range commits {
		d := strings.TrimSpace(c.Trailer.Decision)
		if d == "" || seen[norm(d)] {
			continue
		}
		seen[norm(d)] = true
		if already(d) {
			res.Skipped++
			continue
		}
		maxADR++
		key := fmt.Sprintf("ADR-%04d", maxADR)
		path := filepath.Join(".nugit", "decisions", fmt.Sprintf("%04d-%s.md", maxADR, slug(d)))
		if err := writeObj(opt.RepoDir, path, adrBody(key, c, now)); err != nil {
			return res, err
		}
		res.Decisions = append(res.Decisions, path)
	}

	// Promote recurring (or decision-accompanied) lessons.
	seenL := map[string]bool{}
	for _, c := range commits {
		l := strings.TrimSpace(c.Trailer.Learned)
		if l == "" || seenL[norm(l)] {
			continue
		}
		significant := strings.TrimSpace(c.Trailer.Decision) != "" || strings.TrimSpace(c.Trailer.Rejected) != ""
		if learnedCount[l] < opt.MinRecur && !significant {
			continue
		}
		seenL[norm(l)] = true
		if already(l) {
			res.Skipped++
			continue
		}
		path := filepath.Join(".nugit", "lessons", slug(l)+".md")
		if err := writeObj(opt.RepoDir, path, lessonBody(c, now)); err != nil {
			return res, err
		}
		res.Lessons = append(res.Lessons, path)
	}
	return res, nil
}

func writeObj(repoDir, rel, content string) error {
	p := filepath.Join(repoDir, rel)
	if _, err := os.Stat(p); err == nil {
		return nil // never clobber
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func adrBody(key string, c model.Commit, now string) string {
	t := c.Trailer
	scope := "global"
	if len(t.Affects) > 0 {
		scope = t.Affects[0]
	}
	var rel []string
	for _, a := range t.Affects {
		rel = append(rel, "constrains:"+a)
	}
	if t.Spec != "" {
		rel = append(rel, "satisfies:"+t.Spec)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: decision\nscope: %s\nstatus: accepted\ncreated: %s\n", key, scope, now)
	if len(rel) > 0 {
		b.WriteString("relates_to:\n")
		for _, r := range rel {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	fmt.Fprintf(&b, "provenance:\n  commit: %s\nconfidence: medium\n---\n\n", short(c.SHA))
	fmt.Fprintf(&b, "# %s — %s\n\n", key, title(t.Decision))
	fmt.Fprintf(&b, "## Context\n\n%s\n\n", firstLine(c.Subject))
	fmt.Fprintf(&b, "## Decision\n\n%s\n\n", t.Decision)
	if t.Rejected != "" {
		fmt.Fprintf(&b, "## Rejected\n\n%s\n\n", t.Rejected)
	}
	fmt.Fprintf(&b, "## Consequences\n\nPromoted from commit `%s` by `nugit distill`.\n", short(c.SHA))
	return b.String()
}

func lessonBody(c model.Commit, now string) string {
	t := c.Trailer
	scope := "global"
	if len(t.Affects) > 0 {
		scope = t.Affects[0]
	}
	var b strings.Builder
	id := "LESSON-" + slug(t.Learned)
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: lesson\nscope: %s\nstatus: active\ncreated: %s\nprovenance:\n  commit: %s\nconfidence: medium\n---\n\n",
		id, scope, now, short(c.SHA))
	fmt.Fprintf(&b, "# Lesson — %s\n\n", title(t.Learned))
	fmt.Fprintf(&b, "**Trigger:** %s\n\n", firstLine(c.Subject))
	fmt.Fprintf(&b, "**Insight:** %s\n\n", t.Learned)
	if t.Rejected != "" {
		fmt.Fprintf(&b, "**Rejected:** %s\n\n", t.Rejected)
	}
	if len(t.Keywords) > 0 {
		fmt.Fprintf(&b, "**Keywords:** %s\n", strings.Join(t.Keywords, ", "))
	}
	return b.String()
}

// --- helpers ---

func norm(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), " ")) }

func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 50 {
		s = s[:50]
		s = strings.Trim(s, "-")
	}
	if s == "" {
		s = "note"
	}
	return s
}

func title(s string) string {
	s = firstLine(s)
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

var adrNumRE = regexp.MustCompile(`(?i)^ADR-(\d+)$`)

func adrNum(id string) int {
	m := adrNumRE.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	n := 0
	for _, r := range m[1] {
		n = n*10 + int(r-'0')
	}
	return n
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
