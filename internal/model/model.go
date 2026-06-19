// Package model holds the shared, dependency-free types for the nugit engine.
//
// These types are the contract every other internal package is written
// against. Keep this package free of imports from sibling internal packages
// so it can never participate in an import cycle.
package model

import "time"

// Kind enumerates the durable knowledge types. The c4 model is intentionally
// NOT a content-addressed KnowledgeObject (it is a single DSL file); see
// .nugit/decisions/0002-file-to-component-binding.md.
type Kind string

const (
	KindLesson   Kind = "lesson"
	KindDecision Kind = "decision"
	KindSpec     Kind = "spec"
	KindGlossary Kind = "glossary"
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
	// Rejected is the extracted "Rejected" rationale (decisions/lessons), the
	// anti-hallucination field. Computed once at parse time so the renderer never
	// re-implements the extraction.
	Rejected string
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
	Affects  []string // C4 component ids
	Spec     string
	Keywords []string
	// Extra captures any other key: value trailer lines verbatim.
	Extra map[string]string
}

// Has reports whether the trailer carried any nugit fields at all.
func (t Trailer) Has() bool {
	return t.Decision != "" || t.Rejected != "" || t.Learned != "" ||
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

// HasRelationship reports whether the model contains a src->dst edge.
func (m Model) HasRelationship(src, dst string) bool {
	for _, r := range m.Relationships {
		if r.Src == src && r.Dst == dst {
			return true
		}
	}
	return false
}

// ---- Deltas (all computed deterministically) ----

// C4Delta is the structural diff of workspace.dsl between base and head.
type C4Delta struct {
	AddedComponents   []Component
	RemovedComponents []Component
	ChangedComponents []ComponentChange
	AddedRels         []Relationship
	RemovedRels       []Relationship
	ChangedRels       []RelationshipChange
	// RawDiff is the unified text diff of the DSL file (may be empty).
	RawDiff string
}

func (d C4Delta) Empty() bool {
	return len(d.AddedComponents) == 0 && len(d.RemovedComponents) == 0 &&
		len(d.ChangedComponents) == 0 && len(d.AddedRels) == 0 &&
		len(d.RemovedRels) == 0 && len(d.ChangedRels) == 0
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

// ---- Top-level render input ----

// Report is the fully-computed input to the renderer: everything deterministic,
// assembled before any prose is generated.
type Report struct {
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
	// HeadModel feeds the in-comment C4 diagram only; kept out of the structured
	// JSON contract (it would leak every component's path globs to reviewer agents).
	HeadModel Model `json:"-"`
}
