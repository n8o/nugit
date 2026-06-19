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
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
// recurring lessons into .nugit/. Idempotent: a decision/lesson whose exact
// normalized text already backs a stored object is skipped.
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

	// Build dedup sets from the EXACT field a distilled object carries (its
	// Decision section / Insight line), not a whole-body substring scan — that
	// wrongly skipped any candidate whose text appeared anywhere in a body.
	existing, _ := knowledge.Load(opt.RepoDir)
	haveDecision := map[string]bool{}
	haveLesson := map[string]bool{}
	maxADR := 0
	for _, o := range existing {
		if n := adrNum(o.ID); n > maxADR {
			maxADR = n
		}
		switch o.Type {
		case model.KindDecision:
			if t := sectionText(o.Body, "## Decision"); t != "" {
				haveDecision[norm(t)] = true
			}
		case model.KindLesson:
			if t := insightText(o.Body); t != "" {
				haveLesson[norm(t)] = true
			}
		}
	}

	var res Result
	learnedCount := map[string]int{}
	for _, c := range commits {
		if l := strings.TrimSpace(c.Trailer.Learned); l != "" {
			learnedCount[norm(l)]++ // norm key: whitespace/case variants are one lesson
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
		if haveDecision[norm(d)] {
			res.Skipped++
			continue
		}
		maxADR++
		key := fmt.Sprintf("ADR-%04d", maxADR)
		path := filepath.Join(".nugit", "decisions", fmt.Sprintf("%04d-%s.md", maxADR, slug(d)))
		wrote, err := writeObj(opt.RepoDir, path, adrBody(key, c, now))
		if err != nil {
			return res, err
		}
		if wrote {
			res.Decisions = append(res.Decisions, path)
		} else {
			res.Skipped++
		}
	}

	// Promote recurring (or decision-accompanied) lessons.
	seenL := map[string]bool{}
	usedSlug := map[string]bool{}
	for _, c := range commits {
		l := strings.TrimSpace(c.Trailer.Learned)
		if l == "" || seenL[norm(l)] {
			continue
		}
		significant := strings.TrimSpace(c.Trailer.Decision) != "" || strings.TrimSpace(c.Trailer.Rejected) != ""
		if learnedCount[norm(l)] < opt.MinRecur && !significant {
			continue
		}
		seenL[norm(l)] = true
		if haveLesson[norm(l)] {
			res.Skipped++
			continue
		}
		// Disambiguate colliding slugs so two distinct lessons never overwrite or
		// share an id.
		s := uniqueSlug(slug(l), usedSlug, opt.RepoDir)
		usedSlug[s] = true
		path := filepath.Join(".nugit", "lessons", s+".md")
		wrote, err := writeObj(opt.RepoDir, path, lessonBody(c, now, s))
		if err != nil {
			return res, err
		}
		if wrote {
			res.Lessons = append(res.Lessons, path)
		} else {
			res.Skipped++
		}
	}
	return res, nil
}

// writeObj writes content unless a file already exists; it returns whether it
// actually wrote (so a no-op is never reported as a promotion).
func writeObj(repoDir, rel, content string) (bool, error) {
	p := filepath.Join(repoDir, rel)
	if _, err := os.Stat(p); err == nil {
		return false, nil // never clobber
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

// uniqueSlug returns base, or base-2/base-3/... so neither the in-run set nor an
// on-disk lesson file collides.
func uniqueSlug(base string, used map[string]bool, repoDir string) string {
	s := base
	for i := 2; used[s] || fileExists(repoDir, filepath.Join(".nugit", "lessons", s+".md")); i++ {
		s = base + "-" + strconv.Itoa(i)
	}
	return s
}

func fileExists(repoDir, rel string) bool {
	_, err := os.Stat(filepath.Join(repoDir, rel))
	return err == nil
}

// scopeOf: a single-component decision scopes there; a cross-cutting one (>1
// affects, or none) is global.
func scopeOf(affects []string) string {
	if len(affects) == 1 {
		return affects[0]
	}
	return "global"
}

func adrBody(key string, c model.Commit, now string) string {
	t := c.Trailer
	var rel []string
	for _, a := range t.Affects {
		rel = append(rel, "constrains:"+a)
	}
	if t.Spec != "" {
		rel = append(rel, "satisfies:"+t.Spec)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: decision\nscope: %s\nstatus: accepted\ncreated: %s\n", key, scopeOf(t.Affects), now)
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

func lessonBody(c model.Commit, now, slug string) string {
	t := c.Trailer
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: lesson\nscope: %s\nstatus: active\ncreated: %s\nprovenance:\n  commit: %s\nconfidence: medium\n---\n\n",
		"LESSON-"+slug, scopeOf(t.Affects), now, short(c.SHA))
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
		s = strings.Trim(s[:50], "-")
	}
	if s == "" {
		s = "note"
	}
	return s
}

func title(s string) string { return truncRunes(firstLine(s), 80) }

// truncRunes truncates to n runes (never mid-rune) so output stays valid UTF-8.
func truncRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// sectionText returns the text under a "## Heading" up to the next heading.
func sectionText(body, heading string) string {
	var out []string
	in := false
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if in {
			if strings.HasPrefix(t, "#") {
				break
			}
			out = append(out, ln)
		}
		if t == heading {
			in = true
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// insightText returns the "**Insight:** ..." line's text (distilled lessons).
func insightText(body string) string {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "**Insight:**") {
			return strings.TrimSpace(strings.TrimPrefix(t, "**Insight:**"))
		}
	}
	return ""
}

var adrNumRE = regexp.MustCompile(`(?i)^ADR-(\d+)$`)

func adrNum(id string) int {
	m := adrNumRE.FindStringSubmatch(id)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
