// Package adopt computes the argument for adopting nugit on a brownfield repo,
// from the repo alone (ADR-0036).
//
// A repo that has never run `nugit init` still has a knowledge store — it is
// just written in prose, unversioned against the code, and duplicated across an
// agent-instruction file, an architecture document and a shelf of runbooks. The
// interesting thing about those documents is not what they say but where they
// DISAGREE: with the tree, and with each other. That disagreement is measurable,
// and measuring it is what this package does.
//
// Three hard constraints, all of them load-bearing:
//
//   - It runs BEFORE adoption. Nothing here reads `.nugit/`, workspace.dsl, the
//     knowledge store or config. The primary caller is a repo that has none of
//     them.
//   - Detected units come from modelfacts.Units (ADR-0021) — the same inventory
//     the model-drift check uses. There is no second detector.
//   - It is a report, never a gate: read-only by default, exit 0 always.
package adopt

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/modelfacts"
)

// unitRef is the detected-unit type, aliased so this package never grows a
// second notion of what a unit is.
type unitRef = modelfacts.Unit

// commitScanCap bounds every history walk: staleness is O(cap) per document,
// not O(history). A document more than this many commits behind is reported as
// "at least" — the argument is already made at that magnitude.
const commitScanCap = 20000

// Options configures one adopt run.
type Options struct {
	RepoDir string
	// WriteCandidates opts into the ONE thing adopt writes: runbook candidates
	// into the candidate lane (.nugit/lessons/, status: proposed, ADR-0016).
	// It never touches workspace.dsl or config — that is `nugit init`'s job.
	WriteCandidates bool
	// Now stamps written candidates; empty means time.Now().UTC().
	Now string
	// Peers are sibling checkouts to attribute cited paths to (ADR-0038). They
	// are passed IN rather than read from `.nugit/config.yml` because adopt
	// still reads no store (ADR-0036 clause 1) and its primary caller has none;
	// with no peers the report falls back to history corroboration alone.
	Peers []Peer
}

// Doc is one prose inventory and how far behind the code it is.
type Doc struct {
	Path string `json:"path"`
	// LastCommit / LastDate are empty when the file is untracked.
	LastCommit   string `json:"last_commit,omitempty"`
	LastDate     string `json:"last_date,omitempty"`
	CommitsSince int    `json:"commits_since"`
	Capped       bool   `json:"commits_since_capped,omitempty"`
	Claims       int    `json:"claimed_units"`
	Untracked    bool   `json:"untracked,omitempty"`
}

// Absent is a unit the prose names that the tree does not contain — the phantom
// service. Rule records which admission rule let the name in, so a reader can
// audit the finding rather than trust it.
type Absent struct {
	Name string `json:"name"`
	Rule string `json:"rule"`
	// DeletedPath / DeletedIn / DeletedAt are the history's corroboration
	// (ADR-0038): the path this repository once tracked, and the commit that
	// removed it. Set for every `path-anchor` finding, because that rule may no
	// longer assert a phantom without it. A `inventory-colocation` finding
	// carries them when the history happens to agree and is unaffected when it
	// does not — that rule's corroboration is two documents, not the index.
	DeletedPath string `json:"deleted_path,omitempty"`
	DeletedIn   string `json:"deleted_in,omitempty"`
	DeletedAt   string `json:"deleted_at,omitempty"`
	// Peer names a configured peer whose checkout ALSO contains this path — a
	// unit that moved to a sibling rather than one that simply went away.
	Peer     string    `json:"also_in_peer,omitempty"`
	Mentions []mention `json:"mentions"`
}

// Elsewhere is a name this repo's prose cites that this repo has never
// contained: no such directory now, and nothing by that path in its history.
// It is deliberately NOT reported as this repo's phantom — the measured case is
// a decision record about the boundary with a sibling repo, naming the
// sibling's directories, which `path-anchor` cannot distinguish from its own
// tree (ADR-0038).
//
// Peer names the sibling that does contain it, when one is configured and
// checked out. Empty means nobody nugit can see contains it — still worth
// listing, in its own clearly-labelled bucket, but never as an assertion about
// this repo.
type Elsewhere struct {
	Name     string    `json:"name"`
	Rule     string    `json:"rule"`
	Peer     string    `json:"peer,omitempty"`
	PeerPath string    `json:"peer_path,omitempty"`
	Mentions []mention `json:"mentions"`
}

// Disagreement is one SCALAR attribute two documents assert differently about
// the same unit. Scalar is the whole point: a port has one right answer, so two
// documents giving two values means one of them is wrong. A set-valued
// attribute — the files a component touches — has no such property, and is not
// reported here (ADR-0036).
type Disagreement struct {
	Unit   string      `json:"unit"`
	Attr   string      `json:"attr"` // "port"
	Claims []AttrClaim `json:"claims"`
}

// AttrClaim is one document's assertion.
type AttrClaim struct {
	Doc    string `json:"doc"`
	Line   int    `json:"line"`
	Values string `json:"values"`
}

// Report is the whole computed adoption argument.
type Report struct {
	Repo         string   `json:"repo"`
	HasStore     bool     `json:"has_nugit_store"`
	TotalCommits int      `json:"total_commits"`
	TotalCapped  bool     `json:"total_commits_capped,omitempty"`
	Units        []Unit   `json:"units"`
	Docs         []Doc    `json:"prose_inventories"`
	Absent       []Absent `json:"documented_but_absent"`
	// Elsewhere are cited paths this repo never contained — another repo's, as
	// far as this repo's own history knows (ADR-0038).
	Elsewhere []Elsewhere `json:"cited_but_never_here,omitempty"`
	// Peers records what each configured peer checkout contributed, including
	// the ones that were not on disk (ADR-0032 clause 3).
	Peers []PeerStatus `json:"peers,omitempty"`
	// Undocumented are detected units no prose inventory mentions anywhere —
	// searched over the FULL text of every document, not just its inventory
	// slots, so "undocumented" means genuinely unnamed.
	Undocumented  []Unit         `json:"present_but_undocumented"`
	Disagreements []Disagreement `json:"disagreements"`
	Runbooks      []Runbook      `json:"runbook_candidates"`
	// Written lists candidate lesson paths written by -write-candidates.
	Written []string `json:"written_candidates,omitempty"`
	// Notes record what the report could NOT determine, so a limitation is
	// visible in the output rather than only in the ADR.
	Notes []string `json:"notes,omitempty"`
}

// Unit is a detected unit as this report names it.
type Unit struct {
	Dir      string `json:"dir"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}

// Run computes the report. It never fails on a repo it does not understand: a
// repo with no git history, no docs or no detected units yields an honest empty
// report plus a note, because an adoption pitch that errors out is no pitch.
func Run(opt Options) (Report, error) {
	repoDir := opt.RepoDir
	if repoDir == "" {
		repoDir = "."
	}
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Repo: filepath.Base(strings.TrimRight(abs, string(filepath.Separator)))}
	if st, serr := os.Stat(filepath.Join(repoDir, ".nugit")); serr == nil && st.IsDir() {
		rep.HasStore = true
	}

	units := modelfacts.Units(repoDir)
	for _, u := range units {
		rep.Units = append(rep.Units, Unit(u))
	}
	sh := newShape(units, repoDir)

	docPaths := discoverDocs(repoDir)
	if len(docPaths) == 0 {
		rep.Notes = append(rep.Notes, "no prose inventories found (no CLAUDE.md/AGENTS.md/README.md, no docs/**/*.md) — nothing to diff the tree against")
	}
	if len(units) == 0 {
		rep.Notes = append(rep.Notes, "no code units detected — the deployable/CMake/Go detectors found nothing here, so every prose claim is unverifiable (ADR-0021 documents the detector blind spots)")
	}

	docs := make([]docText, 0, len(docPaths))
	for _, p := range docPaths {
		if d, ok := readDoc(repoDir, p); ok {
			docs = append(docs, d)
		}
	}

	peers, peerStatus := loadPeers(opt.Peers)
	rep.Peers = peerStatus
	for _, ps := range peerStatus {
		if !ps.Present {
			rep.Notes = append(rep.Notes, "peer `"+ps.Name+"` is not checked out at `"+ps.Dir+"` — it contributed nothing (ADR-0032: an absent peer is never an error)")
		}
	}

	claims, perDoc := gatherClaims(docs, sh, repoDir)
	rep.Absent, rep.Elsewhere = partitionClaims(claims, sh, repoDir, peers, &rep)
	rep.Undocumented = undocumented(units, docs)
	rep.Disagreements = disagreements(claims)
	rep.Docs = staleness(repoDir, docs, perDoc, &rep)
	rep.Runbooks = runbookCandidates(repoDir, docs)

	if opt.WriteCandidates {
		written, werr := writeCandidates(repoDir, rep.Runbooks, opt.Now)
		if werr != nil {
			return rep, werr
		}
		rep.Written = written
	}
	return rep, nil
}

// gatherClaims extracts every claimed unit from every document, returning the
// claims keyed by normalized name and the per-document claim count.
func gatherClaims(docs []docText, sh shape, repoDir string) (map[string]*claim, map[string]int) {
	claims := map[string]*claim{}
	perDoc := map[string]int{}
	// One table row names a unit in its name cell, in its path cell and inside a
	// backticked span — three slots, one claim. Mentions are deduped per
	// (unit, document, line) so the evidence list stays a list of places, and
	// the per-document count stays a count of units rather than of tokens.
	mentionAt := map[string]int{} // (unit, doc, line) -> index into c.Mentions
	seenPerDoc := map[string]bool{}
	for _, d := range docs {
		for _, sl := range scanDoc(d, sh) {
			tok := primaryToken(sl.Text)
			if tok == "" {
				continue
			}
			rule := sh.admit(tok, sl.Colocated, repoDir)
			if rule == "" {
				continue
			}
			name := canonName(tok)
			c := claims[name]
			if c == nil {
				c = &claim{Name: name, Ports: map[string][]mention{}}
				claims[name] = c
			}
			c.addSpelling(tok)
			key := name + "\x00" + d.Path + "\x00" + strconv.Itoa(sl.Line)
			if ix, ok := mentionAt[key]; ok {
				// Same place, second slot: keep the STRONGEST evidence, so a name
				// that a table column vouches for is never filed under the weaker
				// rule a code span on the same line happened to match first.
				if ruleRank[rule] < ruleRank[c.Mentions[ix].Rule] {
					c.Mentions[ix].Rule = rule
				}
			} else {
				mentionAt[key] = len(c.Mentions)
				c.Mentions = append(c.Mentions, mention{Doc: d.Path, Line: sl.Line, Text: trimRow(sl.Row), Rule: rule})
			}
			if dk := name + "\x00" + d.Path; !seenPerDoc[dk] {
				seenPerDoc[dk] = true
				perDoc[d.Path]++
			}
			for _, p := range rowPorts(sl.Row, sl.Header) {
				c.Ports[d.Path] = append(c.Ports[d.Path], mention{Doc: d.Path, Line: sl.Line, Text: p})
			}
		}
	}
	return claims, perDoc
}

// partitionClaims splits the claims that survive the absence veto into the two
// buckets this report may assert: the phantoms of THIS repo, and the paths this
// repo cites but has never contained.
//
// The directory check is the load-bearing precision guard. modelfacts.Units has
// documented blind spots (ADR-0021: no Python or Rust unit detector), so a real
// service the detectors cannot see would otherwise be reported as a phantom —
// the single most discrediting false positive this report can produce. A name
// that resolves to a real directory is never called absent, whatever the
// detectors think of it.
//
// Every SPELLING the prose used is checked, not just the name the claim is filed
// under. A claim is keyed by basename, so `apps/gateway/api/v1` files under
// `v1`; asking only whether `v1` exists at the root or beside a unit answers a
// question the document never asked.
func partitionClaims(claims map[string]*claim, sh shape, repoDir string, peers []*peerRepo, rep *Report) ([]Absent, []Elsewhere) {
	// Survivors of the veto, in a deterministic order.
	names := make([]string, 0, len(claims))
	for n := range claims {
		names = append(names, n)
	}
	sort.Strings(names)

	type candidate struct {
		name string
		rule string
		c    *claim
	}
	var cands []candidate
	var query []string
	for _, name := range names {
		c := claims[name]
		rule := strongestRule(c)
		if rule == RuleExact {
			continue
		}
		if sh.existsHere(name) || c.anySpellingResolves(sh) {
			continue
		}
		// CORROBORATION, for co-location only. That rule's whole evidence is
		// statistical — "most of this group is real, so the rest of it is too" —
		// and one group in one document is one editor's list. A plan document
		// that tables a service it intends to build, a numbered migration step
		// naming a database table, a feature matrix whose Status column reads
		// "Production-ready": all measured, all admitted by a single qualifying
		// group, none of them a claim this repo makes about itself. Two
		// independent documents naming the same missing unit is the shape of the
		// finding this report exists to make.
		if rule == RuleColocated && c.docCount() < minCorroborating {
			continue
		}
		cands = append(cands, candidate{name: name, rule: rule, c: c})
		query = append(query, c.historyPaths(sh)...)
	}
	if len(cands) == 0 {
		return nil, nil
	}

	// ONE history call for the whole report: which of these paths did this
	// repository once track and later delete? That is the corroboration
	// path-anchor now requires, and it is the same principle ADR-0037 used —
	// ask the version-control system, it already knows what this repo owns.
	sort.Strings(query)
	deleted, herr := gitutil.Repo{Dir: repoDir}.DeletedPaths(query, commitScanCap)
	if herr != nil {
		rep.Notes = append(rep.Notes, "no git history readable here — a cited path could not be corroborated against this repo's own history, so nothing admitted by `"+RulePathAnchor+"` alone is asserted as a phantom")
	}

	var absent []Absent
	var elsewhere []Elsewhere
	for _, cd := range cands {
		ms := append([]mention(nil), cd.c.Mentions...)
		sort.Slice(ms, func(i, j int) bool {
			if ms[i].Doc != ms[j].Doc {
				return ms[i].Doc < ms[j].Doc
			}
			return ms[i].Line < ms[j].Line
		})
		peerName, peerPath := "", ""
		for _, p := range peers {
			if hit := p.holds(cd.c); hit != "" {
				peerName, peerPath = p.name, hit
				break
			}
		}
		del, everHere := cd.c.deletion(sh, deleted)

		// THE NARROWING (ADR-0038). `path-anchor`'s evidence is that a real
		// parent directory has an empty slot where the document points. That is
		// evidence about the LAYOUT, and two sibling repos in one org share a
		// layout — so it is exactly as consistent with "this path is the other
		// repo's" as with "this repo deleted it". The history tells the two
		// apart deterministically: a path this repo once tracked is a phantom,
		// a path it never tracked is not this repo's to claim.
		//
		// Co-location is untouched. Its corroboration is two independent
		// documents of THIS repo naming the same missing unit, which is a claim
		// this repo makes about itself; requiring the index to agree as well
		// would silence exactly the finding this report exists to make (a
		// service deleted long enough ago to fall outside any bounded walk, or
		// one that never had a directory of its own).
		//
		// The peer half is the same judgement from the other side: a sibling
		// that really holds the path answers the question better than "absent"
		// does, whatever rule admitted it — the name is not missing, it is over
		// there. ADR-0032 made the sibling readable; this is the first place
		// adopt spends it.
		if !everHere && (cd.rule == RulePathAnchor || peerName != "") {
			elsewhere = append(elsewhere, Elsewhere{
				Name: cd.name, Rule: cd.rule, Peer: peerName, PeerPath: peerPath, Mentions: ms,
			})
			continue
		}
		a := Absent{Name: cd.name, Rule: cd.rule, Peer: peerName, Mentions: ms}
		if everHere {
			a.DeletedPath, a.DeletedIn, a.DeletedAt = del.Path, del.SHA, del.Date
		}
		absent = append(absent, a)
	}
	sort.Slice(absent, func(i, j int) bool { return absent[i].Name < absent[j].Name })
	sort.Slice(elsewhere, func(i, j int) bool { return elsewhere[i].Name < elsewhere[j].Name })
	return absent, elsewhere
}

// historyPaths are the paths worth asking this repo's history about: every
// path-shaped spelling the prose actually wrote, plus the slot this name would
// occupy under each directory that holds a detected unit.
//
// The second half is why the true positive measured on a real repo is
// citeable. That finding is a bare NAME in three tables — the prose never
// writes its directory — so the spellings alone ask git nothing, and the report
// could say "this unit is gone" without being able to show when. `apps/<name>`
// is the same slot the absence veto already walks, so asking about it costs
// nothing new and turns an assertion into a citation. Both separator spellings
// are asked: a claim is filed under a hyphenated key, and a repo that names its
// directories with underscores would otherwise never match its own history.
func (c *claim) historyPaths(sh shape) []string {
	var out []string
	for _, s := range c.Spellings {
		if strings.Contains(s, "/") {
			out = append(out, strings.Trim(strings.TrimSpace(s), "/"))
		}
	}
	variants := []string{c.Name}
	if u := strings.ReplaceAll(c.Name, "-", "_"); u != c.Name {
		variants = append(variants, u)
	}
	for _, p := range sh.parentsRaw {
		for _, v := range variants {
			out = append(out, p+"/"+v)
		}
	}
	return out
}

// deletion looks this claim's paths up in the batched history answer, returning
// the newest deletion of any of them.
func (c *claim) deletion(sh shape, deleted map[string]gitutil.Deletion) (gitutil.Deletion, bool) {
	var best gitutil.Deletion
	found := false
	for _, s := range c.historyPaths(sh) {
		d, ok := deleted[s]
		if !ok {
			continue
		}
		if !found || d.Date > best.Date {
			best, found = d, true
		}
	}
	return best, found
}

// ruleRank orders the admission rules by how much evidence they carry.
var ruleRank = map[string]int{RuleExact: 0, RuleColocated: 1, RulePathAnchor: 2}

func strongestRule(c *claim) string {
	best := ""
	for _, m := range c.Mentions {
		if best == "" || ruleRank[m.Rule] < ruleRank[best] {
			best = m.Rule
		}
	}
	return best
}

// minCorroborating is how many DISTINCT documents must name a co-located claim
// before it is reported absent. Two, for the same reason two entries make a
// group an inventory: one is an editor, two is the repo.
const minCorroborating = 2

// docCount is how many distinct documents mention this claim.
func (c *claim) docCount() int {
	docs := map[string]bool{}
	for _, m := range c.Mentions {
		docs[m.Doc] = true
	}
	return len(docs)
}

// addSpelling records one raw token that produced this claim, deduped on its
// normalized form. The RAW text is kept: every consumer that only compares
// names normalizes on the way in, and the one consumer that asks git about a
// path (ADR-0038) needs the bytes the document actually wrote.
func (c *claim) addSpelling(tok string) {
	t := normalize(tok)
	for _, s := range c.Spellings {
		if normalize(s) == t {
			return
		}
	}
	c.Spellings = append(c.Spellings, strings.TrimSpace(tok))
}

// anySpellingResolves reports whether any spelling the prose used names
// something that really exists — the veto that keeps a real directory out of
// the phantom list.
func (c *claim) anySpellingResolves(sh shape) bool {
	for _, s := range c.Spellings {
		if sh.existsHere(s) {
			return true
		}
	}
	return false
}

// undocumented returns detected units no document names anywhere. The search is
// deliberately LIBERAL — the full text of every document, token-split, not just
// the inventory slots — because the asymmetry runs the other way here: claiming
// a documented unit is undocumented is the false positive that costs credibility.
func undocumented(units []unitRef, docs []docText) []Unit {
	mentioned := map[string]bool{}
	for _, d := range docs {
		for _, line := range d.Lines {
			for _, tok := range tokenRE.FindAllString(line, -1) {
				t := normalize(tok)
				mentioned[t] = true
				mentioned[normalize(path.Base(t))] = true
			}
		}
	}
	var out []Unit
	for _, u := range units {
		if mentioned[normalize(u.Name)] || mentioned[normalize(path.Base(u.Dir))] || mentioned[strings.ToLower(u.Dir)] {
			continue
		}
		out = append(out, Unit(u))
	}
	sort.Slice(out, func(i, j int) bool {
		ki, kj := kindRank(out[i].Kind), kindRank(out[j].Kind)
		if ki != kj {
			return ki < kj
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

func kindRank(k string) int {
	switch k {
	case "deployable":
		return 0
	case "cmake":
		return 1
	default:
		return 2
	}
}

// disagreements finds units two documents describe differently. Only CROSS-
// document conflicts are reported, only where both documents actually assert a
// value (silence is not disagreement), and only for SCALAR attributes.
//
// The scalar restriction is a narrowing forced by measurement. It used to
// include "path", on the theory that two documents placing one unit in two
// directories is a contradiction. In practice documents do not assert a
// component's location; they cite some of its files, and different documents
// cite different ones. On a real monorepo every path finding (7 of 7) was two
// documents listing different, individually-correct file subsets — noise
// dressed as a contradiction, next to three genuine port conflicts on a sibling
// repo. A disagreement is only worth a reader's time when the two values cannot
// both be true, and that is a property of scalar facts: a port, a version, a
// replica count. See ADR-0036.
func disagreements(claims map[string]*claim) []Disagreement {
	var out []Disagreement
	names := make([]string, 0, len(claims))
	for n := range claims {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		c := claims[n]
		for _, attr := range []struct {
			name string
			set  map[string][]mention
		}{{"port", c.Ports}} {
			byDoc := map[string]string{}
			lineOf := map[string]int{}
			for doc, ms := range attr.set {
				vals := map[string]bool{}
				for _, m := range ms {
					vals[m.Text] = true
					if l, ok := lineOf[doc]; !ok || m.Line < l {
						lineOf[doc] = m.Line
					}
				}
				byDoc[doc] = joinSorted(vals)
			}
			if len(byDoc) < 2 {
				continue
			}
			distinct := map[string]bool{}
			for _, v := range byDoc {
				distinct[v] = true
			}
			if len(distinct) < 2 {
				continue
			}
			docs := make([]string, 0, len(byDoc))
			for d := range byDoc {
				docs = append(docs, d)
			}
			sort.Strings(docs)
			d := Disagreement{Unit: n, Attr: attr.name}
			for _, doc := range docs {
				d.Claims = append(d.Claims, AttrClaim{Doc: doc, Line: lineOf[doc], Values: byDoc[doc]})
			}
			out = append(out, d)
		}
	}
	return out
}

func joinSorted(set map[string]bool) string {
	vals := make([]string, 0, len(set))
	for v := range set {
		vals = append(vals, v)
	}
	sort.Strings(vals)
	return strings.Join(vals, ", ")
}

// staleness dates every prose inventory against the history: its last-touched
// commit and how many commits have landed since. A document 1,300 commits
// behind is the whole argument, so this is computed for the root files always
// and for any document that made a unit claim.
func staleness(repoDir string, docs []docText, perDoc map[string]int, rep *Report) []Doc {
	repo := gitutil.Repo{Dir: repoDir}
	total, capped, terr := repo.CountCommits("HEAD", commitScanCap)
	if terr != nil {
		rep.Notes = append(rep.Notes, "no git history readable here — document staleness could not be computed")
		return nil
	}
	rep.TotalCommits, rep.TotalCapped = total, capped

	var out []Doc
	for _, d := range docs {
		root := false
		for _, r := range inventoryRoots {
			if d.Path == r {
				root = true
			}
		}
		if !root && perDoc[d.Path] == 0 {
			continue
		}
		doc := Doc{Path: d.Path, Claims: perDoc[d.Path]}
		sha, date, err := repo.LastCommitFor(d.Path)
		switch {
		case err != nil || sha == "":
			doc.Untracked = true
		default:
			doc.LastCommit, doc.LastDate = sha, date
			n, c, cerr := repo.CountCommits(sha+"..HEAD", commitScanCap)
			if cerr == nil {
				doc.CommitsSince, doc.Capped = n, c
			}
		}
		out = append(out, doc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CommitsSince != out[j].CommitsSince {
			return out[i].CommitsSince > out[j].CommitsSince
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func trimRow(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 160 {
		s = strings.TrimSpace(s[:160]) + "…"
	}
	return s
}

func nowStamp(now string) string {
	if now != "" {
		return now
	}
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}
