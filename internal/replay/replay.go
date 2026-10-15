// Package replay mines a repository's own history into replay benchmark tasks
// (ADR-0044): a closed bead from the plan store is the prompt, the commit(s)
// that implemented it on the target branch are the solution, and the
// solution's parent is where an agent under test starts.
//
// Deterministic, git-only and LLM-free (ADR-0006). The single rule the package
// exists to keep is the LEAKAGE rule: everything a harness may show or score an
// agent against — the prompt, the oracle keys, the rejected alternatives, the
// decision density — is read from the repo as of `base`, never the working tree
// and never the tip. A decision the solution itself wrote did not exist when
// the work started, so it can never be an oracle for that work.
package replay

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/n8o/nugit/internal/beads"
	"github.com/n8o/nugit/internal/c4"
	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/retrieval"
)

// PromptSuffix ends every prompt: the harness grades a commit, so the agent
// must be told to make one.
const PromptSuffix = "Commit your work when done."

// Task is one replay benchmark task. The JSON shape is a contract with the
// evals harness: fields may be added, never renamed.
type Task struct {
	ID            string     `json:"id"`
	Prompt        string     `json:"prompt"`
	Path          string     `json:"path"`
	Base          string     `json:"base"`
	Verify        string     `json:"verify"` // always empty: the harness derives it
	Source        Source     `json:"source"`
	SolutionFiles []string   `json:"solution_files"`
	HiddenTests   []string   `json:"hidden_tests"`
	OracleKeys    []string   `json:"oracle_keys"`
	Tags          Tags       `json:"tags"`
	Rejected      []Rejected `json:"rejected"`
}

// Source records where the task came from.
type Source struct {
	Bead     string   `json:"bead"`
	Commits  []string `json:"commits"` // every first-parent commit on the branch that mentions the bead, oldest first
	Solution string   `json:"solution"`
	// Layout is how the solution landed: squash (one single-parent commit),
	// merge (one merge commit), or range (several commits; base is the parent
	// of the earliest, solution the latest).
	Layout string `json:"layout"`
	// SolutionTime is the solution head's committer time (RFC3339, UTC).
	SolutionTime string `json:"solution_time"`
	// Span counts the first-parent commits in base..solution — 1 for a squash
	// or merge, the run length for a contiguous range.
	Span int `json:"span"`
	// PromptRef says which store the prompt was read from: "base" when the bead
	// already existed there (the pre-implementation text), else "tip".
	PromptRef string `json:"prompt_ref"`
	ClosedAt  string `json:"closed_at,omitempty"`
	// CloseReason is metadata for graders. It describes the solution, so it is
	// never part of Prompt.
	CloseReason string `json:"close_reason,omitempty"`
}

// Tags are the task's stratification features.
type Tags struct {
	Kind string `json:"kind"`
	// Files is len(solution_files).
	Files int `json:"files"`
	// DecisionDensity counts live decisions context(path) would admit with no
	// task, in the store at base — global ones included.
	DecisionDensity int `json:"decision_density"`
	// DecisionDensityScoped is the part of DecisionDensity that is
	// component-scoped or path-bound — the part that actually varies by path.
	DecisionDensityScoped int  `json:"decision_density_scoped"`
	Churn90d              int  `json:"churn_90d"`
	HasRejected           bool `json:"has_rejected"`
	HasHiddenTests        bool `json:"has_hidden_tests"`
}

// Rejected is one oracle decision's rejected-alternatives prose, at base.
type Rejected struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// Options configures an export.
type Options struct {
	RepoDir string
	// Branch is the target branch to mine. Empty → origin/HEAD, origin/main,
	// origin/master, main, master — the first that resolves.
	Branch       string
	Since        time.Time // keep tasks whose solution landed at or after this; zero = no bound
	Limit        int       // keep the N newest tasks; 0 = all
	RequireTests bool      // keep only tasks with hidden tests
}

// Skip reasons, reported by name.
const (
	SkipNoLinkedCommit = "no-linked-commit"
	SkipDocsOnly       = "docs-only"
	SkipNoBase         = "no-base"
	SkipTestsOnly      = "tests-only"
)

// Report is the export's accounting, for stderr.
type Report struct {
	Branch    string         `json:"branch"`
	Beads     int            `json:"beads"`
	NotClosed int            `json:"not_closed"`
	Closed    int            `json:"closed"`
	Mined     int            `json:"mined"`
	Emitted   int            `json:"emitted"`
	Skipped   map[string]int `json:"skipped"`
	Filtered  map[string]int `json:"filtered"`
}

// Summary is the one-line stderr summary.
func (r Report) Summary() string {
	return fmt.Sprintf("nugit export: %d tasks from %d closed beads on %s (%d not closed); skipped: %s; filtered: %s\n",
		r.Emitted, r.Closed, r.Branch, r.NotClosed, counts(r.Skipped), counts(r.Filtered))
}

func counts(m map[string]int) string {
	if len(m) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("%s=%d", k, m[k])
	}
	return strings.Join(parts, ", ")
}

// DefaultBranches are tried in order when no branch is given.
var DefaultBranches = []string{"origin/HEAD", "origin/main", "origin/master", "main", "master"}

// WriteJSON writes the tasks as a pretty JSON array ("[]" when empty).
func WriteJSON(w io.Writer, tasks []Task) error {
	if tasks == nil {
		tasks = []Task{}
	}
	b, err := json.MarshalIndent(tasks, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

// tokenRe matches dash-joined alphanumeric runs — the shape of both bead ids
// (`gr-12-1`) and knowledge keys (`ADR-0012`, `LESSON-some-slug`). A mention
// must be the WHOLE token: `gr-12` never matches inside `gr-12-1`, which is
// what keeps an epic from claiming every one of its steps' commits.
var tokenRe = regexp.MustCompile(`[A-Za-z0-9]+(?:-[A-Za-z0-9]+)+`)

func tokens(s string) []string {
	return tokenRe.FindAllString(s, -1)
}

// conventionalRe parses a Conventional Commits subject's type.
var conventionalRe = regexp.MustCompile(`^([A-Za-z]+)(\([^)]*\))?!?:`)

func kindOf(subject string) (string, bool) {
	m := conventionalRe.FindStringSubmatch(strings.TrimSpace(subject))
	if m == nil {
		return "", false
	}
	switch t := strings.ToLower(m[1]); t {
	case "fix", "feat", "refactor", "docs", "test", "chore":
		return t, true
	case "tests":
		return "test", true
	default:
		return "other", true
	}
}

// pathClass classifies one changed path.
type pathClass int

const (
	classCode pathClass = iota
	classDoc
	classTest
	classStore // .beads/ or .nugit/ — never a solution file
)

// testRules is the test-file convention table. A path matching any rule is a
// test: the harness withholds it and applies it after the run.
var testRules = []struct {
	name  string
	match func(p, base string) bool
}{
	{"go", func(_, b string) bool { return strings.HasSuffix(b, "_test.go") }},
	{"js/ts .test/.spec", func(_, b string) bool {
		for _, mid := range []string{".test.", ".spec."} {
			if i := strings.LastIndex(b, mid); i > 0 {
				switch b[i+len(mid):] {
				case "ts", "tsx", "js", "jsx", "mjs", "cjs":
					return true
				}
			}
		}
		return false
	}},
	{"python", func(_, b string) bool {
		return strings.HasSuffix(b, ".py") && (strings.HasPrefix(b, "test_") || strings.HasSuffix(b, "_test.py"))
	}},
	{"c++", func(_, b string) bool {
		ext := path.Ext(b)
		if ext != ".cpp" && ext != ".cc" {
			return false
		}
		stem := strings.TrimSuffix(b, ext)
		return strings.HasSuffix(stem, "_test") || strings.Contains(stem, "Test")
	}},
	{"test dirs", func(p, _ string) bool {
		for _, seg := range strings.Split(path.Dir(p), "/") {
			switch seg {
			case "test", "tests", "spec", "__tests__":
				return true
			}
		}
		return false
	}},
}

// IsTestFile reports whether p (slash-separated) is a test by convention.
func IsTestFile(p string) bool {
	b := path.Base(p)
	for _, r := range testRules {
		if r.match(p, b) {
			return true
		}
	}
	return false
}

func isDoc(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".md", ".mdx", ".rst", ".adoc", ".txt":
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if seg == "docs" || seg == "doc" {
			return true
		}
	}
	return false
}

func isStore(p, prefix string) bool {
	for _, root := range []string{"", prefix} {
		if strings.HasPrefix(p, root+".beads/") || strings.HasPrefix(p, root+".nugit/") {
			return true
		}
	}
	return false
}

func classify(p, prefix string) pathClass {
	switch {
	case isStore(p, prefix):
		return classStore
	case IsTestFile(p):
		return classTest
	case isDoc(p):
		return classDoc
	}
	return classCode
}

// atBase is everything read from the repo as of one base commit, cached
// because several beads often share a base.
type atBase struct {
	objs   []model.KnowledgeObject
	byID   map[string]*model.KnowledgeObject // lowercased id → decision/lesson
	byPath map[string]*model.KnowledgeObject // nugit-root-relative path → object
	model  model.Model
	beads  map[string]beads.Issue
	time   time.Time
}

type exporter struct {
	repo   gitutil.Repo
	prefix string
	cache  map[string]*atBase
	commit map[string]*gitutil.HistoryCommit
}

func (e *exporter) base(sha string) *atBase {
	if b, ok := e.cache[sha]; ok {
		return b
	}
	b := &atBase{byID: map[string]*model.KnowledgeObject{}, byPath: map[string]*model.KnowledgeObject{}, beads: map[string]beads.Issue{}}
	e.cache[sha] = b
	if c := e.commit[sha]; c != nil {
		b.time = c.Time
	}
	objs, err := knowledge.LoadAtRef(e.repo, sha, e.prefix)
	if err != nil {
		return b // an unreadable store at base contributes no oracle, never a tip read
	}
	b.objs = objs
	for i := range b.objs {
		o := &b.objs[i]
		b.byPath[o.Path] = o
		if o.ID != "" && (o.Type == model.KindDecision || o.Type == model.KindLesson) {
			b.byID[strings.ToLower(o.ID)] = o
		}
	}
	cfgSrc, _ := e.repo.ShowFile(sha, e.prefix+".nugit/config.yml")
	cfg, _ := config.LoadBytes([]byte(cfgSrc))
	dsl := cfg.C4.DSL
	if dsl == "" {
		dsl = ".nugit/architecture/workspace.dsl"
	}
	dslSrc, _ := e.repo.ShowFile(sha, e.prefix+dsl)
	b.model = c4.Parse(dslSrc)
	if st, ok := beads.Read(e.repo, sha, e.prefix); ok {
		for _, it := range st.Issues {
			b.beads[strings.ToLower(it.ID)] = it
		}
	}
	return b
}

// ResolveBranch returns the branch to mine and its tip sha.
func ResolveBranch(repo gitutil.Repo, branch string) (string, string, error) {
	if branch != "" {
		if sha := repo.Resolve(branch); sha != "" {
			return branch, sha, nil
		}
		return "", "", fmt.Errorf("branch %q does not resolve", branch)
	}
	for _, b := range DefaultBranches {
		if sha := repo.Resolve(b); sha != "" {
			return b, sha, nil
		}
	}
	return "", "", fmt.Errorf("no target branch: none of %s resolves (pass -branch)", strings.Join(DefaultBranches, ", "))
}

// Export mines the tasks.
func Export(opt Options) ([]Task, Report, error) {
	repo := gitutil.Repo{Dir: opt.RepoDir}
	rep := Report{Skipped: map[string]int{}, Filtered: map[string]int{}}
	branch, tip, err := ResolveBranch(repo, opt.Branch)
	if err != nil {
		return nil, rep, err
	}
	rep.Branch = branch
	e := &exporter{repo: repo, prefix: repo.Prefix(), cache: map[string]*atBase{}, commit: map[string]*gitutil.HistoryCommit{}}

	store, ok := beads.Read(repo, tip, e.prefix)
	if !ok {
		return nil, rep, fmt.Errorf("no beads plan store (.beads/**/*.jsonl) at %s", branch)
	}
	rep.Beads = len(store.Issues)
	known := map[string]bool{}
	for _, it := range store.Issues {
		if it.ID != "" {
			known[strings.ToLower(it.ID)] = true
		}
	}

	all, fp, err := repo.History(tip)
	if err != nil {
		return nil, rep, err
	}
	for i := range all {
		e.commit[all[i].SHA] = &all[i]
	}
	fpIdx := make(map[string]int, len(fp))
	for i, s := range fp {
		fpIdx[s] = i
	}
	// Attribute every side-branch commit to the first-parent merge that
	// introduced it, oldest merge first, so a bead named only on a feature
	// branch commit still links to the merge that landed it.
	sides := make([][]string, len(fp))
	owner := map[string]int{}
	for i, s := range fp {
		c := e.commit[s]
		if c == nil || len(c.Parents) < 2 {
			continue
		}
		stack := append([]string(nil), c.Parents[1:]...)
		for len(stack) > 0 {
			x := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if _, onFP := fpIdx[x]; onFP {
				continue
			}
			if _, done := owner[x]; done {
				continue
			}
			owner[x] = i
			sides[i] = append(sides[i], x)
			if xc := e.commit[x]; xc != nil {
				stack = append(stack, xc.Parents...)
			}
		}
	}
	mentions := map[string]map[int]bool{}
	for _, c := range all {
		i, onFP := fpIdx[c.SHA]
		if !onFP {
			var owned bool
			if i, owned = owner[c.SHA]; !owned {
				continue
			}
		}
		for _, tok := range tokens(c.Message()) {
			if t := strings.ToLower(tok); known[t] {
				if mentions[t] == nil {
					mentions[t] = map[int]bool{}
				}
				mentions[t][i] = true
			}
		}
	}

	var tasks []Task
	for _, it := range store.Issues {
		if it.ID == "" {
			continue
		}
		if !beads.IsDone(it.Status) {
			rep.NotClosed++
			continue
		}
		rep.Closed++
		t, reason := e.mine(it, fp, sides, mentions[strings.ToLower(it.ID)])
		if reason != "" {
			rep.Skipped[reason]++
			continue
		}
		rep.Mined++
		tasks = append(tasks, t)
	}

	// Newest solution first; id breaks ties so the order is total.
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Source.SolutionTime != tasks[j].Source.SolutionTime {
			return tasks[i].Source.SolutionTime > tasks[j].Source.SolutionTime
		}
		return tasks[i].ID < tasks[j].ID
	})
	var out []Task
	for _, t := range tasks {
		if opt.RequireTests && len(t.HiddenTests) == 0 {
			rep.Filtered["require-tests"]++
			continue
		}
		if !opt.Since.IsZero() {
			if st, _ := time.Parse(time.RFC3339, t.Source.SolutionTime); st.Before(opt.Since) {
				rep.Filtered["since"]++
				continue
			}
		}
		if opt.Limit > 0 && len(out) >= opt.Limit {
			rep.Filtered["limit"]++
			continue
		}
		out = append(out, t)
	}
	rep.Emitted = len(out)
	return out, rep, nil
}

// contiguous reports whether every first-parent commit between the first and
// last implementing commit is itself linked to the bead.
func contiguous(impl []int, linked map[int]bool) bool {
	for k := 1; k < len(impl); k++ {
		for j := impl[k-1] + 1; j < impl[k]; j++ {
			if !linked[j] {
				return false
			}
		}
	}
	return true
}

// closingRe matches a closing verb directly before a token.
var closingRe = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?|implement[sd]?|bead)[:\s]+([A-Za-z0-9]+(?:-[A-Za-z0-9]+)+)`)

// bestCommit picks the one implementing commit that most plainly is the
// bead's work. Deterministic: score, then latest.
func (e *exporter) bestCommit(id string, fp []string, impl []int) int {
	score := func(i int) int {
		c := e.commit[fp[i]]
		if c == nil {
			return -1
		}
		s := 0
		for _, tok := range tokens(c.Subject) {
			if strings.EqualFold(tok, id) {
				s += 2
				break
			}
		}
		for _, m := range closingRe.FindAllStringSubmatch(c.Message(), -1) {
			if strings.EqualFold(m[1], id) {
				s++
				break
			}
		}
		return s
	}
	best, bestScore := impl[len(impl)-1], score(impl[len(impl)-1])
	for k := len(impl) - 2; k >= 0; k-- {
		if s := score(impl[k]); s > bestScore {
			best, bestScore = impl[k], s
		}
	}
	return best
}

// mine builds one task, or names why it cannot.
func (e *exporter) mine(it beads.Issue, fp []string, sides [][]string, linked map[int]bool) (Task, string) {
	if len(linked) == 0 {
		return Task{}, SkipNoLinkedCommit
	}
	idxs := make([]int, 0, len(linked))
	for i := range linked {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)

	// A commit that touches only the plan/knowledge store (the bead-closing
	// `chore(beads): close …` commit) is linked but implements nothing, so it
	// never decides the range.
	var impl []int
	for _, i := range idxs {
		c := e.commit[fp[i]]
		if c == nil || len(c.Parents) == 0 {
			impl = append(impl, i) // a root commit: no base, reported below
			continue
		}
		changes, err := e.repo.NameStatus(c.Parents[0], c.SHA)
		if err != nil {
			continue
		}
		for _, ch := range changes {
			if !isStore(ch.Path, e.prefix) {
				impl = append(impl, i)
				break
			}
		}
	}
	if len(impl) == 0 {
		return Task{}, SkipDocsOnly
	}
	// Several implementing commits form ONE solution only when nothing
	// unrelated landed between them. A bead is routinely named again by a
	// later follow-up weeks after it closed, and spanning base..follow-up would
	// hand the agent's diff every unrelated commit in between. Then the single
	// commit that most plainly IS the work wins: the id in its subject, else a
	// closing verb before the id, else the latest.
	if !contiguous(impl, linked) {
		impl = []int{e.bestCommit(it.ID, fp, impl)}
	}
	first, last := e.commit[fp[impl[0]]], e.commit[fp[impl[len(impl)-1]]]
	if first == nil || last == nil || len(first.Parents) == 0 {
		return Task{}, SkipNoBase
	}
	base, sol := first.Parents[0], last.SHA

	changes, err := e.repo.NameStatus(base, sol)
	if err != nil {
		return Task{}, SkipNoBase
	}
	numstat, _ := e.repo.Numstat(base, sol)
	var solutionFiles, hidden, storeChanged []string
	storeStatus := map[string]string{}
	anyCode, anyTest := false, false
	type cand struct {
		path  string
		lines int
		doc   bool
	}
	var cands []cand
	for _, ch := range changes {
		switch classify(ch.Path, e.prefix) {
		case classStore:
			if strings.HasPrefix(ch.Path, e.prefix+".nugit/") && strings.HasSuffix(ch.Path, ".md") {
				storeChanged = append(storeChanged, ch.Path)
				storeStatus[ch.Path] = ch.Status
			}
		case classTest:
			anyTest = true
			if ch.Status != "D" {
				hidden = append(hidden, ch.Path)
			}
		default:
			doc := isDoc(ch.Path)
			if !doc {
				anyCode = true
			}
			solutionFiles = append(solutionFiles, ch.Path)
			if ch.Status != "D" {
				n := numstat[ch.Path]
				cands = append(cands, cand{ch.Path, n[0] + n[1], doc})
			}
		}
	}
	if !anyCode && !anyTest {
		return Task{}, SkipDocsOnly
	}
	if len(solutionFiles) == 0 {
		return Task{}, SkipTestsOnly
	}
	// Primary path: the code file with the most changed lines; a doc only
	// when the solution changed no code file at all.
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].doc != cands[j].doc {
			return !cands[i].doc
		}
		if cands[i].lines != cands[j].lines {
			return cands[i].lines > cands[j].lines
		}
		return cands[i].path < cands[j].path
	})
	primary := solutionFiles[0]
	if len(cands) > 0 {
		primary = cands[0].path
	}
	sort.Strings(solutionFiles)
	sort.Strings(hidden)

	at := e.base(base)

	// The prompt: the bead as it stood at base when it already existed there
	// (written before the work), else the tip's record.
	src, promptRef := it, "tip"
	if b, ok := at.beads[strings.ToLower(it.ID)]; ok {
		src, promptRef = b, "base"
	}
	prompt := strings.TrimSpace(src.Title)
	if d := strings.TrimSpace(src.Field("description")); d != "" {
		prompt += "\n\n" + d
	}
	prompt = strings.TrimSpace(prompt + "\n\n" + PromptSuffix)

	// Every commit the solution is made of, for messages: the linked
	// first-parent commits up to the solution head plus what their merges
	// brought in.
	var msgs []*gitutil.HistoryCommit
	var commits []string
	for _, i := range idxs {
		commits = append(commits, fp[i])
		if i < impl[0] || i > impl[len(impl)-1] {
			continue // before base or after the solution: not the solution's message
		}
		msgs = append(msgs, e.commit[fp[i]])
		for _, s := range sides[i] {
			msgs = append(msgs, e.commit[s])
		}
	}

	oracle := map[string]bool{}
	cite := func(key string) {
		if o := at.byID[strings.ToLower(strings.TrimSpace(key))]; o != nil {
			oracle[o.ID] = true
		}
	}
	for _, c := range msgs {
		if c == nil {
			continue
		}
		for _, tok := range tokens(c.Message()) {
			cite(tok)
		}
	}
	if len(storeChanged) > 0 {
		var live []string
		for _, p := range storeChanged {
			if storeStatus[p] != "D" {
				live = append(live, p)
			}
		}
		srcs, _ := e.repo.ShowFiles(sol, live)
		for _, p := range storeChanged {
			rel := strings.TrimPrefix(p, e.prefix)
			if storeStatus[p] == "D" {
				if o := at.byPath[rel]; o != nil {
					cite(o.ID) // deleted: it existed at base by construction
				}
				continue
			}
			obj, ok := knowledge.ParseObject(rel, srcs[p])
			if !ok {
				continue
			}
			// A file the solution ADDED is not an oracle — it did not exist
			// yet — but what it points at may well have. A modified or
			// renamed file's own id existed at base, so it is.
			if storeStatus[p] != "A" {
				cite(obj.ID)
			}
			for _, rt := range obj.RelatesTo {
				cite(knowledge.ParseEdge(rt).Target)
			}
			cite(obj.Supersedes)
		}
	}
	keys := make([]string, 0, len(oracle))
	for k := range oracle {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rejected := []Rejected{}
	for _, k := range keys {
		if o := at.byID[strings.ToLower(k)]; o != nil && strings.TrimSpace(o.Rejected) != "" {
			rejected = append(rejected, Rejected{Key: k, Text: o.Rejected})
		}
	}

	scoped := retrieval.ScopedDecisions(at.model, at.objs, strings.TrimPrefix(primary, e.prefix))
	nScoped := 0
	for i := range scoped {
		if s := scoped[i].Scope; (s != "" && s != "global") || knowledge.AppliesTo(&scoped[i], strings.TrimPrefix(primary, e.prefix)) {
			nScoped++
		}
	}

	churn := 0
	if !at.time.IsZero() {
		churn, _ = e.repo.CountTouching(base, at.time.Add(-90*24*time.Hour), at.time, solutionFiles)
	}

	kind := "other"
	if k, ok := kindOf(last.Subject); ok {
		kind = k
	} else {
		for j := len(msgs) - 1; j >= 0; j-- {
			if msgs[j] == nil {
				continue
			}
			if k, ok := kindOf(msgs[j].Subject); ok {
				kind = k
				break
			}
		}
	}

	layout := "range"
	if len(impl) == 1 {
		layout = "squash"
		if len(last.Parents) > 1 {
			layout = "merge"
		}
	}

	if hidden == nil {
		hidden = []string{}
	}
	return Task{
		ID:     it.ID,
		Prompt: prompt,
		Path:   primary,
		Base:   base,
		Verify: "",
		Source: Source{
			Bead:         it.ID,
			Commits:      commits,
			Solution:     sol,
			Layout:       layout,
			Span:         impl[len(impl)-1] - impl[0] + 1,
			SolutionTime: last.Time.Format(time.RFC3339),
			PromptRef:    promptRef,
			ClosedAt:     it.Field("closed_at"),
			CloseReason:  it.Field("close_reason"),
		},
		SolutionFiles: solutionFiles,
		HiddenTests:   hidden,
		OracleKeys:    keys,
		Tags: Tags{
			Kind:                  kind,
			Files:                 len(solutionFiles),
			DecisionDensity:       len(scoped),
			DecisionDensityScoped: nScoped,
			Churn90d:              churn,
			HasRejected:           len(rejected) > 0,
			HasHiddenTests:        len(hidden) > 0,
		},
		Rejected: rejected,
	}, ""
}
