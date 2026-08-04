// Package distill promotes the "why" captured in commit trailers into durable,
// reviewed knowledge objects (MADR decisions + lessons), written into the PR so
// the store fills itself and capture survives squash-merge (ADR-0005). Stable
// human keys, never content hashes (ADR-0001). Deterministic, no LLM.
//
// A lesson's Trigger is seeded from an observable symptom — the `symptom:`
// trailer, else a symptom-shaped observation scavenged from the commit body,
// else an explicit author-facing TODO. Never the commit subject (ADR-0028);
// see symptom.go.
//
// A `decision:` trailer that CITES a record rather than stating one promotes
// nothing: a bare key ("decision: ADR-0026") has no statement to record, and a
// key the store already carries is already recorded. Only the statement is ever
// minted, with any leading key stripped — see splitKey/citation.
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
	RepoDir string
	Base    string
	Head    string
	// MinRecur is the recurrence a lone `learned:` needs to promote. Default 1
	// (ADR-0018): within a single PR range one excellent trailer is worth
	// proposing — the candidate lane, not recurrence, is the quality gate.
	MinRecur int
	Now      string // ISO timestamp for created: (testable); "" -> time.Now()
	// Status controls the minted status: "proposed" (default — the candidate
	// lane of ADR-0016: machine-drafted records await `nugit ratify`) or
	// "ratified" (pre-lane behavior: decisions land accepted, lessons active).
	Status string
}

// Result reports what was promoted.
type Result struct {
	Decisions []string // created decision paths
	Lessons   []string // created lesson paths
	Skipped   int      // candidates already present in the store
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

// candidate is one promotable trailer selected from the range, with any
// existing-store match marked (dup) rather than dropped — so the PR-time
// proposal set (ADR-0018) and the distill write path agree on what is fresh.
type candidate struct {
	kind model.Kind
	c    model.Commit
	text string
	dup  bool
}

// index is the dedup view of the existing store, built from the EXACT field a
// distilled object carries (its Decision section / Insight line), not a
// whole-body substring scan — that wrongly skipped any candidate whose text
// appeared anywhere in a body. Lessons additionally record their keyword sets
// for the ADR-0018 overlap rule, and every object's stable key is recorded so
// a `decision:` trailer that merely CITES one can be told from one that states
// a new decision (see splitKey).
type index struct {
	decisionText map[string]bool
	lessonText   map[string]bool
	lessonKw     []map[string]bool
	keys         map[string]bool // upper-cased ids of every loaded object
	maxADR       int
}

func indexStore(objs []model.KnowledgeObject) index {
	ix := index{decisionText: map[string]bool{}, lessonText: map[string]bool{}, keys: map[string]bool{}}
	for _, o := range objs {
		if id := strings.ToUpper(strings.TrimSpace(o.ID)); id != "" {
			ix.keys[id] = true
		}
		if n := adrNum(o.ID); n > ix.maxADR {
			ix.maxADR = n
		}
		switch o.Type {
		case model.KindDecision:
			if t := sectionText(o.Body, "## Decision"); t != "" {
				ix.decisionText[norm(t)] = true
			}
		case model.KindLesson:
			if t := insightText(o.Body); t != "" {
				ix.lessonText[norm(t)] = true
			}
			if kws := keywordsText(o.Body); len(kws) > 0 {
				set := map[string]bool{}
				for _, k := range kws {
					set[norm(k)] = true
				}
				ix.lessonKw = append(ix.lessonKw, set)
			}
		}
	}
	return ix
}

// dupLesson reports whether the store already carries this lesson: an exact
// normalized-insight match, or the ADR-0018 keyword-overlap rule — a single
// existing lesson shares ≥2 keywords covering ≥ half the candidate's set.
func (ix index) dupLesson(normText string, kws []string) bool {
	if ix.lessonText[normText] {
		return true
	}
	for _, set := range ix.lessonKw {
		if similarKeywordSet(kws, set) {
			return true
		}
	}
	return false
}

// similarKeywordSet is the ADR-0018 overlap rule against an already-normalized
// set. SimilarKeywords (overlap.go) is the exported slice-taking form, and both
// go through this one predicate so a caller outside this package can never end
// up with a different notion of "the same record" (ADR-0035).
func similarKeywordSet(candidate []string, set map[string]bool) bool {
	if len(candidate) < 2 {
		return false
	}
	shared := 0
	for _, k := range candidate {
		if set[norm(k)] {
			shared++
		}
	}
	return shared >= 2 && shared*2 >= len(candidate)
}

// keyPrefixRE matches a leading knowledge-object key ("ADR-0026", "SPEC-014")
// and, optionally, an EXPLICIT separator introducing a statement. A bare space
// is deliberately NOT a separator: "HTTP-2 over HTTP-1" states a decision, it
// does not cite ADR-shaped key HTTP-2.
var keyPrefixRE = regexp.MustCompile(`^([A-Za-z][A-Za-z0-9]*-\d+)\s*(?:$|(?:—|–|--|-|:|;)\s*)`)

// splitKey splits a `decision:` trailer value into the knowledge-object key it
// leads with (upper-cased, "" when absent) and the decision statement that
// follows. Both forms this repo's history uses are covered:
//
//	"ADR-0026"                       -> ("ADR-0026", "")           pure citation
//	"ADR-0024 — spend the budget…"   -> ("ADR-0024", "spend the…") cite + statement
//	"expose P"                       -> ("",         "expose P")   plain statement
func splitKey(s string) (key, stmt string) {
	s = strings.TrimSpace(s)
	m := keyPrefixRE.FindStringSubmatch(s)
	if m == nil {
		return "", s
	}
	return strings.ToUpper(m[1]), strings.TrimSpace(s[len(m[0]):])
}

// citation reports whether a `decision:` trailer merely REFERS to a decision
// rather than stating one, in which case it must mint nothing. Two cases:
//
//  1. No statement at all ("decision: ADR-0026"). There is nothing to record —
//     minting can only produce a record whose Decision body is a key, which is
//     the bug this rule exists to kill. True whether or not the key resolves:
//     an unresolved bare key is a dangling reference, not a decision.
//  2. A statement led by a key the store already carries ("ADR-0024 — …"). The
//     decision is already recorded under that key; the commit is citing it
//     while adding detail. Minting would fork the record under a second id.
//
// A statement led by an UNKNOWN key still mints — with the key stripped, so the
// title and Decision body are the statement (ADR-0001: keys are assigned by the
// store, never carried in a record's own prose).
func (ix index) citation(key, stmt string) bool {
	return stmt == "" || (key != "" && ix.keys[key])
}

// selectCandidates walks the range in commit order: every unique `decision:`
// trailer that STATES a decision (a deliberate, significant act — citations of
// existing records are skipped), then every unique `learned:` that recurs ≥
// minRecur times or rides a decision/rejected-bearing commit. In-range
// duplicates collapse to the first occurrence.
func selectCandidates(commits []model.Commit, ix index, minRecur int) []candidate {
	var out []candidate
	learnedCount := map[string]int{}
	for _, c := range commits {
		if l := strings.TrimSpace(c.Trailer.Learned); l != "" {
			learnedCount[norm(l)]++ // norm key: whitespace/case variants are one lesson
		}
	}
	seen := map[string]bool{}
	for _, c := range commits {
		key, d := splitKey(c.Trailer.Decision)
		if ix.citation(key, d) || seen[norm(d)] {
			continue
		}
		seen[norm(d)] = true
		out = append(out, candidate{kind: model.KindDecision, c: c, text: d, dup: ix.decisionText[norm(d)]})
	}
	seenL := map[string]bool{}
	for _, c := range commits {
		l := strings.TrimSpace(c.Trailer.Learned)
		if l == "" || seenL[norm(l)] {
			continue
		}
		significant := strings.TrimSpace(c.Trailer.Decision) != "" || strings.TrimSpace(c.Trailer.Rejected) != ""
		if learnedCount[norm(l)] < minRecur && !significant {
			continue
		}
		seenL[norm(l)] = true
		out = append(out, candidate{kind: model.KindLesson, c: c, text: l, dup: ix.dupLesson(norm(l), c.Trailer.Keywords)})
	}
	return out
}

// Propose computes — without touching the filesystem or git — the PR-time
// proposal set (ADR-0018): what Distill would write for this range of parsed
// commits against this store, plus how many candidates the store already
// covers. minRecur ≤ 0 defaults to 1, matching Distill.
func Propose(commits []model.Commit, existing []model.KnowledgeObject, minRecur int) (props []model.Proposal, deduped int) {
	if minRecur <= 0 {
		minRecur = 1
	}
	for _, cand := range selectCandidates(commits, indexStore(existing), minRecur) {
		if cand.dup {
			deduped++
			continue
		}
		t := cand.c.Trailer
		props = append(props, model.Proposal{
			Kind:     cand.kind,
			Title:    title(cand.text),
			Text:     cand.text,
			Rejected: strings.TrimSpace(t.Rejected),
			Scope:    scopeOf(t.Affects),
			Keywords: t.Keywords,
			Commit:   short(cand.c.SHA),
			Subject:  firstLine(cand.c.Subject),
		})
	}
	return props, deduped
}

// Distill reads trailers over (base, head] and writes promotable decisions and
// lessons into .nugit/. Idempotent: a decision/lesson the store already covers
// (exact normalized text, or keyword overlap for lessons) is skipped.
func Distill(opt Options) (Result, error) {
	if opt.MinRecur <= 0 {
		opt.MinRecur = 1
	}
	adrStatus, lessonStatus := string(model.StatusProposed), string(model.StatusProposed)
	switch opt.Status {
	case "", "proposed":
	case "ratified":
		adrStatus, lessonStatus = string(model.StatusAccepted), string(model.StatusActive)
	default:
		return Result{}, fmt.Errorf("distill: unknown status %q (want proposed or ratified)", opt.Status)
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
	ix := indexStore(existing)

	var res Result
	maxADR := ix.maxADR
	usedSlug := map[string]bool{}
	for _, cand := range selectCandidates(commits, ix, opt.MinRecur) {
		if cand.dup {
			res.Skipped++
			continue
		}
		switch cand.kind {
		case model.KindDecision:
			maxADR++
			key := fmt.Sprintf("ADR-%04d", maxADR)
			path := filepath.Join(".nugit", "decisions", fmt.Sprintf("%04d-%s.md", maxADR, slug(cand.text)))
			wrote, err := writeObj(opt.RepoDir, path, adrBody(key, cand.text, cand.c, now, adrStatus))
			if err != nil {
				return res, err
			}
			if wrote {
				res.Decisions = append(res.Decisions, path)
			} else {
				res.Skipped++
			}
		case model.KindLesson:
			// Disambiguate colliding slugs so two distinct lessons never
			// overwrite or share an id.
			s := uniqueSlug(slug(cand.text), usedSlug, opt.RepoDir)
			usedSlug[s] = true
			path := filepath.Join(".nugit", "lessons", s+".md")
			wrote, err := writeObj(opt.RepoDir, path, lessonBody(cand.c, now, s, lessonStatus))
			if err != nil {
				return res, err
			}
			if wrote {
				res.Lessons = append(res.Lessons, path)
			} else {
				res.Skipped++
			}
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

// adrBody renders one MADR record. `text` is the selected decision STATEMENT,
// not the raw trailer: any leading knowledge-object key was stripped upstream
// (splitKey), so the title and Decision body never restate an id.
func adrBody(key, text string, c model.Commit, now, status string) string {
	t := c.Trailer
	var rel []string
	for _, a := range t.Affects {
		rel = append(rel, "constrains:"+a)
	}
	if t.Spec != "" {
		rel = append(rel, "satisfies:"+t.Spec)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: decision\nscope: %s\nstatus: %s\ncreated: %s\n", key, scopeOf(t.Affects), status, now)
	if len(rel) > 0 {
		b.WriteString("relates_to:\n")
		for _, r := range rel {
			fmt.Fprintf(&b, "  - %s\n", r)
		}
	}
	fmt.Fprintf(&b, "provenance:\n  commit: %s\nconfidence: medium\n---\n\n", short(c.SHA))
	fmt.Fprintf(&b, "# %s — %s\n\n", key, title(text))
	fmt.Fprintf(&b, "## Context\n\n%s\n\n", firstLine(c.Subject))
	fmt.Fprintf(&b, "## Decision\n\n%s\n\n", text)
	if t.Rejected != "" {
		fmt.Fprintf(&b, "## Rejected\n\n%s\n\n", t.Rejected)
	}
	fmt.Fprintf(&b, "## Consequences\n\nPromoted from commit `%s` by `nugit distill`.\n", short(c.SHA))
	return b.String()
}

func lessonBody(c model.Commit, now, slug, status string) string {
	t := c.Trailer
	var b strings.Builder
	fmt.Fprintf(&b, "---\nschema_version: 1\nid: %s\ntype: lesson\nscope: %s\nstatus: %s\ncreated: %s\nprovenance:\n  commit: %s\nconfidence: medium\n---\n\n",
		"LESSON-"+slug, scopeOf(t.Affects), status, now, short(c.SHA))
	fmt.Fprintf(&b, "# Lesson — %s\n\n", title(t.Learned))
	// Trigger is the observable SYMPTOM, never the commit subject (ADR-0028):
	// the subject is a task description, which is what a future debugger will
	// never search for and what the store's exporter refuses outright.
	fmt.Fprintf(&b, "**Trigger:** %s\n\n", triggerFor(c))
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

// keywordsText returns the "**Keywords:** a, b" line of a distilled lesson as
// a list (empty when absent — hand-written lessons need not carry one).
func keywordsText(body string) []string {
	for _, ln := range strings.Split(body, "\n") {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "**Keywords:**") {
			var out []string
			for _, f := range strings.Split(strings.TrimPrefix(t, "**Keywords:**"), ",") {
				if f = strings.TrimSpace(f); f != "" {
					out = append(out, f)
				}
			}
			return out
		}
	}
	return nil
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
