---
schema_version: 1
id: ADR-0020
type: decision
scope: global
status: proposed
created: 2026-08-01T00:00:00Z
relates_to:
  - amends:ADR-0002
  - constrains:knowledge
  - constrains:retrieval
  - constrains:consistency
provenance:
  commit: seed
  citation: "feat/applies-to-paths"
confidence: high
---

# ADR-0020 — `applies_to_paths`: knowledge binds directly to paths without a component

## Context

ADR-0002 made the file→component binding the single relevance primitive:
knowledge reaches a path only through the C4 component that owns it (scope,
`constrains:` edges), or through global scope gated on task-keyword match.
That works exactly as far as the C4 model reaches — and the pilot showed how
far it doesn't:

- **189 of the last 600 pilot commits touch ONLY infra paths** (`helm/`,
  `k8s/`, `docker/`, `scripts/`, `.github/`, `third_party/`, `cmake/`), and
  the C4 model maps almost none of that surface. A C4 model is a model of the
  *architecture*; these files are real governed surface but not components.
- **4 of 21 logged retrievals resolved `component: ""`** and degraded to
  global-only bundles — the scope chain had nothing to hang knowledge on.
- The **pilot preview-freeze root cause lived in `third_party/versions.env`**
  — a file that the pilot's own ADR-0020 (MoQ draft lock, `scope: global`) is
  ENTIRELY about — yet nothing connected the file to the ADR: global scope
  only surfaces on task-keyword match, and the task wording didn't happen to
  hit. Same shape for `k8s/registry-local/configmap.yaml` (the registry-retention
  recurrence): the lesson existed; the file couldn't summon it.

Inventing pseudo-components for every infra file fights the model (fake
elements with no edges pollute the diagram, the C4 delta, and the c4↔code
check). The gap is that knowledge has no way to say "I am about *this file*"
without a component in between.

**Why `amends:` and not `supersedes:`** (ADR-0015 semantics): ADR-0002's
primitive — components bind to files via `properties { paths }` globs,
resolution is total, most-specific-wins, orphans surfaced — stands unchanged
and remains the primary binding; every delta and check keeps using it. This
decision overrides only ADR-0002's implicit corollary that a path reaches
knowledge *exclusively* through its owning component. Partial override of a
live decision is exactly the `amends:` edge.

## Decision

1. **New optional front-matter field on any knowledge object:**

   ```yaml
   applies_to_paths:
     - "third_party/versions.env"
     - "k8s/registry-local/**"
   ```

   Repo-relative doublestar globs — the same dialect as ADR-0002's
   `properties { paths }`. Parsed in `internal/knowledge`, typed in
   `internal/model.FrontMatter`.

2. **Retrieval treats a matched path as component scope.** When the queried
   path matches any of an object's `applies_to_paths` globs, `context(path)`
   includes the object as if it were component-scoped, regardless of
   `scope:`: it bypasses the keyword gate on global decisions, is eligible
   even when its scope names some other component, and ranks with
   component-scoped items (`scopeRank` 0). The binding substitutes for
   *scope*, not for *task relevance* — path-bound lessons and references
   still pass the same task-keyword filter component-scoped ones do. Matched
   items are marked `path_bound` in the bundle (JSON and markdown), never
   silently privileged.

3. **`stale-knowledge` sees the binding.** A PR touching a file matched by a
   superseded/invalidated object's `applies_to_paths` counts as touching that
   object's governed surface, exactly as touching a governed component does —
   no component resolution required.

4. **Evidence tiers count the binding — honestly.** An object with at least
   one `applies_to_paths` glob is *bound to code*, so it derives at least
   `checked`, never `declared`. It never derives `enforced`: a direct path
   binding is verified only by the warn-severity stale-knowledge check, never
   by a fail-severity edge check, so an object declaring `applies_to_paths`
   caps at `checked` (mirroring the existing rule that a partially-bound
   component set is `checked`, not `enforced`).

5. **Invalid globs are reported, never silently dropped** — the
   `mapping.InvalidPatterns()` / model-health discipline: a syntactically
   invalid glob matches nothing, and `knowledge.InvalidAppliesGlobs` feeds a
   warn-severity model-health finding naming the object and the glob.

## Rejected

- **Pseudo-components for infra surfaces** (a `third_party` component, a
  `helm` component…). Fights the C4 model: elements with no relationships and
  no architectural meaning pollute the diagram, the C4 delta, and the c4↔code
  check, and every new infra file demands more fake modeling. ADR-0002
  rejected implicit component inference for the same reason: the model is
  logical, not a file index.
- **Binding in workspace.dsl instead of the object** (e.g. a model-level
  properties table mapping globs to knowledge ids). The model would have to
  name knowledge ids it otherwise never references, and the binding would
  drift from the object it annotates — ADR-0002 chose co-location for exactly
  this reason; here the knowledge object is the thing the binding annotates,
  so the binding lives in its front-matter.
- **Widening `scope:` to accept globs.** One field, two value spaces: `scope:
  helm` is ambiguous between a component id and a directory, and every
  consumer of `scope` would need to disambiguate. A separate field keeps
  `scope` a C4 element reference, as everywhere else.
- **Better keyword matching instead of a binding.** That file was
  *entirely* covered by an ADR and keywords still missed it — keyword match
  is opportunistic; a declared binding is deterministic, and determinism is
  the retrieval contract (ADR-0004, ADR-0006).
- **Deriving the binding from `provenance.citation` or prose mentions.**
  Provenance records where knowledge came from, not what it governs;
  extracting globs from prose is implicit inference, unexplainable when it
  mis-fires.

## Consequences

- A global ADR that is *about a file* now surfaces whenever that file is
  queried or touched — both shapes stop depending on lucky task
  wording. The 4/21 `component: ""` retrievals get a non-global lane.
- Declaring `applies_to_paths` on an object whose components are fully
  enforced downgrades its tier from `enforced` to `checked`. That is the
  honest reading (part of its governed surface is now only warn-checked), and
  it is documented here rather than discovered in a diff.
- Overbroad globs (`**`) would make an object effectively global at rank 0.
  Review holds the line for now; a model-health nudge for pathological globs
  is a possible follow-up.
- Follow-ups (not in this slice): doctor store-health counting path-bound
  coverage; surfacing the field in the Obsidian/Notion projections; a
  `pr-render` knowledge-delta annotation when a binding changes.
