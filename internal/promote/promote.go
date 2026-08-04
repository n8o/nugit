// Package promote copies a LOCAL knowledge object into the organization hub's
// checkout so it can become org-wide knowledge (ADR-0035, phase 4).
//
// The boundary this package exists to hold is the whole point of it: promote
// writes ONE file into the hub's working tree and does nothing else. It never
// commits, never pushes, never touches the network, and never runs git inside
// the hub — not even to read. The human who owns the hub reviews the resulting
// dirty file and opens the PR there. That is the same "you propose, you do not
// merge" discipline every nugit writer follows (ADR-0011: the only writer into
// `.nugit/**` is a reviewed PR), applied across a repo boundary where the agent
// running promote frequently has no business landing anything.
//
// Three rules make a promoted record honest at the far end:
//
//   - It lands as `status: proposed`. The candidate lane (ADR-0016) applies at
//     the hub too: ratification is the hub owner's act, and nobody can ratify a
//     record into someone else's corpus by copying it there.
//   - Its provenance is REWRITTEN to name the origin repo and the source
//     commit, so the hub can always answer "where did this come from".
//   - Its id is NOT rewritten. Ids are stable human keys (ADR-0001) and the
//     record's own prose and edges still spell them the old way.
package promote

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/distill"
	"github.com/n8o/nugit/internal/gitutil"
	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

// Options configure one promotion.
type Options struct {
	// RepoDir is the nugit root of the repo the record is promoted FROM.
	RepoDir string
	// ID is the record's stable key, as authored (`ADR-0007`, `LESSON-foo`).
	ID string
	// To overrides the destination peer by name. Empty means the configured
	// `org.hub`. A peer named here must still be a configured peer — promote
	// never takes a raw path, because writing into an arbitrary directory is
	// not a thing a memory tool should make one flag away.
	To string
	// Force overrides the near-duplicate refusal and an occupied destination
	// path. It never overrides an id collision — see Promote.
	Force bool
	// DryRun prints the plan and writes nothing.
	DryRun bool
}

// Result reports what was (or would be) written.
type Result struct {
	ID   string
	Kind model.Kind
	// Hub is the destination peer's name, HubDir its checkout.
	Hub    string
	HubDir string
	// SourcePath is nugit-root-relative in the origin repo; DestPath is
	// nugit-root-relative in the hub.
	SourcePath string
	DestPath   string
	// OriginRepo and Commit are what the rewritten provenance records.
	OriginRepo string
	Commit     string
	// Content is the exact bytes written (or that would be, on a dry run).
	Content string
	DryRun  bool
	// Overwrote is true when an existing hub file was replaced under -force.
	Overwrote bool
	// DanglingEdges names `relates_to` targets the record cites that the hub
	// does not hold. Advisory: a citation nothing resolves is inert, not wrong,
	// and the origin stamp is how a reader chases it back.
	DanglingEdges []string
}

// DuplicateError is the near-duplicate refusal: the hub already holds a record
// that overlaps this one enough that promoting would grow the org a second copy
// of one fact instead of merging knowledge into the first.
type DuplicateError struct {
	// ID and Path name the hub record that already covers this.
	ID, Path string
	// Why is the signal that fired, in the words of ADR-0018's dedup rule.
	Why string
}

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("the hub already holds %s (%s) — %s; merge into it, or re-run with -force to add a second record",
		e.ID, e.Path, e.Why)
}

// storeDir maps a kind to its directory inside a `.nugit/` store. A glossary is
// deliberately absent: it is a single un-typed local file that names THIS repo's
// vocabulary, which ADR-0032 already kept out of the federated set.
func storeDir(k model.Kind) (string, error) {
	switch k {
	case model.KindDecision:
		return "decisions", nil
	case model.KindLesson:
		return "lessons", nil
	case model.KindSpec:
		return "specs", nil
	case model.KindReference:
		return "references", nil
	case model.KindContract:
		return "contracts", nil
	}
	return "", fmt.Errorf("type %q cannot be promoted", k)
}

// Promote copies one ratified local record into the hub's store.
//
// It refuses, with a sentence naming the fix, when: no hub is configured; the
// hub is not checked out here; the id resolves to nothing locally; the record is
// still a candidate (or dead); this repo declares no `org.repo`, so the hub
// could not be told where the record came from; or the hub already holds a
// near-duplicate (`-force` overrides that one).
//
// Two refusals are deliberately NOT overridable, because they are correctness
// hazards rather than judgement calls: an id already held by a different hub
// record (ADR-0001 keys are stable, and two records under one key is a store
// nobody can reason about), and a `supersedes:` edge whose target the hub also
// holds — copying that record in would derive the HUB's record to superseded,
// which is precisely the silent cross-store kill ADR-0032 point 5 exists to
// prevent.
func Promote(opt Options) (Result, error) {
	cfg, err := config.Load(opt.RepoDir)
	if err != nil {
		return Result{}, err
	}
	hub, err := destination(cfg, opt.To)
	if err != nil {
		return Result{}, err
	}
	hubDir := hub.Dir(opt.RepoDir)
	if err := reachable(hubDir, hub.Name); err != nil {
		return Result{}, err
	}
	if cfg.Org.Repo == "" {
		return Result{}, fmt.Errorf("this repo declares no `org.repo` — a promoted record must say which repo it came from, and nugit never guesses an identity (ADR-0033); set org.repo in .nugit/config.yml")
	}

	objs, err := knowledge.Load(opt.RepoDir)
	if err != nil {
		return Result{}, err
	}
	obj, err := find(objs, opt.ID)
	if err != nil {
		return Result{}, err
	}
	if err := promotable(obj); err != nil {
		return Result{}, err
	}
	dir, err := storeDir(obj.Type)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", obj.ID, err)
	}

	hubObjs, err := knowledge.Load(hubDir)
	if err != nil {
		return Result{}, fmt.Errorf("reading the hub store at %s: %w", hubDir, err)
	}
	if err := checkCollisions(obj, hubObjs); err != nil {
		return Result{}, err
	}
	if !opt.Force {
		if dup := nearDuplicate(obj, hubObjs); dup != nil {
			return Result{}, dup
		}
	}

	res := Result{
		ID: obj.ID, Kind: obj.Type, Hub: hub.Name, HubDir: hubDir,
		SourcePath: obj.Path,
		DestPath:   filepath.ToSlash(filepath.Join(".nugit", dir, filepath.Base(obj.Path))),
		OriginRepo: cfg.Org.Repo,
		Commit:     sourceCommit(opt.RepoDir),
		DryRun:     opt.DryRun,
	}
	res.DanglingEdges = danglingEdges(obj, hubObjs)

	src, err := os.ReadFile(filepath.Join(opt.RepoDir, filepath.FromSlash(obj.Path)))
	if err != nil {
		return Result{}, err
	}
	res.Content, err = rewrite(string(src), res.OriginRepo, obj.Path, res.Commit)
	if err != nil {
		return Result{}, fmt.Errorf("%s (%s): %w", obj.ID, obj.Path, err)
	}

	abs := filepath.Join(hubDir, filepath.FromSlash(res.DestPath))
	if _, serr := os.Stat(abs); serr == nil {
		if !opt.Force {
			return Result{}, fmt.Errorf("%s already exists in the hub — re-run with -force to overwrite it", res.DestPath)
		}
		res.Overwrote = true
	}
	if opt.DryRun {
		return res, nil
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return Result{}, err
	}
	// The single write. No git, no network, no second file — see the package doc.
	if err := os.WriteFile(abs, []byte(res.Content), 0o644); err != nil {
		return Result{}, err
	}
	return res, nil
}

// destination resolves which peer receives the record: the explicit -to name, or
// the designated hub. Either way it must be a CONFIGURED peer, so the set of
// directories promote can write into is exactly the set this repo already
// declared it federates with.
func destination(cfg config.Config, to string) (config.Peer, error) {
	if to = strings.ToLower(strings.TrimSpace(to)); to != "" {
		for _, p := range cfg.Peers {
			if p.Name == to {
				return p, nil
			}
		}
		return config.Peer{}, fmt.Errorf("no peer named %q in .nugit/config.yml — promote only writes into a configured peer%s", to, peerHint(cfg))
	}
	if cfg.Org.Hub == "" {
		return config.Peer{}, errors.New("no org hub configured — set `org.hub: <peer-name>` in .nugit/config.yml (naming one of your `peers:`), or pass -to <peer>")
	}
	p, ok := cfg.HubPeer()
	if !ok {
		return config.Peer{}, fmt.Errorf("org.hub names %q, which is not one of the configured peers%s", cfg.Org.Hub, peerHint(cfg))
	}
	return p, nil
}

func peerHint(cfg config.Config) string {
	if len(cfg.Peers) == 0 {
		return " (this repo configures none)"
	}
	names := make([]string, 0, len(cfg.Peers))
	for _, p := range cfg.Peers {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return " (configured: " + strings.Join(names, ", ") + ")"
}

// reachable is the same degradation ADR-0032 gives every peer, stated as an
// error here because promote is an explicit act with a destination: reading an
// absent peer contributes nothing silently, but writing into one cannot.
func reachable(dir, name string) error {
	fi, err := os.Stat(filepath.Join(dir, ".nugit"))
	if err != nil || !fi.IsDir() {
		return fmt.Errorf("hub %q is not checked out here — no .nugit/ at %s; clone it beside this repo (nothing is fetched, ADR-0032)", name, dir)
	}
	return nil
}

// find resolves an id to exactly one local record. An exact match wins; failing
// that a unique case-insensitive match is accepted, because a lesson id is a
// long slug and refusing on case alone helps nobody.
func find(objs []model.KnowledgeObject, id string) (model.KnowledgeObject, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return model.KnowledgeObject{}, errors.New("no id given")
	}
	var fold []model.KnowledgeObject
	for _, o := range objs {
		if o.ID == id {
			return o, nil
		}
		if strings.EqualFold(o.ID, id) && o.ID != "" {
			fold = append(fold, o)
		}
	}
	if len(fold) == 1 {
		return fold[0], nil
	}
	return model.KnowledgeObject{}, fmt.Errorf("no knowledge object with id %q in this repo's store", id)
}

// promotable refuses records that must not become org-wide.
func promotable(o model.KnowledgeObject) error {
	switch o.EffectiveStatus {
	case model.StatusProposed:
		return fmt.Errorf("%s is `proposed` — the candidate lane is a LOCAL review queue (ADR-0016); ratify it here with `nugit ratify %s` before offering it to the org", o.ID, o.ID)
	case model.StatusSuperseded, model.StatusInvalidated:
		return fmt.Errorf("%s is %s — promoting a dead record would publish a known-wrong answer org-wide; promote whatever replaced it", o.ID, o.EffectiveStatus)
	}
	if o.Type == "" {
		return fmt.Errorf("%s has no `type:` — an untyped file has no store directory to land in", o.ID)
	}
	if o.Provenance.OriginRepo != "" {
		return fmt.Errorf("%s was itself promoted from %q — it is already a copy, and re-promoting it would make a third writer for one fact (ADR-0011); promote from the repo that authored it",
			o.ID, o.Provenance.OriginRepo)
	}
	return nil
}

// checkCollisions holds the two non-overridable refusals: a hub record already
// under this id, and a supersession that would kill a hub record.
func checkCollisions(o model.KnowledgeObject, hub []model.KnowledgeObject) error {
	for _, h := range hub {
		if h.ID != "" && h.ID == o.ID {
			return fmt.Errorf("the hub already holds %s (%s) under this id — ids are stable keys and are never rewritten on promotion (ADR-0001), so nugit will not create a second one; supersede the hub's record there, or give this one a distinct id at home",
				h.ID, h.Path)
		}
	}
	if o.Supersedes == "" {
		return nil
	}
	for _, h := range hub {
		if h.ID != "" && h.ID == o.Supersedes {
			return fmt.Errorf("%s declares `supersedes: %s`, and the hub holds a record under that id (%s) — copying this in would derive the HUB's record to superseded, which is exactly the silent cross-store kill ADR-0032 guards against; resolve the id clash before promoting",
				o.ID, o.Supersedes, h.Path)
		}
	}
	return nil
}

// nearDuplicate applies the ADR-0018 dedup rule — the SAME rule `nugit distill`
// uses for proposals, reached through internal/distill so the org can never end
// up with two notions of "we already know this". Keywords first (the authored
// signal), title words as the fallback for records that carry no keyword line.
//
// Only live hub records of the same kind are considered: a superseded hub record
// is not a reason to withhold a fresh one, and a lesson never duplicates an ADR.
func nearDuplicate(o model.KnowledgeObject, hub []model.KnowledgeObject) *DuplicateError {
	kws := distill.Keywords(o.Body)
	title := distill.TitleWords(headingOf(o))
	for _, h := range hub {
		if h.Type != o.Type || h.ID == "" || dead(h.EffectiveStatus) {
			continue
		}
		if hk := distill.Keywords(h.Body); distill.SimilarKeywords(kws, hk) {
			return &DuplicateError{ID: h.ID, Path: h.Path,
				Why: "its keywords overlap this record's (" + strings.Join(kws, ", ") + ")"}
		}
		if distill.SimilarKeywords(title, distill.TitleWords(headingOf(h))) {
			return &DuplicateError{ID: h.ID, Path: h.Path,
				Why: "its title says substantially the same thing (" + headingOf(h) + ")"}
		}
	}
	return nil
}

func dead(s model.Status) bool {
	return s == model.StatusSuperseded || s == model.StatusInvalidated
}

// headingOf is the record's first markdown heading, with an "ADR-0007 — " or
// "Lesson — " lead-in dropped so two records are compared on what they SAY.
func headingOf(o model.KnowledgeObject) string {
	for _, line := range strings.Split(o.Body, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "#") {
			continue
		}
		t = strings.TrimSpace(strings.TrimLeft(t, "# "))
		for _, dash := range []string{" — ", " – ", " -- ", " - "} {
			if i := strings.Index(t, dash); i >= 0 && i < 24 {
				t = strings.TrimSpace(t[i+len(dash):])
				break
			}
		}
		return t
	}
	return o.ID
}

// danglingEdges lists `relates_to` targets the hub does not hold, sorted. These
// are reported, never refused: a citation that resolves nowhere is inert, and
// the whole reason the id is not rewritten is that the record's own prose still
// spells it — the origin stamp is what lets a hub reader chase it home.
func danglingEdges(o model.KnowledgeObject, hub []model.KnowledgeObject) []string {
	have := map[string]bool{}
	for _, h := range hub {
		if h.ID != "" {
			have[h.ID] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, e := range o.RelatesTo {
		t := knowledge.ParseEdge(e).Target
		// Only ids can dangle: `constrains:render` names a component, not a record.
		if t == "" || !looksLikeKey(t) || have[t] || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// looksLikeKey reports whether an edge target is shaped like a knowledge-object
// key (`ADR-0007`, `LESSON-read-from-reviewed-ref`) rather than a component id.
func looksLikeKey(s string) bool {
	i := strings.IndexByte(s, '-')
	if i <= 0 {
		return false
	}
	head := s[:i]
	return head == strings.ToUpper(head) && head != strings.ToLower(head)
}

// sourceCommit is the origin repo's HEAD. Read-only, and read from the ORIGIN —
// promote runs no git command against the hub, by construction: this is the only
// gitutil call in the package and its argument is opt.RepoDir.
func sourceCommit(repoDir string) string {
	return gitutil.Repo{Dir: repoDir}.HeadSHA()
}
