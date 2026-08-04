package adopt

// Prose-inventory parsing and THE PRECISION RULE (ADR-0036).
//
// A repo's prose names hundreds of things: vendors, protocols, file names, verbs
// capitalized at the start of a bullet. Only a handful of them are claims about
// this repo's own units, and a false "documented but absent" discredits the whole
// report — a reader who finds one phantom that is really a typo stops reading.
//
// So a prose token becomes a CLAIMED UNIT only when the document itself gives
// structural or lexical evidence that it is naming this repo's units. Four
// admission rules, each recorded on the finding so a reader can audit which one
// fired; everything else in the prose is ignored.

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Admission rules, in the order they are tried. The rule that admitted a token
// is reported with every finding: a phantom nobody can audit is a rumour.
const (
	// RuleExact — the token IS a detected unit's name or directory basename.
	// Never yields a phantom; it is how coverage and disagreements are computed.
	RuleExact = "exact-match"
	// RuleColocated — the token sits in a table column, sibling list or sibling
	// heading group where at least two OTHER entries are detected units. The
	// document's own structure declares the group an inventory of this repo.
	RuleColocated = "inventory-colocation"
	// RulePathAnchor — the token is path-shaped and its parent directory really
	// exists and really contains a detected unit, but the full path does not.
	RulePathAnchor = "path-anchor"
	// RuleFamilyAffix — the token's first or last segment appears in the same
	// position in at least two detected unit names, using a separator at least
	// two detected units use ("x-service" in a repo of "*-service"s).
	RuleFamilyAffix = "family-affix"
)

const (
	// minColocated: how many entries in a group must be real units before the
	// group counts as an inventory. Two, not one: a single real name next to a
	// word is a coincidence, two is a list.
	minColocated = 2
	// colocatedShare: and they must be at least this fraction of the group.
	// Without it, ANY long bullet list containing two service names would make
	// every other bullet a unit claim — measured on a real 21-service monorepo
	// that admitted 395 phantoms, nearly all of them English words. A document's
	// structure only vouches for a group when the group is MOSTLY units.
	colocatedShare = 0.5
	// minAffixUnits: how many detected units must share a segment in the same
	// position before it is a family affix. Same reasoning.
	minAffixUnits = 2
	// maxDocBytes bounds a single document read — a generated changelog is not
	// an inventory and must not be able to blow up the report.
	maxDocBytes = 1 << 20
	// maxDocs bounds the corpus walked. Deterministic (docs are sorted) so the
	// truncation point does not move between runs.
	maxDocs = 400
)

// docText is one prose document, read once.
type docText struct {
	Path  string
	Lines []string
}

// mention is one place a claimed unit was named.
type mention struct {
	Doc  string `json:"doc"`
	Line int    `json:"line"` // 1-based
	Text string `json:"text"` // the row/heading/item it was named in, trimmed
	Rule string `json:"rule"` // which admission rule let it in
}

// claim is a claimed unit plus everything the prose asserted about it.
type claim struct {
	Name     string
	Mentions []mention
	Ports    map[string][]mention // doc -> port values asserted there
	Paths    map[string][]mention // doc -> path values asserted there
}

// inventoryRoots are the top-level files an agent or a newcomer actually reads.
// docs/**/*.md is walked in addition to these.
var inventoryRoots = []string{"CLAUDE.md", "AGENTS.md", "README.md"}

// pruneDirs mirrors the detectors' prune set — a vendored README is not this
// repo's claim about itself.
var pruneDirs = map[string]bool{
	".git": true, ".nugit": true, ".nugit-local": true, ".worktrees": true,
	"node_modules": true, "vendor": true, "third_party": true, "testdata": true,
	"build": true, "dist": true, "target": true, "out": true, "site": true,
}

// discoverDocs returns the prose inventories to read: the root agent/readme
// files that exist, plus every markdown file under docs/. Sorted and capped, so
// two runs over one tree read the same set in the same order.
func discoverDocs(repoDir string) []string {
	var out []string
	for _, r := range inventoryRoots {
		if st, err := os.Stat(filepath.Join(repoDir, r)); err == nil && !st.IsDir() {
			out = append(out, r)
		}
	}
	var walked []string
	docsDir := filepath.Join(repoDir, "docs")
	_ = filepath.WalkDir(docsDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != docsDir && (pruneDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}
		rel, rerr := filepath.Rel(repoDir, p)
		if rerr != nil {
			return nil
		}
		walked = append(walked, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(walked)
	out = append(out, walked...)
	if len(out) > maxDocs {
		out = out[:maxDocs]
	}
	return out
}

// readDoc reads one document, bounded.
func readDoc(repoDir, rel string) (docText, bool) {
	b, err := os.ReadFile(filepath.Join(repoDir, rel))
	if err != nil {
		return docText{}, false
	}
	if len(b) > maxDocBytes {
		b = b[:maxDocBytes]
	}
	return docText{Path: rel, Lines: strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")}, true
}

// --- the shape of this repo's names -----------------------------------------

// shape is everything the DETECTED units tell us about how this repo names
// things. It is the only vocabulary the precision rule is allowed to use: the
// prose is the thing under suspicion, so it never gets to define what a unit
// looks like.
type shape struct {
	names     map[string]bool // normalized unit names + dir basenames
	unitDirs  map[string]bool // normalized detected unit directories
	parentDir map[string]bool // directories that CONTAIN a detected unit
	head      map[string]int  // first segment -> how many units use it there
	tail      map[string]int  // last segment  -> how many units use it there
	seps      map[string]int  // separator -> how many units use it
}

var sepChars = []string{"-", "_", "."}

func newShape(units []unitRef) shape {
	s := shape{
		names: map[string]bool{}, unitDirs: map[string]bool{}, parentDir: map[string]bool{},
		head: map[string]int{}, tail: map[string]int{}, seps: map[string]int{},
	}
	for _, u := range units {
		n := normalize(u.Name)
		base := normalize(path.Base(u.Dir))
		s.names[n] = true
		s.names[base] = true
		s.unitDirs[strings.ToLower(u.Dir)] = true
		if d := path.Dir(u.Dir); d != "." && d != "/" && d != "" {
			s.parentDir[strings.ToLower(d)] = true
		}
		// Count each affix ONCE PER UNIT. A unit contributes two spellings (its
		// detector name and its directory basename) and they are usually equal;
		// counting both would let ONE unit mint a "family", which is precisely
		// what the two-unit threshold exists to prevent.
		counted := map[string]bool{}
		bump := func(m map[string]int, key, val string) {
			if val == "" || counted[key+":"+val] {
				return
			}
			counted[key+":"+val] = true
			m[val]++
		}
		for _, name := range []string{n, base} {
			segs, sep := segments(name)
			if len(segs) < 2 {
				continue
			}
			bump(s.seps, "sep", sep)
			bump(s.head, "head", segs[0])
			bump(s.tail, "tail", segs[len(segs)-1])
		}
	}
	return s
}

// segments splits a name on the first separator character it uses, returning the
// parts and which separator that was. A name with no separator returns itself.
func segments(name string) ([]string, string) {
	for _, sep := range sepChars {
		if strings.Contains(name, sep) {
			return strings.Split(name, sep), sep
		}
	}
	return []string{name}, ""
}

func normalize(s string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(s)), "_", "-")
}

// --- blanket rejects ---------------------------------------------------------

// docVocab is the vocabulary every technical document uses about units without
// naming one. A bare generic word is never a unit claim, whatever rule would
// otherwise admit it.
var docVocab = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		service services component components container containers module modules
		package packages app apps application applications system systems
		name names port ports path paths description descriptions purpose owner
		status notes note readme todo tbd na yes no none all any other others
		table overview architecture summary usage example examples default
		version versions type types kind kinds file files dir dirs directory
		main master head repo repository branch commit commits pr prs issue issues
		http https localhost stdin stdout stderr env var vars config configs
		true false null nil test tests build builds run runs make cmake docker
		image images tag tags api apis cli sdk ui db database cache queue
	`) {
		docVocab[w] = true
	}
}

// fileExt rejects a token that is plainly a file, not a unit.
var fileExt = regexp.MustCompile(`(?i)\.(md|go|py|ts|tsx|js|jsx|rs|c|cc|cpp|h|hpp|java|rb|sh|bash|zsh|yml|yaml|json|toml|ini|cfg|txt|lock|sum|mod|png|jpg|svg|gif|pdf|html|css|xml|proto|sql|env|dockerfile)$`)

// versionish rejects "v1.2.3", "1.2", "0.20.0-rc1" — a version is not a unit.
var versionish = regexp.MustCompile(`^v?\d+(\.\d+)*(-[a-z0-9.]+)?$`)

// urlish rejects a host or URL fragment picked up from a link.
var urlish = regexp.MustCompile(`(?i)^(https?|ftp|git|ssh)$|\.(com|org|net|io|dev|sh|ai|co|cloud|gov|edu)$`)

// tokenRE is the identifier shape a unit name can take in prose.
var tokenRE = regexp.MustCompile(`[A-Za-z0-9]+(?:[._/-][A-Za-z0-9]+)*`)

// screamingSnake matches an environment variable or a compile-time constant —
// CACHE_ENABLED, MAX_RETRIES. Normalization turns those into perfectly
// unit-shaped names ("cache-enabled"), so they must be rejected on the RAW token,
// before it is folded.
var screamingSnake = regexp.MustCompile(`^[A-Z][A-Z0-9]*(?:_[A-Z0-9]+)*$`)

// plausibleToken applies the blanket rejects — the tests every candidate must
// pass before any admission rule is even consulted.
func plausibleToken(tok string) bool {
	if screamingSnake.MatchString(strings.TrimSpace(tok)) {
		return false
	}
	t := normalize(tok)
	if len(t) < 3 || len(t) > 64 {
		return false
	}
	if versionish.MatchString(t) || urlish.MatchString(t) || fileExt.MatchString(t) {
		return false
	}
	if _, err := strconv.Atoi(t); err == nil {
		return false
	}
	if !strings.ContainsAny(t, "abcdefghijklmnopqrstuvwxyz") {
		return false
	}
	if strings.Count(t, "/") > 3 {
		return false
	}
	// Every segment generic → the token is doc vocabulary ("the service path").
	segs := strings.FieldsFunc(t, func(r rune) bool {
		return r == '-' || r == '.' || r == '/'
	})
	generic := 0
	for _, s := range segs {
		if docVocab[s] {
			generic++
		}
	}
	return generic < len(segs)
}

// --- admission ---------------------------------------------------------------

// admit returns the rule that lets tok in as a claimed unit, or "" for no.
// Rules are tried most-certain first, so the reported rule is the strongest
// evidence available rather than whichever one happened to run.
func (s shape) admit(tok string, colocated bool, repoDir string) string {
	// plausibleToken takes the RAW token: some rejects (SCREAMING_SNAKE env vars)
	// are only visible before case folding.
	if !plausibleToken(tok) {
		return ""
	}
	t := normalize(tok)
	if s.isExact(t) {
		return RuleExact
	}
	// Beyond exact match, a token must be NAME-SHAPED to be claimed as a unit:
	// at least two segments, or a path. A bare English word in an inventory-ish
	// group ("Full", "New", "Origin") is the dominant false positive on a real
	// repo — measured — because units generically named after single common
	// words make ordinary prose look like an inventory. The cost is real and
	// deliberate: a one-word phantom is invisible to this report.
	if !nameShaped(t) {
		return ""
	}
	if colocated {
		return RuleColocated
	}
	if s.pathAnchored(t, repoDir) {
		return RulePathAnchor
	}
	if s.familyAffix(t) {
		return RuleFamilyAffix
	}
	return ""
}

// nameShaped reports whether a token carries internal structure — a separator
// or a path — rather than being a single bare word.
func nameShaped(t string) bool {
	segs, _ := segments(t)
	return len(segs) > 1 || strings.Contains(t, "/")
}

// isExact reports whether tok names a detected unit — by unit name, by unit
// directory, or by the basename of either. The same test decides RuleExact and
// decides whether a table column / list / heading group is an inventory, so the
// two can never disagree about what "a real unit" means.
func (s shape) isExact(tok string) bool {
	t := normalize(tok)
	return s.names[t] || s.names[normalize(path.Base(t))] || s.unitDirs[strings.ToLower(t)]
}

// canonName is the key a claim is filed under: a path-shaped claim
// ("apps/depot-service") files under its basename, so the same unit named as
// a path in one document and as a bare name in another is ONE claim rather than
// two — which is what makes a cross-document path disagreement visible at all.
func canonName(tok string) string {
	t := normalize(tok)
	if strings.Contains(t, "/") {
		return normalize(path.Base(strings.TrimSuffix(t, "/")))
	}
	return t
}

// pathAnchored: "services/depot" where services/ is a real directory
// that really holds detected units, and services/depot is not there. The
// document is pointing at a slot in a layout nugit can see.
func (s shape) pathAnchored(t string, repoDir string) bool {
	if !strings.Contains(t, "/") {
		return false
	}
	parent := path.Dir(strings.Trim(t, "/"))
	if parent == "." || parent == "/" {
		return false
	}
	if !s.parentDir[strings.ToLower(parent)] {
		return false
	}
	if st, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(parent))); err != nil || !st.IsDir() {
		return false
	}
	return true
}

// familyAffix: the token is named the way this repo names its units — same
// separator as at least two units, and a first or last segment that at least two
// units use in the SAME position. Position matters: "service" trailing
// "parcel-service" and "crate-service" is a family; "service" appearing anywhere in
// anything is doc vocabulary.
func (s shape) familyAffix(t string) bool {
	if strings.Contains(t, "/") {
		return false // a path is the path-anchor rule's business, not this one
	}
	segs, sep := segments(t)
	if len(segs) < 2 || sep == "" || s.seps[sep] < minAffixUnits {
		return false
	}
	// "per-service", "multi-service", "non-service" all carry the family tail
	// and none of them is a unit: a qualifier in front of an affix makes an
	// English phrase, not a name.
	if qualifierWord[segs[0]] {
		return false
	}
	return s.head[segs[0]] >= minAffixUnits || s.tail[segs[len(segs)-1]] >= minAffixUnits
}

// qualifierWord is the closed set of English modifiers that turn a family affix
// into a phrase. Closed on purpose — a general dictionary would be a permanent
// maintenance surface, and this list covers the shapes prose actually produces.
var qualifierWord = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		per the this that each any all some every one two both other another
		non pre post sub super multi cross inter intra self semi anti co un re
		new old first second third last next prev previous current legacy
		single dual full half high low mid best worst same different
		our your their its my its no not more less most least
	`) {
		qualifierWord[w] = true
	}
}

// --- slot extraction ---------------------------------------------------------

var (
	headingRE    = regexp.MustCompile(`^(#{1,6})\s+(.*\S)\s*$`)
	listMarkerRE = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	backtickRE   = regexp.MustCompile("`([^`\n]+)`")
	tableSepRE   = regexp.MustCompile(`^\|?[\s:|-]*-[\s:|-]*\|?$`)
	portInline   = regexp.MustCompile(`(?i)\bport[s]?\b[^0-9]{0,12}(\d{2,5})|:(\d{2,5})\b`)
	pathValueRE  = regexp.MustCompile(`^[A-Za-z0-9._-]+(?:/[A-Za-z0-9._-]+)+/?$`)
	hasLetter    = regexp.MustCompile(`[A-Za-z]`)
	urlSpan      = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)
)

// slot is one place a document names something, with the whole line kept so the
// attributes asserted alongside (a port, a path) can be read off it.
type slot struct {
	Line      int
	Text      string // the cell / heading / item text
	Row       string // the full source line
	Colocated bool   // the enclosing group is an inventory of this repo's units
}

// scanDoc walks one document and yields every slot: table cells, headings, list
// items and inline code spans. Fenced code blocks are skipped wholesale — a
// shell transcript names binaries, flags and hosts, and mining it for unit
// claims is where a phantom report goes to die.
//
// Co-location is resolved in two passes because a group only becomes an
// inventory once enough of its members are known to be real: pass one collects
// the groups, pass two marks every member of a qualifying group.
func scanDoc(d docText, s shape) []slot {
	var slots []slot
	// group key -> indices into slots
	groups := map[string][]int{}
	var tableStart, tableRow, listRun int
	inFence := false

	addSlot := func(line int, text, row, group string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		// "@scope/name" is a package coordinate — a dependency reference in some
		// registry's namespace, not a directory in this tree. It normalizes into
		// a perfectly unit-shaped name, so it has to be dropped at the source.
		if strings.HasPrefix(text, "@") {
			return
		}
		slots = append(slots, slot{Line: line, Text: text, Row: row})
		if group != "" {
			groups[group] = append(groups[group], len(slots)-1)
		}
	}

	for i, raw := range d.Lines {
		line := i + 1
		t := strings.TrimSpace(raw)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || t == "" {
			if t == "" {
				tableStart = 0
			}
			continue
		}
		switch {
		case strings.HasPrefix(t, "|"):
			if tableSepRE.MatchString(t) {
				continue // the header rule; not data
			}
			if tableStart == 0 {
				tableStart, tableRow = line, 0
				listRun++
			}
			tableRow++
			for col, cell := range splitRow(t) {
				g := ""
				if tableRow > 1 { // row 1 is the header: column labels, not units
					g = "table:" + strconv.Itoa(tableStart) + ":" + strconv.Itoa(col)
				}
				addSlot(line, cellToken(cell), t, g)
			}
		case headingRE.MatchString(t):
			m := headingRE.FindStringSubmatch(t)
			addSlot(line, cellToken(m[2]), t, "head:"+strconv.Itoa(len(m[1])))
			tableStart, listRun = 0, listRun+1
		case listMarkerRE.MatchString(raw):
			m := listMarkerRE.FindStringSubmatch(raw)
			// A list group is one CONTIGUOUS run at one indent, never every
			// bullet in the document: an inventory is a list, not a file.
			addSlot(line, cellToken(m[3]), t, "list:"+strconv.Itoa(listRun)+":"+strconv.Itoa(len(m[1])))
			tableStart = 0
		default:
			if len(raw) > 0 && raw[0] != ' ' && raw[0] != '\t' {
				listRun++ // an unindented paragraph ends the run
			}
			tableStart = 0
		}
		// Inline code spans anywhere on the line are always candidates, but they
		// join no group: `foo` in running prose is a mention, not an inventory.
		for _, m := range backtickRE.FindAllStringSubmatch(t, -1) {
			addSlot(line, m[1], t, "")
		}
	}

	// Pass two: a group is an inventory when at least minColocated of its
	// members are real units AND they are at least colocatedShare of it. Both
	// halves are load-bearing — the count stops a coincidence, the share stops a
	// long list of prose that happens to contain two service names.
	for _, idxs := range groups {
		exact := 0
		for _, ix := range idxs {
			if tok := primaryToken(slots[ix].Text); tok != "" && s.isExact(tok) {
				exact++
			}
		}
		if exact < minColocated || float64(exact) < colocatedShare*float64(len(idxs)) {
			continue
		}
		for _, ix := range idxs {
			slots[ix].Colocated = true
		}
	}
	return slots
}

// splitRow splits a markdown table row into cells.
func splitRow(t string) []string {
	t = strings.TrimSuffix(strings.TrimPrefix(t, "|"), "|")
	return strings.Split(t, "|")
}

// cellToken reduces a cell or list-item head to the thing it names: the first
// backticked span if there is one, else the text with markdown emphasis and a
// trailing " — description" clause stripped.
func cellToken(cell string) string {
	c := strings.TrimSpace(cell)
	if m := backtickRE.FindStringSubmatch(c); m != nil {
		return m[1]
	}
	c = strings.Trim(c, "*_ ")
	for _, cut := range []string{" — ", " – ", " -- ", ": ", " (", ", "} {
		if ix := strings.Index(c, cut); ix > 0 {
			c = c[:ix]
		}
	}
	return strings.TrimSpace(c)
}

// primaryToken is the first identifier-shaped token of a slot's text — what the
// slot names, if it names anything.
func primaryToken(text string) string {
	if m := tokenRE.FindString(text); m != "" {
		return m
	}
	return ""
}

// --- attributes --------------------------------------------------------------

// rowPorts returns the port numbers asserted on a line: a bare numeric table
// cell in range, or an inline "port 8080" / ":8080".
func rowPorts(row string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	if strings.HasPrefix(strings.TrimSpace(row), "|") {
		for _, cell := range splitRow(row) {
			c := strings.Trim(strings.TrimSpace(cell), "`*_ ")
			if _, err := strconv.Atoi(c); err == nil {
				add(c)
			}
		}
	}
	// A port inside a URL is the port of the thing being CALLED, not of the unit
	// this row is about — an env-var table full of `http://other-svc:9000`
	// otherwise mints a disagreement for every row it has.
	for _, m := range portInline.FindAllStringSubmatch(urlSpan.ReplaceAllString(row, " "), -1) {
		for _, g := range m[1:] {
			if g != "" {
				add(g)
			}
		}
	}
	sort.Strings(out)
	return out
}

// rowPaths returns the repo-path-shaped values asserted on a line.
func rowPaths(row string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range backtickRE.FindAllStringSubmatch(row, -1) {
		v := strings.TrimSpace(m[1])
		if !pathValueRE.MatchString(v) || urlish.MatchString(v) || !hasLetter.MatchString(v) {
			continue
		}
		v = strings.TrimSuffix(v, "/")
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	if strings.HasPrefix(strings.TrimSpace(row), "|") {
		for _, cell := range splitRow(row) {
			v := strings.Trim(strings.TrimSpace(cell), "`*_ ")
			if !pathValueRE.MatchString(v) || urlish.MatchString(v) || !hasLetter.MatchString(v) {
				continue
			}
			v = strings.TrimSuffix(v, "/")
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	sort.Strings(out)
	return out
}
