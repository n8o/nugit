package adopt

// Prose-inventory parsing and THE PRECISION RULE (ADR-0036).
//
// A repo's prose names hundreds of things: vendors, protocols, file names, verbs
// capitalized at the start of a bullet. Only a handful of them are claims about
// this repo's own units, and a false "documented but absent" discredits the whole
// report — a reader who finds one phantom that is really a typo stops reading.
//
// So a prose token becomes a CLAIMED UNIT only when the document itself gives
// STRUCTURAL evidence that it is naming this repo's units. Three admission
// rules, each recorded on the finding so a reader can audit which one fired;
// everything else in the prose is ignored.
//
// There used to be a fourth, lexical rule — "family affix": a token that shares
// a same-position separator segment with two or more detected units. It was
// removed after being run against a second real monorepo. Its precision turns
// out to be a property of the REPO, not of the rule: it is sharp when a repo's
// units share a distinctive role suffix (`*-service`) and it collapses when they
// share ordinary domain nouns, because then every hyphenated phrase in ordinary
// prose carries the family shape. On that repo it admitted 41 of 47 findings,
// and the sampled ones were struct fields, local variables and a Dockerfile
// name. Resemblance to a unit name is not evidence about the repo; only the
// document's own structure is.

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
	// heading group where at least two OTHER entries RESOLVE in this tree. The
	// document's own structure declares the group an inventory of this repo.
	RuleColocated = "inventory-colocation"
	// RulePathAnchor — the token is path-shaped and its parent directory really
	// exists and really contains a detected unit, but the full path does not.
	RulePathAnchor = "path-anchor"
)

const (
	// minColocated: how many entries in a group must resolve in this tree before
	// the group counts as an inventory. Two, not one: a single real name next to
	// a word is a coincidence, two is a list.
	minColocated = 2
	// colocatedShare: and they must be at least this fraction of the group.
	// Without it, ANY long bullet list containing two service names would make
	// every other bullet a unit claim — measured on a real 21-service monorepo
	// that admitted 395 phantoms, nearly all of them English words. A document's
	// structure only vouches for a group when the group is MOSTLY real.
	colocatedShare = 0.5
	// maxDocBytes bounds a single document read — a generated changelog is not
	// an inventory and must not be able to blow up the report.
	maxDocBytes = 1 << 20
	// maxDocs bounds the corpus walked. Deterministic (docs are sorted) so the
	// truncation point does not move between runs.
	maxDocs = 400
	// maxDirListings bounds how many directories the resolver may read while
	// deciding whether prose names something real. A docs tree with thousands of
	// distinct path-shaped tokens must not turn a report into a tree walk.
	maxDirListings = 2000
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
	// Spellings are the raw tokens that produced this claim. A claim is filed
	// under its basename, so `apps/gateway/api/v1` files under `v1` — and the
	// only way to check whether the document was pointing at something that
	// really exists is to keep the path it actually wrote.
	Spellings []string
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
	repoDir     string
	names       map[string]bool // normalized unit names + dir basenames
	unitDirs    map[string]bool // normalized detected unit directories
	parentDir   map[string]bool // directories that CONTAIN a detected unit
	parents     []string        // parentDir, sorted — deterministic lookup order
	unitDirList []string        // unitDirs, sorted — deterministic lookup order

	// unitLike / existing / listings memoize the filesystem side of the two
	// resolution tests. The maps are shared by every copy of the shape value,
	// which is the point: the quorum test runs once per slot in a whole docs tree.
	unitLike map[string]bool
	existing map[string]bool
	listings map[string]map[string]string // dir -> normalized child name -> real name
}

var sepChars = []string{"-", "_", "."}

func newShape(units []unitRef, repoDir string) shape {
	if repoDir == "" {
		repoDir = "."
	}
	s := shape{
		repoDir: repoDir,
		names:   map[string]bool{}, unitDirs: map[string]bool{}, parentDir: map[string]bool{},
		unitLike: map[string]bool{}, existing: map[string]bool{},
		listings: map[string]map[string]string{},
	}
	for _, u := range units {
		s.names[normalize(u.Name)] = true
		s.names[normalize(path.Base(u.Dir))] = true
		s.unitDirs[normalize(u.Dir)] = true
		if d := path.Dir(u.Dir); d != "." && d != "/" && d != "" {
			s.parentDir[normalize(d)] = true
		}
	}
	for p := range s.parentDir {
		s.parents = append(s.parents, p)
	}
	for d := range s.unitDirs {
		s.unitDirList = append(s.unitDirList, d)
	}
	sort.Strings(s.parents)
	sort.Strings(s.unitDirList)
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

// fileTypeWord is the other half of that test. Some build files put the type
// word FIRST and the subject after it — `Dockerfile.press-binder`,
// `Makefile.local`, `CMakeLists.shared` — which normalizes into a perfectly
// unit-shaped two-segment name and sails past a trailing-extension check. A
// token whose basename starts `<file-type>.` is a filename, not a unit.
//
// The dot is required. A unit may legitimately be NAMED after what it does with
// those files (`dockerfile-linter`), and only the `<type>.<subject>` spelling is
// unambiguously a file.
var fileTypeWord = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`
		dockerfile containerfile makefile cmakelists justfile taskfile
		jenkinsfile procfile gemfile rakefile brewfile vagrantfile
		readme license notice changelog copying codeowners
	`) {
		fileTypeWord[w] = true
	}
}

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
	if len(segs) == 0 {
		return false
	}
	// A leading file-type word makes the whole token a filename, wherever it was
	// found: `Dockerfile.press-binder` is a build file, not a service.
	if base := path.Base(t); strings.Contains(base, ".") &&
		fileTypeWord[base[:strings.Index(base, ".")]] {
		return false
	}
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
	// And that is the whole list. A token that merely LOOKS like this repo's
	// units — same separator, same head or tail segment — is not admitted: see
	// the package comment for why that rule was removed rather than narrowed.
	return ""
}

// nameShaped reports whether a token carries internal structure — a separator
// or a path — rather than being a single bare word.
func nameShaped(t string) bool {
	segs, _ := segments(t)
	return len(segs) > 1 || strings.Contains(t, "/")
}

// isExact reports whether tok names a DETECTED unit — by unit name, by unit
// directory, or by the basename of either. It decides RuleExact and nothing
// else.
func (s shape) isExact(tok string) bool {
	t := normalize(tok)
	return s.names[t] || s.names[normalize(path.Base(t))] || s.unitDirs[t]
}

// namesAUnit is the CO-LOCATION QUORUM currency: does this token name a unit of
// this repo — one the detectors found, or one sitting right beside a unit they
// did find?
//
// Widening it from "detected unit" to "detected unit or its sibling directory"
// is the fix for a measured defect. On a real monorepo a 16-row service table in
// which 15 rows were real directories under `apps/` failed to qualify as an
// inventory, because only 4 of those services had a detector (ADR-0021 has
// blind spots) — so the one row that was genuinely a phantom could not be
// admitted by the document's structure and fell to a lexical rule instead.
//
// It stops at siblings deliberately. A directory at the REPO ROOT (`build/`,
// `k8s/`, `docs/`) is not a unit, and counting one as quorum currency turns a
// license table's "Build" column into an inventory — measured, on the same repo.
//
// Comparison is on NORMALIZED path segments, so `libs/crate_index` answers to
// `crate-index`. Prose spells a directory the way prose feels like.
func (s shape) namesAUnit(tok string) bool {
	return s.lookup(s.unitLike, tok, func(t string) bool {
		if s.names[t] || s.names[normalize(path.Base(t))] || s.unitDirs[t] {
			return true
		}
		if strings.Contains(t, "/") {
			return s.parentDir[path.Dir(strings.Trim(t, "/"))] && s.dirExists(t)
		}
		for _, p := range s.parents {
			if s.dirExists(p + "/" + t) {
				return true
			}
		}
		return false
	})
}

// existsHere is the ABSENCE VETO: does this token name anything real in this
// tree at all? Wider than namesAUnit on purpose — a root-level directory counts,
// and so does the exact path the document wrote. The asymmetry is the point:
// deciding a group is an inventory should be strict, and deciding a name is a
// phantom should be generous, because a real directory reported as absent is the
// single most discrediting output this report can produce.
func (s shape) existsHere(tok string) bool {
	return s.lookup(s.existing, tok, func(t string) bool {
		if s.names[t] || s.names[normalize(path.Base(t))] || s.unitDirs[t] {
			return true
		}
		if s.dirExists(t) {
			return true
		}
		if !strings.Contains(t, "/") {
			for _, p := range s.parents {
				if s.dirExists(p + "/" + t) {
					return true
				}
			}
			return false
		}
		// A path in a document is not always relative to the repo root. A unit
		// that is itself a workspace repeats the layout inside its own directory,
		// so a paragraph about that unit writes `apps/frontend` meaning
		// `<that unit>/apps/frontend`. Reading it against the root alone reports a
		// directory that is right there as a phantom — measured.
		for _, u := range s.unitDirList {
			if s.dirExists(u + "/" + t) {
				return true
			}
		}
		return false
	})
}

// lookup normalizes, memoizes and rejects the degenerate tokens once, so the two
// tests above are just their own predicate.
func (s shape) lookup(memo map[string]bool, tok string, fn func(string) bool) bool {
	t := normalize(tok)
	if t == "" || t == "." || t == "/" {
		return false
	}
	if v, ok := memo[t]; ok {
		return v
	}
	v := fn(t)
	memo[t] = v
	return v
}

// dirExists walks a repo-relative path segment by segment, matching each
// segment against the real directory entries by normalized name.
func (s shape) dirExists(rel string) bool {
	cur := "."
	segs := strings.Split(strings.Trim(rel, "/"), "/")
	if len(segs) == 0 || len(segs) > 8 {
		return false
	}
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
		real, ok := s.listing(cur)[normalize(seg)]
		if !ok {
			return false
		}
		if cur == "." {
			cur = real
		} else {
			cur += "/" + real
		}
	}
	return cur != "."
}

// listing returns one directory's child DIRECTORIES, keyed by normalized name,
// read at most once per run and capped so a docs tree full of path-shaped
// tokens cannot turn this report into a tree walk.
func (s shape) listing(rel string) map[string]string {
	if m, ok := s.listings[rel]; ok {
		return m
	}
	m := map[string]string{}
	if len(s.listings) < maxDirListings {
		ents, err := os.ReadDir(filepath.Join(s.repoDir, filepath.FromSlash(rel)))
		if err == nil {
			for _, e := range ents {
				if !e.IsDir() {
					continue
				}
				n := normalize(e.Name())
				if _, dup := m[n]; !dup {
					m[n] = e.Name()
				}
			}
		}
	}
	s.listings[rel] = m
	return m
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
	if !s.parentDir[normalize(parent)] {
		return false
	}
	if st, err := os.Stat(filepath.Join(repoDir, filepath.FromSlash(parent))); err != nil || !st.IsDir() {
		return false
	}
	return true
}

// --- slot extraction ---------------------------------------------------------

var (
	headingRE    = regexp.MustCompile(`^(#{1,6})\s+(.*\S)\s*$`)
	listMarkerRE = regexp.MustCompile(`^(\s*)([-*+]|\d+[.)])\s+(.*)$`)
	backtickRE   = regexp.MustCompile("`([^`\n]+)`")
	tableSepRE   = regexp.MustCompile(`^\|?[\s:|-]*-[\s:|-]*\|?$`)
	portInline   = regexp.MustCompile(`(?i)\bport[s]?\b[^0-9]{0,12}(\d{2,5})|:(\d{2,5})\b`)
	urlSpan      = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://\S+`)
	portColumnRE = regexp.MustCompile(`(?i)\bports?\b`)
)

// slot is one place a document names something, with the whole line kept so the
// scalar attributes asserted alongside (a port) can be read off it.
type slot struct {
	Line      int
	Text      string // the cell / heading / item text
	Row       string // the full source line
	Colocated bool   // the enclosing group is an inventory of this repo's units
	// Header is the enclosing table's header row, cell by cell, for a table slot
	// and nil otherwise. A bare number in a table cell is a port only when the
	// column says it is a port — the column headed "Default" in an env-var table
	// is full of timeouts in milliseconds, and reading those as ports invents a
	// disagreement for every row.
	Header []string
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
	var tableHeader []string
	inFence := false

	addSlot := func(line int, text, row, group string, header []string) {
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
		slots = append(slots, slot{Line: line, Text: text, Row: row, Header: header})
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
				tableHeader = splitRow(t)
				listRun++
			}
			tableRow++
			for col, cell := range splitRow(t) {
				g := ""
				if tableRow > 1 { // row 1 is the header: column labels, not units
					g = "table:" + strconv.Itoa(tableStart) + ":" + strconv.Itoa(col)
				}
				addSlot(line, cellToken(cell), t, g, tableHeader)
			}
		case headingRE.MatchString(t):
			m := headingRE.FindStringSubmatch(t)
			addSlot(line, cellToken(m[2]), t, "head:"+strconv.Itoa(len(m[1])), nil)
			tableStart, listRun = 0, listRun+1
		case listMarkerRE.MatchString(raw):
			m := listMarkerRE.FindStringSubmatch(raw)
			// A list group is one CONTIGUOUS run at one indent, never every
			// bullet in the document: an inventory is a list, not a file.
			addSlot(line, cellToken(m[3]), t, "list:"+strconv.Itoa(listRun)+":"+strconv.Itoa(len(m[1])), nil)
			tableStart = 0
		default:
			if len(raw) > 0 && raw[0] != ' ' && raw[0] != '\t' {
				listRun++ // an unindented paragraph ends the run
			}
			tableStart = 0
		}
		// Inline code spans anywhere on the line are always candidates, but they
		// join no group: `foo` in running prose is a mention, not an inventory.
		hdr := []string(nil)
		if tableStart != 0 {
			hdr = tableHeader
		}
		for _, m := range backtickRE.FindAllStringSubmatch(t, -1) {
			addSlot(line, m[1], t, "", hdr)
		}
	}

	// Pass two: a group is an inventory when at least minColocated of its
	// members NAME A UNIT of this repo AND they are at least colocatedShare of
	// it. Both halves are load-bearing — the count stops a coincidence, the
	// share stops a long list of prose that happens to contain two service names.
	for _, idxs := range groups {
		real := 0
		for _, ix := range idxs {
			if tok := primaryToken(slots[ix].Text); tok != "" && s.namesAUnit(tok) {
				real++
			}
		}
		if real < minColocated || float64(real) < colocatedShare*float64(len(idxs)) {
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
//
// Only SCALAR attributes are collected, and that is a narrowing forced by
// evidence. An attribute is worth reporting a disagreement about when two
// documents asserting different values means at least one of them is WRONG — a
// port, a version, a replica count: one fact, one value. Paths are not like
// that. Two documents listing different files under one component are both
// telling the truth about different subsets, and every path "disagreement" this
// report produced on a real monorepo (7 of 7) was that shape. See ADR-0036.

// rowPorts returns the port numbers asserted on a line: a bare numeric cell in
// a column the table HEADS as a port, or an inline "port 8080" / ":8080".
//
// The header condition is not decoration. Without it every bare number in every
// table is a port, and a service-configuration table whose "Default" column
// holds `15000` (a poll interval, in milliseconds) manufactures a port conflict
// with the same service's real port — measured on a real repo.
func rowPorts(row string, header []string) []string {
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
		for col, cell := range splitRow(row) {
			if col >= len(header) || !portColumnRE.MatchString(header[col]) {
				continue
			}
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
