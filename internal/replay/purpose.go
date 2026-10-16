package replay

// One bead, one change (ADR-0045). A task's prompt is ONE bead and its hidden
// tests grade ONE change, so a solution that also did something else measures
// whether the agent obeys the tests, not whether it did the bead. Two
// deterministic checks catch that shape; neither reads anything but git.

import (
	"path"
	"regexp"
	"strings"
	"unicode"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/gitutil"
)

// family reports whether two bead ids are one piece of work: equal, one an
// ancestor of the other (`gr-12` / `gr-12-1`), or one a lettered sub-step of
// the other (`gr-12-7` / `gr-12-7a`). `gr-1` and `gr-12` are NOT family.
func family(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	if len(a) > len(b) {
		a, b = b, a
	}
	if a == b {
		return true
	}
	if !strings.HasPrefix(b, a) {
		return false
	}
	rest := b[len(a):]
	if rest[0] == '-' {
		return true
	}
	for _, r := range rest {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

const idPat = `[A-Za-z0-9]+(?:-[A-Za-z0-9]+)+`

// closesBeadsRe matches a closing verb and the id list it governs:
// `closes gr-12-1`, `Closes xx-1, xx-2 and xx-3`, `implements: gr-4`.
var closesBeadsRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?|implement[sd]?)\b:?\s+(` +
	idPat + `(?:(?:\s*,\s*|\s+and\s+|\s*&\s*)` + idPat + `)*)`)

// closedBeads returns the known bead ids, outside id's family, that msg claims
// to close. A bead merely NAMED ("follow-up to x", "x still in progress") is a
// cross-reference, not a second piece of work, and does not count.
func closedBeads(msg, id string, known map[string]bool) []string {
	var out []string
	for _, m := range closesBeadsRe.FindAllStringSubmatch(msg, -1) {
		for _, tok := range tokens(m[1]) {
			if t := strings.ToLower(tok); known[t] && !family(t, id) {
				out = append(out, t)
			}
		}
	}
	return out
}

// closesRe matches a GitHub closing keyword and the issue list after it:
// `Closes #12`, `fixes: #3, #4 and #5`, `resolves owner/repo#9`. A bare `(#12)`
// — a squash subject's PR number — is a reference, not a closing claim.
var closesRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)\b:?\s+((?:[\w.-]+/[\w.-]+)?#\d+(?:(?:\s*,\s*|\s+and\s+|\s*&\s*)(?:[\w.-]+/[\w.-]+)?#\d+)*)`)

var issueNumRe = regexp.MustCompile(`#(\d+)`)

// closedIssues returns the issue numbers msg claims to close.
func closedIssues(msg string) []string {
	var out []string
	for _, m := range closesRe.FindAllStringSubmatch(msg, -1) {
		for _, n := range issueNumRe.FindAllStringSubmatch(m[1], -1) {
			out = append(out, n[1])
		}
	}
	return out
}

// refsIssue reports whether the bead's own text references issue n — as `#n`,
// `issues/n` or `gh-n`.
func refsIssue(text, n string) bool {
	return regexp.MustCompile(`(?i)(?:#|issues/|gh-)` + n + `\b`).MatchString(text)
}

// multiPurpose reports whether the solution's implementing commits close
// another bead (outside this one's family) or close an issue the bead's own
// text never references. A commit that touches only the plan/knowledge store
// implements nothing, so a `chore(beads): close a, b, c` sweep never marks a
// task. The other half of multi-bead — two beads mined to the SAME solution
// commit — is decided across tasks, in Export.
func (e *exporter) multiPurpose(it, src beads.Issue, msgs []*gitutil.HistoryCommit) bool {
	text := strings.Join([]string{it.Title, it.Field("description"), src.Title, src.Field("description"), it.Field("external_ref")}, "\n")
	for _, c := range msgs {
		if c == nil || e.storeOnly(c) {
			continue
		}
		if len(closedBeads(c.Message(), it.ID, e.known)) > 0 {
			return true
		}
		for _, n := range closedIssues(c.Message()) {
			if !refsIssue(text, n) {
				return true
			}
		}
	}
	return false
}

// storeOnly reports whether c changes nothing outside .beads/ and .nugit/.
func (e *exporter) storeOnly(c *gitutil.HistoryCommit) bool {
	if len(c.Parents) == 0 {
		return false
	}
	changes, err := e.repo.NameStatus(c.Parents[0], c.SHA)
	if err != nil || len(changes) == 0 {
		return false
	}
	for _, ch := range changes {
		if !isStore(ch.Path, e.prefix) {
			return false
		}
	}
	return true
}

// stopwords are words too common in bead prose or test code to say anything
// about which change a test belongs to. Deliberately generic and short: a word
// missing here can only make a test look MORE related, which keeps a task —
// the safe direction.
var stopwords = func() map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(`
about above after again against along already also always among another anything around because
been before being below between both bring build built cannot cause change changed changes check
checks could currently default does doing done during each either empty ensure error errors every
exist exists expect expected false field fields first found from function given github handle
handled happen having here instead into itself later least little longer looks makes making maybe
might missing more most multiple must needs never newer nothing other others output param params
passes passing please possible previous print properly provide quite rather really result return
returns right running second should since single something still string strings struct subject
support supports their them then there these thing things think third those though three through
today together tried under unless until update updated using value values variable version wants
where whether which while within without would wrong
assert asserts testing tests import const types typed implement implemented implementation works
working correct correctly
also back bead both case code data does done each epic file from have into just last like line
list main make more must name need next null only plan same some spec step task test than that
them then they this todo true type used uses very what when will with work`) {
		m[w] = true
	}
	return m
}()

// Minimum lengths for a plain prose word to be a bead term. A test's PATH is
// a deliberate choice of topic, so a shorter shared word there means
// something; its CONTENT and a diff are large and noisy, so they need longer.
const (
	pathWordMin    = 4
	contentWordMin = 5
)

var (
	backtickRe = regexp.MustCompile("`([^`\n]+)`")
	pathishRe  = regexp.MustCompile(`[\w.-]+(?:/[\w.-]+)+|[\w-]+\.[A-Za-z]{1,4}\b`)
	identRe    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

// splitIdent breaks `parseConfigV2` / `parse_config` / `HTTPServer` into its
// lowercase words.
func splitIdent(s string) []string {
	var out []string
	var cur []rune
	rs := []rune(s)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range rs {
		switch {
		case !unicode.IsLetter(r) && !unicode.IsDigit(r):
			flush()
		case unicode.IsUpper(r) && len(cur) > 0 &&
			(unicode.IsLower(cur[len(cur)-1]) || (i+1 < len(rs) && unicode.IsLower(rs[i+1]))):
			flush()
			cur = append(cur, r)
		default:
			cur = append(cur, r)
		}
	}
	flush()
	return out
}

// identShaped: snake_case, camelCase or carrying digits — a name, not prose.
func identShaped(s string) bool {
	if strings.ContainsAny(s, "_0123456789") {
		return true
	}
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// beadTerms is the distinctive vocabulary of a bead's title and description,
// all lowercase:
//
//   - every backticked span, whole (≥3 chars), and its identifier words (≥4);
//   - every path-like token's base name and stem, and its segments (≥3);
//   - every identifier-shaped word whole (≥3), and its words (≥minWord);
//   - every other word of ≥minWord letters.
//
// Stopwords are never terms.
func beadTerms(text string, minWord int) map[string]bool {
	terms := map[string]bool{}
	add := func(w string, min int) {
		w = strings.ToLower(strings.TrimSpace(w))
		if len(w) >= min && !stopwords[w] {
			terms[w] = true
		}
	}
	for _, m := range backtickRe.FindAllStringSubmatch(text, -1) {
		add(m[1], 3)
		for _, w := range splitIdent(m[1]) {
			add(w, 4)
		}
	}
	for _, p := range pathishRe.FindAllString(text, -1) {
		b := path.Base(p)
		add(b, 3)
		add(strings.TrimSuffix(b, path.Ext(b)), 3)
		for _, w := range splitIdent(p) {
			add(w, 3)
		}
	}
	for _, w := range identRe.FindAllString(text, -1) {
		if identShaped(w) {
			add(w, 3)
			for _, part := range splitIdent(w) {
				add(part, minWord)
			}
			continue
		}
		add(w, minWord)
	}
	return terms
}

// vocab is the set of lowercase words some text (a path, a file, a diff) is
// made of: every identifier whole and split, every path's base name and stem.
func vocab(texts ...string) map[string]bool {
	v := map[string]bool{}
	for _, t := range texts {
		for _, w := range identRe.FindAllString(t, -1) {
			v[strings.ToLower(w)] = true
			for _, part := range splitIdent(w) {
				v[part] = true
			}
		}
		for _, p := range pathishRe.FindAllString(t, -1) {
			b := strings.ToLower(path.Base(p))
			v[b] = true
			v[strings.TrimSuffix(b, path.Ext(b))] = true
		}
	}
	return v
}

// shared counts the distinct bead terms that occur in text — as a word of its
// vocabulary or, for a term with punctuation or spaces (a backticked span like
// `a.b(c)`), as a substring.
func shared(terms map[string]bool, texts ...string) int {
	v := vocab(texts...)
	lower := strings.ToLower(strings.Join(texts, "\n"))
	n := 0
	for t := range terms {
		if v[t] || (strings.ContainsAny(t, " .(/") && strings.Contains(lower, t)) {
			n++
		}
	}
	return n
}

// diffLinkMin is how many distinct bead terms the primary directory's added
// lines must share before a test is kept for sitting next to it. One shared
// word in a diff of any size is coincidence; two is a topic.
const diffLinkMin = 2

// testsRelated is the hidden-test relevance check over the bead's title and
// description. A hidden test belongs to the bead when ANY of these holds:
//
//  1. a bead term (prose words ≥ pathWordMin) occurs in the test's path, or a
//     bead term (prose words ≥ contentWordMin) in its content;
//  2. a bead term (≥ pathWordMin) occurs in the path of a non-test solution
//     file in the test's own directory — the code the test exercises;
//  3. the test sits in the directory of the task's primary path AND that
//     directory's added solution lines share ≥ diffLinkMin bead terms
//     (≥ contentWordMin).
//
// Rule 3 keeps a normal task whose bead is prose the test never repeats: the
// test is next to the changed code, and the changed code is about the bead. It
// deliberately does NOT keep a test merely for sitting next to the primary
// path — when one commit did two things, the larger one decides the primary
// path, and its test sits right next to it.
//
// True when at least one hidden test belongs, when there are none, and when
// the bead has no distinctive terms to judge by.
func testsRelated(beadText string, hidden []string, testSrc map[string]string,
	solutionFiles []string, primary, primaryAdded string) bool {
	pathTerms, contentTerms := beadTerms(beadText, pathWordMin), beadTerms(beadText, contentWordMin)
	if len(hidden) == 0 || len(pathTerms) == 0 {
		return true
	}
	byDir := map[string][]string{}
	for _, f := range solutionFiles {
		byDir[path.Dir(f)] = append(byDir[path.Dir(f)], f)
	}
	diffLinked := -1 // computed on first need
	for _, h := range hidden {
		if shared(pathTerms, h) > 0 || shared(contentTerms, testSrc[h]) > 0 {
			return true
		}
		dir := path.Dir(h)
		if shared(pathTerms, byDir[dir]...) > 0 {
			return true
		}
		if dir == path.Dir(primary) {
			if diffLinked < 0 {
				diffLinked = shared(contentTerms, primaryAdded)
			}
			if diffLinked >= diffLinkMin {
				return true
			}
		}
	}
	return false
}

// addedLines keeps only a unified diff's added lines (without the `+`).
func addedLines(diff string) string {
	var b strings.Builder
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++") {
			b.WriteString(l[1:])
			b.WriteByte('\n')
		}
	}
	return b.String()
}
