---
schema_version: 1
id: ADR-0011
type: decision
scope: global
status: accepted
created: 2026-06-19T00:00:00Z
relates_to:
  - constrains:delta
  - constrains:beads
provenance:
  commit: integration-phase-0
confidence: high
---

# ADR-0011 — External-tool integration: single-writer-per-fact, one-way flows

## Context

nugit's value is that the *enforced* architecture and knowledge live as git text,
validated against code at PR time (intent-at-commit vs code-at-commit). But teams
keep architecture in IcePanel, plan in Linear, docs in Notion. If nugit becomes a
fourth silo, "alignment" gets worse, not better. We need integrations that give the
org reach into those tools **without** creating a second source of truth that drifts.

A capability analysis (2026-06-19) plus prior art (GitOps, Backstage, Structurizr,
log4brains) converged on one pattern. Notably, IcePanel's 2026-03 "Model as code"
launch added a programmatic `import?prune=true` (full-replace) — so IcePanel can now
be a *derived* projection, not a dead end.

## Decision

**Single-writer-per-fact, layered.** Git text is the *sole authoritative writer* for
every fact nugit enforces at commit (the C4 model in `workspace.dsl`; the knowledge
corpus under `.nugit/**`). Every external tool is either:

- an **outbound projection** — git text → published into the tool, read-mostly
  (IcePanel via `import?prune=true`; Notion via `replace_content` keyed by a stable
  `nugit_git_id`). The full-overwrite is the keystone: git deterministically
  overwrites tool state on every push, so divergence cannot accumulate.
- an **inbound proposal** — a tool edit → a *reviewed PR* editing the git text. The
  only writer into `.nugit/**` is a reviewed PR; tools never write it directly.

Never a bidirectional-authoritative two-way merge.

**One carve-out:** plan-position is *already* declared not-enforced-at-commit
(`model.PlanPosition`). For that single layer an external system (Beads file today,
Linear live tomorrow) is allowed to be the contextual source, quarantined as
"live/forecast" (the `Note` provenance string) and it **never gates a PR**.

**Identity** is owned in git: a stable handle per layer (C4 component id → tool object
id; `nugit_git_id` → Notion page id; issue id for plan) so projections update in place
instead of churning, and inbound harvests can avoid re-proposing nugit's own writes.

## Rejected

- **A tool as the source of truth (e.g. author architecture in IcePanel, nugit
  validates against it).** Rejected: external state is "as of now", not pinnable to a
  commit, so a PR can't be checked as intent-at-commit vs code-at-commit — it breaks
  the read-at-ref determinism that makes the gate meaningful.
- **Bidirectional sync.** Rejected: two-way-authoritative sync always rots
  (merge-precedence, echo loops). Backstage overwrites UI edits; Structurizr's only
  write-back (layout coords) is the cautionary tale — the moment a tool authors what
  the text also describes, you inherit a merge tax. We keep diagrams *generated*.
- **A nugit-native GUI with its own datastore.** Rejected: a second store that drifts
  from git is exactly what nugit exists to avoid. IcePanel (derived projection) is the
  GUI answer instead.

## Consequences

- Tool MCP/REST write paths are kept read-only or draft-only; any stray tool edit is
  reconciled away on the next outbound push (self-healing **iff** the push job runs).
- A new, owned transform layer per tool (e.g. `workspace.dsl` → IcePanel
  LandscapeImportData) is bespoke nugit code that must track both the DSL subset and
  the vendor schema; unstable ids cause churn.
- Orphan/deletion handling ([[0007-hard-delete-and-gc]]) must cover zombie tool
  objects and "knowledge still links a removed component".
- The differentiated work stays the PR-time delta renderer; the sync *direction* is
  the boring, battle-tested part.
