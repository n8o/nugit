// Package model holds the shared, dependency-free types for the nugit engine.
//
// These types are the contract every other internal package is written
// against. Keep this package free of imports from sibling internal packages
// so it can never participate in an import cycle.
package model

import (
	"fmt"
	"time"
)

// Kind enumerates the durable knowledge types. The c4 model is intentionally
// NOT a content-addressed KnowledgeObject (it is a single DSL file); see
// .nugit/decisions/0002-file-to-component-binding.md.
type Kind string

const (
	KindLesson   Kind = "lesson"
	KindDecision Kind = "decision"
	KindSpec     Kind = "spec"
	KindGlossary Kind = "glossary"
	// KindReference is distilled EXTERNAL knowledge (papers, vendor docs, specs,
	// benchmarks) scoped and keyworded for retrieval. The body carries the claims
	// relevant to this project — never the full document; provenance.citation and
	// the `source` front-matter link out. Enters via reviewed PR (ADR-0011's
	// inbound-proposal path); see .nugit/decisions/0014-reference-type.md.
	KindReference Kind = "reference"
)

// Status is the lifecycle marker carried in a knowledge object's front-matter.
//
// NOTE (resolves the review's immutability contradiction): a record's file is
// immutable and content-addressed. The *effective* status of a record is
// derived at read time from the supersedes graph — a record is Superseded iff
// some other record names it in `supersedes:`. The Status field in front-matter
// is therefore an authored hint for the record's own initial state, never
// mutated in place. See .nugit/decisions/0003-supersede-without-mutation.md.
type Status string

const (
	StatusProposed    Status = "proposed"
	StatusAccepted    Status = "accepted"
	StatusActive      Status = "active"
	StatusSuperseded  Status = "superseded"
	StatusInvalidated Status = "invalidated"
)

// Evidence is the derived trust tier of a knowledge object — how much of it
// nugit mechanically verifies. Derived at read time (never authored, never
// persisted) from the object's status plus the governance substrate: whether
// the components it governs are path-bound in the model and whether the edge
// checks enforce them. "Enforced" claims the SUBSTRATE is verified, not the
// object's prose constraint. Tier strings never carry path globs, so they are
// safe in structured JSON output.
type Evidence string

const (
	// EvidenceEnforced: every governed component is path-bound and the
	// architecture edges touching them are mechanically enforced (fail
	// severity) at PR time.
	EvidenceEnforced Evidence = "enforced"
	// EvidenceChecked: at least one governed component is path-bound (or the
	// object binds paths directly via applies_to_paths, ADR-0020), but the
	// binding is not fully edge-enforced (warn mode, no backend, structural
	// model, partial binding, or a direct path binding — which only the
	// warn-severity stale-knowledge check verifies).
	EvidenceChecked Evidence = "checked"
	// EvidenceDeclared: written down; nothing binds it to code.
	EvidenceDeclared Evidence = "declared"
	// EvidenceProposed: candidate lane (ADR-0016), awaiting ratification.
	EvidenceProposed Evidence = "proposed"
	// EvidenceStale: superseded or invalidated.
	EvidenceStale Evidence = "stale"
)

// Confidence is a coarse self-rating attached to a knowledge object.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"
	ConfidenceMedium Confidence = "medium"
	ConfidenceLow    Confidence = "low"
)

// Provenance records where a knowledge object came from.
type Provenance struct {
	Commit   string `yaml:"commit"`
	Agent    string `yaml:"agent,omitempty"`
	Citation string `yaml:"citation,omitempty"`
}

// FrontMatter is the common YAML header every durable knowledge object begins
// with (§4.3 of the architecture spec, with SchemaVersion added per the
// format-freeze decision .nugit/decisions/0001-schema-versioning.md).
type FrontMatter struct {
	SchemaVersion int        `yaml:"schema_version"`
	ID            string     `yaml:"id"`
	Type          Kind       `yaml:"type"`
	Scope         string     `yaml:"scope"`
	Status        Status     `yaml:"status"`
	Created       time.Time  `yaml:"created"`
	Supersedes    string     `yaml:"supersedes,omitempty"`
	RelatesTo     []string   `yaml:"relates_to,omitempty"`
	Provenance    Provenance `yaml:"provenance"`
	Confidence    Confidence `yaml:"confidence,omitempty"`
	// Source is the external origin of a reference object (URL/DOI/standard id).
	// The body holds distilled claims; Source links to the full document.
	Source string `yaml:"source,omitempty"`
	// AppliesToPaths optionally binds the object DIRECTLY to repo paths
	// (doublestar globs, repo-relative — the ADR-0002 dialect), with no C4
	// component in between (ADR-0020). Retrieval includes the object for a
	// matching queried path as if it were component-scoped, and the
	// stale-knowledge check treats a PR touching a matched path as touching
	// the object's governed surface.
	AppliesToPaths []string `yaml:"applies_to_paths,omitempty"`
}

// KnowledgeObject is one parsed durable record (a markdown file under .nugit/**).
type KnowledgeObject struct {
	FrontMatter
	// Path is the repo-relative path of the record file.
	Path string
	// Body is the markdown after the front-matter block.
	Body string
	// EffectiveStatus is computed at load time from the supersedes graph; it
	// overrides FrontMatter.Status when a newer record supersedes this one.
	EffectiveStatus Status
	// AmendedBy is computed at load time from reverse `amends:` edges: ids of
	// records that override PART of this one. The record stays live (unlike
	// supersession) but must be read together with its amendments (ADR-0015).
	AmendedBy []string
	// ReinforcedBy is computed at load time from reverse `reinforces:` edges:
	// ids of records that re-confirm this one after a recurrence and widen its
	// retrieval surface (ADR-0019). Purely additive — the target's status is
	// untouched (unlike supersession) and nothing is overridden (unlike amends).
	ReinforcedBy []string
	// Rejected is the extracted "Rejected" rationale (decisions/lessons), the
	// anti-hallucination field. Computed once at parse time so the renderer never
	// re-implements the extraction.
	Rejected string
	// Evidence is the derived trust tier (see the Evidence type). Populated by
	// evidence.Annotate; empty when no derivation ran (e.g. untyped objects).
	Evidence Evidence
}

// Edge is a parsed typed cross-reference from a relates_to entry, e.g.
// "constrains:payments.charge" -> {Relation: "constrains", Target: "payments.charge"}.
type Edge struct {
	Relation string
	Target   string
	Raw      string
}

// Trailer is the parsed nugit commit-trailer block (§6.1).
type Trailer struct {
	Decision string
	Rejected string
	Learned  string
	// Symptom is the observable behavior that led to the change — what someone
	// SAW, not what was done about it. Optional (ADR-0028): it is the only
	// reliable source for a lesson's Trigger, because a symptom is frequently
	// nowhere in the commit at all.
	Symptom  string
	Affects  []string // C4 component ids
	Spec     string
	Keywords []string
	// Extra captures any other key: value trailer lines verbatim.
	Extra map[string]string
}

// Has reports whether the trailer carried any nugit fields at all.
func (t Trailer) Has() bool {
	return t.Decision != "" || t.Rejected != "" || t.Learned != "" || t.Symptom != "" ||
		len(t.Affects) > 0 || t.Spec != "" || len(t.Keywords) > 0 || len(t.Extra) > 0
}

// Commit is a single commit in the reviewed range.
type Commit struct {
	SHA     string
	Subject string
	Body    string
	Trailer Trailer
}

// Component is one C4 component parsed from workspace.dsl.
type Component struct {
	// ID is the DSL identifier used as the affects:/constrains: link target,
	// e.g. "render" or "payments.charge".
	ID string
	// Name is the human label.
	Name string
	// Paths are the repo-relative globs that bind this logical component to
	// physical files (the review's missing primitive). Sourced from the DSL
	// `properties { paths "internal/render/**" }` block.
	Paths []string
	Tags  []string
	Tech  string
	// Container is the id of the parent container this component is declared
	// inside, or "" for a flat (container-less) model.
	Container string `json:",omitempty"`
}

// Container is one C4 container parsed from workspace.dsl — the level between
// system and component. The parent system is deliberately not tracked: nugit
// models a single-system subset, so containers are the top grouping level.
type Container struct {
	ID    string
	Name  string
	Paths []string
	Tags  []string
	Tech  string
}

// Relationship is a directed dependency between two components in the model.
type Relationship struct {
	Src  string
	Dst  string
	Desc string
}

// Model is the parsed C4 workspace (the subset nugit needs).
type Model struct {
	Name          string
	Components    []Component
	Containers    []Container
	Relationships []Relationship
	// Properties are model-level key/values (a Structurizr `properties` block).
	Properties map[string]string
}

// Structural reports whether this model came from the directory layout (no
// language analysis), in which case the c4<->code import check must stay silent —
// the model deliberately declares no relationships, so a clean run is not an
// architecture guarantee. Persisted as a model property by `nugit init`.
func (m Model) Structural() bool { return m.Properties["nugit_structural"] == "true" }

// Comp returns the component with the given id, if present.
func (m Model) Comp(id string) (Component, bool) {
	for _, c := range m.Components {
		if c.ID == id {
			return c, true
		}
	}
	return Component{}, false
}

// Container returns the container with the given id, if present.
func (m Model) Container(id string) (Container, bool) {
	for _, ct := range m.Containers {
		if ct.ID == id {
			return ct, true
		}
	}
	return Container{}, false
}

// ContainerOf returns the parent container id of the component with the given
// id, or "" otherwise (unknown id, flat component, or id naming a container).
func (m Model) ContainerOf(id string) string {
	for _, c := range m.Components {
		if c.ID == id {
			return c.Container
		}
	}
	return ""
}

// SameLineage reports whether a and b are the same element or one is the
// other's parent container. Containment is never a flaggable dependency.
func (m Model) SameLineage(a, b string) bool {
	return a == b || m.ContainerOf(a) == b && b != "" || m.ContainerOf(b) == a && a != ""
}

// HasRelationship reports whether the model contains a src->dst edge.
func (m Model) HasRelationship(src, dst string) bool {
	for _, r := range m.Relationships {
		if r.Src == src && r.Dst == dst {
			return true
		}
	}
	return false
}

// Covered reports whether a code-level src→dst dependency is legalized by the
// model under two-level roll-up semantics (ADR-0017). Let A/B be the container
// scopes of src/dst (an id that IS a container is its own scope):
//
//   - same lineage (containment) is never flagged;
//   - within one container (A == B, non-empty), only a literal component edge
//     covers — a container edge NEVER covers intra-container pairs;
//   - across containers (or mixed levels), any declared edge over
//     {src, A} × {dst, B} covers: component edge, container edge (roll-up),
//     or mixed.
//
// Flat models (A == B == "") reduce exactly to HasRelationship.
func (m Model) Covered(src, dst string) bool {
	if m.SameLineage(src, dst) {
		return true
	}
	a, b := m.scopeOf(src), m.scopeOf(dst)
	if a != "" && a == b {
		return m.HasRelationship(src, dst)
	}
	srcs := []string{src}
	if a != "" && a != src {
		srcs = append(srcs, a)
	}
	dsts := []string{dst}
	if b != "" && b != dst {
		dsts = append(dsts, b)
	}
	for _, s := range srcs {
		for _, d := range dsts {
			if m.HasRelationship(s, d) {
				return true
			}
		}
	}
	return false
}

// scopeOf returns the container scope of an id: the id itself when it names a
// container, its parent container when it names a component, "" otherwise.
func (m Model) scopeOf(id string) string {
	if _, ok := m.Container(id); ok {
		return id
	}
	return m.ContainerOf(id)
}

// ---- Deltas (all computed deterministically) ----

// C4Delta is the structural diff of workspace.dsl between base and head.
type C4Delta struct {
	AddedComponents   []Component
	RemovedComponents []Component
	ChangedComponents []ComponentChange
	AddedContainers   []Container       `json:",omitempty"`
	RemovedContainers []Container       `json:",omitempty"`
	ChangedContainers []ContainerChange `json:",omitempty"`
	AddedRels         []Relationship
	RemovedRels       []Relationship
	ChangedRels       []RelationshipChange
	// RawDiff is the unified text diff of the DSL file (may be empty).
	RawDiff string
}

func (d C4Delta) Empty() bool {
	return len(d.AddedComponents) == 0 && len(d.RemovedComponents) == 0 &&
		len(d.ChangedComponents) == 0 && len(d.AddedContainers) == 0 &&
		len(d.RemovedContainers) == 0 && len(d.ChangedContainers) == 0 &&
		len(d.AddedRels) == 0 && len(d.RemovedRels) == 0 && len(d.ChangedRels) == 0
}

// RelationshipChange records an edge whose metadata (description) changed while
// its endpoints stayed the same.
type RelationshipChange struct {
	Before Relationship
	After  Relationship
}

// ComponentChange records a non-structural change to a component (e.g. paths/tech).
type ComponentChange struct {
	Before Component
	After  Component
	Fields []string // names of fields that changed
}

// ContainerChange records a non-structural change to a container (e.g. paths/tech).
type ContainerChange struct {
	Before Container
	After  Container
	Fields []string // names of fields that changed
}

// FileChange is one changed file in the code delta.
type FileChange struct {
	Path      string
	Added     int
	Deleted   int
	Status    string // A, M, D, R...
	Component string // resolved C4 component id, or "" if unmapped
}

// CodeDelta is the standard diff summary grouped by C4 component.
type CodeDelta struct {
	Files    []FileChange
	ByComp   map[string][]FileChange // component id -> files (key "" = unmapped)
	TotalAdd int
	TotalDel int
}

// KnowledgeChange is one added/changed/removed knowledge object in the PR.
type KnowledgeChange struct {
	Path   string
	Status string // A, M, D
	Object *KnowledgeObject
}

// KnowledgeDelta groups knowledge objects touched by the PR.
type KnowledgeDelta struct {
	Changes []KnowledgeChange
}

func (d KnowledgeDelta) Empty() bool { return len(d.Changes) == 0 }

// PlanPosition is the (optional, Beads-deferred) plan delta. In the keystone it
// is sourced from a simple committed plan file, if present.
type PlanPosition struct {
	Present   bool
	Completed []string
	Current   string
	Remaining []string
	Note      string
	// Diff vs the base ref (what moved), populated by delta.DiffPlan.
	NewlyCompleted []string
	NewlyStarted   []string
	AddedItems     []string
	RemovedItems   []string
	Regressed      []string // were done at base, no longer done at head (reopened)
}

// Changed reports whether the plan moved between base and head.
func (p PlanPosition) Changed() bool {
	return len(p.NewlyCompleted) > 0 || len(p.NewlyStarted) > 0 ||
		len(p.AddedItems) > 0 || len(p.RemovedItems) > 0 || len(p.Regressed) > 0
}

// ---- Cross-artifact consistency ----

// Severity ranks a consistency finding.
type Severity string

const (
	SevFail Severity = "fail"
	SevWarn Severity = "warn"
	SevInfo Severity = "info"
)

// Finding is one cross-artifact consistency result (§9.3).
type Finding struct {
	Check    string
	Severity Severity
	Title    string
	Detail   string
}

// ---- Significance ----

// Tier is the significance class that drives progressive disclosure (§9.4).
type Tier string

const (
	TierTrivial       Tier = "trivial"
	TierFeature       Tier = "feature"
	TierArchitectural Tier = "architectural"
)

// Significance is the classifier output with its reasons (always explainable).
type Significance struct {
	Tier    Tier
	Reasons []string
}

// ---- PR-time distill proposals (ADR-0018) ----

// Proposal is one distillable commit trailer surfaced at PR time as a
// ready-to-apply knowledge candidate: exactly what `nugit distill` would write
// into the candidate lane (`status: proposed`, ADR-0016) for this range.
// Computed deterministically from the PR's own commits; rendering never writes.
type Proposal struct {
	Kind     Kind     // decision | lesson
	Title    string   // first line of the trailer text (truncated)
	Text     string   // the full decision:/learned: trailer text
	Rejected string   `json:",omitempty"`
	Scope    string   // affects-derived scope: the single component, else global
	Keywords []string `json:",omitempty"`
	Commit   string   // short source-commit SHA
	Subject  string   // source-commit subject line (the trigger)
}

// ---- Top-level render input ----

// EnforcementDowngrade records that an explicit CLI -fail-on weakened the
// config-declared pr_render.fail_on policy (ADR-0026: enforcement knobs must
// not silently cancel). Presence means "this run is weaker than the repo
// declared". The exit-code policy still follows the flag — the downgrade is
// made visible, never overridden.
type EnforcementDowngrade struct {
	// Notice is the canonical one-line sentence every output format leads with.
	Notice          string
	ConfigFailOn    string // pr_render.fail_on declared in config.yml at head
	EffectiveFailOn string // the weaker -fail-on this run actually applied
}

// NewEnforcementDowngrade builds the record with its canonical notice line, so
// markdown, check-run, and structured JSON all carry the identical sentence.
func NewEnforcementDowngrade(configFailOn, effective string) *EnforcementDowngrade {
	return &EnforcementDowngrade{
		Notice: fmt.Sprintf("enforcement downgraded by flag: config says %s, running with %s",
			configFailOn, effective),
		ConfigFailOn:    configFailOn,
		EffectiveFailOn: effective,
	}
}

// Report is the fully-computed input to the renderer: everything deterministic,
// assembled before any prose is generated.
type Report struct {
	// Enforcement is non-nil only when an explicit -fail-on downgraded the
	// config-declared policy (ADR-0026). Declared first so the notice leads the
	// structured JSON; omitempty keeps undowngraded output byte-identical.
	Enforcement  *EnforcementDowngrade `json:",omitempty"`
	BaseRef      string
	HeadRef      string
	Commits      []Commit
	C4           C4Delta
	Code         CodeDelta
	Knowledge    KnowledgeDelta
	Plan         PlanPosition
	Findings     []Finding
	Significance Significance
	Narrative    string // optional opt-in LLM prose; "" on the deterministic path
	// Proposals is the PR-time distill candidate set (ADR-0018): trailers in
	// this range that `nugit distill` would promote into the candidate lane.
	Proposals []Proposal `json:",omitempty"`
	// ProposalsDeduped counts candidates suppressed because the store already
	// carries an equivalent object (exact text or keyword overlap).
	ProposalsDeduped int `json:",omitempty"`
	// HeadModel feeds the in-comment C4 diagram only; kept out of the structured
	// JSON contract (it would leak every component's path globs to reviewer agents).
	HeadModel Model `json:"-"`
}
