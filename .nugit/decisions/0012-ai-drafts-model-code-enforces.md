---
schema_version: 1
id: ADR-0012
type: decision
scope: global
status: accepted
created: 2026-06-23T00:00:00Z
relates_to:
  - constrains:bootstrap
  - constrains:scaffold
provenance:
  commit: ai-bootstrap-design
confidence: high
---

# ADR-0012 — Model bootstrap: AI drafts (grounded), humans ratify, code enforces

## Context

nugit must turn an arbitrary git repo into a C4 model. The bootstrap today
([[0008-brownfield-bootstrap]]) is purely deterministic: each per-language analyzer
(Go imports, CMake `target_link_libraries`, Python/TS) yields a **flat** graph where
every package/target becomes a "component" of equal status. It cannot tell a deployable
service from a library, so on a 48-service on-prem-k8s pilot monorepo it collapsed every
service and lib into a single synthetic container — structurally valid, architecturally
useless.

"Just write smarter heuristics" is a trap. Bootstrapping a model is two different acts,
and only one is mechanical:

- **Facts** — "this target builds an executable", "this Dockerfile / k8s Deployment
  exists", "module A links B". Mechanical, high-accuracy *where an extractor exists*.
- **Abstraction** — the system boundary; which units are containers vs components; how to
  group and *name* them so the model is legible. This is interpretation, and the space of
  build systems / deploy targets / monorepo conventions across "any repo" is effectively
  unbounded. Deterministic code chasing it is an infinite-detector treadmill that still
  yields mediocre groupings — because the grouping isn't in the files, it's a human
  abstraction laid over them.

Meanwhile the *enforcement* half — `c4<->code` at PR time — must stay deterministic:
same inputs → same verdict, reproducible, auditable, cheap, on every PR. An AI gate would
be flaky, costly, and non-reproducible — worthless.

## Decision

**AI proposes (grounded by deterministic fact-extraction) → a human ratifies → code
enforces.** The committed `workspace.dsl` (and `.nugit/**`) is the hand-off artifact
between the three.

- **Bootstrap = a grounded agent.** `nugit init` / a `nugit model` command runs nugit's
  deterministic extractors first to produce *ground-truth facts* — the verified
  dependency graph plus deploy signals (k8s workloads, Dockerfiles, executables vs
  libraries) — then has an agent reason over those + the repo to draft a *leveled* model
  (systems / containers / components, sensibly grouped and named) **constrained to nugit's
  schema**. Containers are derived from deploy artifacts; components from the libraries
  they are built from.
- **The agent is bounded by facts + schema + review.** It does not invent dependency
  edges (it is handed the real graph); its output is typed to the schema; it lands as a
  *reviewed PR* in warn mode — consistent with single-writer-per-fact
  ([[0011-external-tool-integration-single-writer]]): the only writer into the enforced
  text is a reviewed PR.
- **Enforcement stays deterministic.** The extractors do double duty — they ground the AI
  bootstrap *and* power the `c4<->code` gate. The agent is confined to the interpretive,
  one-time bootstrap; it never runs in the PR gate.
- **Two reaches, not one.** *Bootstrap reach* is broad — an agent can read any repo.
  *Enforcement reach* is bounded by deterministic extractors (Go / CMake / Python / TS
  today). A repo in an unsupported ecosystem can still get an AI-drafted model, but without
  an extractor it is documentation that can drift — not an enforced contract.

Bootstrap accuracy is **amortized**: the model is durable, human-editable text (in
Obsidian / Structurizr / the DSL), produced once and refined — so the bar is "a good,
legible draft that's easy to fix", not "perfect". That bar favours a grounded agent over
elaborate heuristics, on both accuracy and maintenance cost.

## Rejected

- **Pure deterministic bootstrap for any repo.** Extracts facts but not the abstraction;
  an unbounded-convention treadmill that still yields mediocre, illegible models. The pilot-model
  flattening is the demonstration.
- **AI in the enforcement gate.** Non-deterministic, expensive, non-reproducible — it
  destroys the property (same inputs → same verdict) that makes the gate worth having.
  Determinism at enforcement is non-negotiable.
- **Fully manual modelling only.** Doesn't scale to a 48-service monorepo and is the
  friction we set out to remove; even today's deterministic-heuristic draft beats a blank
  file. The agent raises the floor; the human keeps the pen.

## Consequences

- A new agent layer is needed in `bootstrap` / `scaffold` (or a `nugit model` command):
  input = repo + the extractor facts; output = schema-valid `workspace.dsl`; surfaced as a
  PR. nugit already has the pieces — extractors, the typed schema, MCP, and the
  warn→enforce ratification gate.
- A **deployable-detector** family (k8s / Docker / executable-vs-library), mirroring the
  per-language analyzers, supplies the container-vs-component signal the agent grounds on.
- Mapping a deploy artifact (Dockerfile / k8s manifest) back to its source dir(s) is fuzzy
  (image names, build args, COPY paths) — a known hard part to validate per ecosystem
  before trusting it.
- Enforcement may need to span container + component levels (today `c4<->code` is
  component-level only) — a real model change, tracked separately.
- The agent's cost and non-determinism are confined to bootstrap and gated by human
  review, so they never threaten the PR gate's determinism or budget
  ([[0006-per-pr-cost-budget]]).
