---
schema_version: 1
id: ADR-0037
type: decision
scope: global
status: proposed
created: 2026-08-06T01:35:29Z
relates_to:
  - constrains:modelfacts
  - informs:ADR-0021
  - informs:ADR-0012
provenance:
  commit: seed
  citation: docs/adr-0037-submodule-units
confidence: high
---

# ADR-0037 — A unit is code this repo versions; a submodule's contents are not

## Context

The detectors pruned by NAME — a fixed set (`vendor`, `node_modules`,
`third_party`, `testdata`, `build`, `dist`, `target`, `out`). That list encodes
the right intent and enforces it with the wrong mechanism: it only catches
directories someone thought to name.

Two ways past it, both observed on real repos:

- **A git submodule.** A repo vendoring an upstream library as a submodule under
  a name the list does not contain had the submodule's own internal targets —
  samples, tests, parsers — detected as this repo's units. On one repo that was
  **12 of 22 units**: more than half the inventory was upstream's code. They
  padded `doctor`'s modelling backlog forever, and PR-scoped `model-drift` could
  never fire on them anyway, because a parent PR's diff cannot touch submodule
  files.
- **Untracked scratch trees.** A stray merge-test copy of the repo and nested
  worktrees minted phantom units and, worse, *shadowed real ones* — 19 units on
  a second repo had their evidence attributed to a Dockerfile inside a scratch
  copy instead of the committed one, so the report cited a file that is not in
  the repository.

Both are the same defect: **detection asked the filesystem what exists, when the
question is what this repository versions.**

## Decision

1. **A unit is admitted only when its evidence file is tracked by this
   repository** — the Dockerfile, the defining `CMakeLists.txt`, the package
   file. Tracking is read once (`gitutil.TrackedFiles`), never per candidate.
2. **A submodule's contents therefore mint no units.** The parent's index holds
   only the gitlink, so everything inside is untracked from the parent's point
   of view, and that is exactly the right answer: the parent does not version
   that code, does not review changes to it, and cannot enforce architecture
   over it.
3. **First-party code that lives in a submodule adopts nugit itself.** It gets
   its own store and its own model, and the parent reaches it as a peer
   (ADR-0032) rather than absorbing it. A submodule is a repository boundary;
   the model should agree with git about where the boundary is.
4. **The name-based prune set stays**, demoted from boundary to fast path — it
   avoids walking large trees, but nothing depends on its completeness.
5. **Detection degrades to the old filesystem behaviour** when there is no repo,
   no git binary, or any git error. A detector that silently finds nothing is
   indistinguishable from a clean repo and worse than one that finds too much.

## Rejected

- **Union the submodule's own `ls-files` into the parent's tracked set.** It
  reinstates precisely the noise this removes, and it makes the parent's model
  claim ownership of code the parent does not version — the parent cannot
  enforce a boundary it cannot change, and `model-drift` would nag about
  elements no PR in this repo could ever add.
- **Keep extending the name-based prune list.** Endless by construction, and it
  misses the structural case entirely: a submodule at `libs/<name>` is
  indistinguishable by name from a first-party library at `libs/<name>`. Only
  git knows the difference.
- **Treat a submodule as a container in the parent's model.** Containers are
  units this repo builds and enforces edges against; a submodule is a versioned
  dependency. ADR-0034's landscape is where a relationship to something this
  repo does not own belongs.

## Consequences

- Repos vendoring by submodule see their unit inventory shrink, sometimes
  sharply. This is a correction, not a loss: the vanished units were never
  this repo's to model, and `doctor`'s orphan-component backlog gets honest.
- **`nugit deploy` and `nugit model facts` still report everything on disk, and
  intentionally so** — they are grounding for the ADR-0012 drafting agent, which
  wants to see the whole tree before deciding what belongs in the model.
  `modelfacts.Units` is the enforcement inventory and is narrower. The two
  surfaces answer different questions and will disagree on a repo with
  submodules; that is by design, and is recorded here so it is not later
  "fixed".
- Evidence paths now always name a file that exists in the repository, so a
  finding can be opened at the path it cites.
