---
schema_version: 1
id: ADR-0021
type: decision
scope: global
status: accepted
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:consistency
  - constrains:modelfacts
  - informs:ADR-0012
provenance:
  commit: seed
  citation: "feat/model-drift-check"
confidence: high
---

# ADR-0021 — Model drift becomes a check, not a ritual

## Context

ADR-0012 split modeling into facts (deterministic detectors), abstraction (a
grounded agent drafts `workspace.dsl` via the nugit-model skill), and
enforcement (deterministic PR checks). What it left implicit is *when the
abstraction gets re-run* — and the pilot shows the answer "when a human
remembers to invoke the skill" does not hold up:

- **11 real code units are absent from the pilot's `workspace.dsl`**: 3
  deployable services and 8 libs.
- **Five of the missing C++ libs are `add_subdirectory()`'d from the root
  CMakeLists.txt** — squarely inside nugit's own CMake detector's field of
  view. Three of them landed *before* the last manual model refresh (pilot
  dadf2b42, 2026-07-16) and were still missed, because refresh is a manual
  agent ritual: the skill re-derives whatever it is pointed at, and nothing
  points it at anything on a schedule.
- **A Python shared lib went unmodeled for 3 months** and caused a logged
  retrieval miss — `component: ""` on a path named in a lesson's keywords, so
  scoped knowledge that existed was never served for the code it governed.
- The pilot's own commit trailer says it plainly (pilot 21d2de01): "model
  coverage decays with normal development… a periodic facts-vs-DSL diff is
  cheap and catches it."

The asymmetry is the point: the facts half of ADR-0012 is already sitting in
the repo (`nugit model facts` — the `internal/modelfacts` grounding bundle
over `internal/deploy` + `internal/cmake`), and the enforced model is parsed
on every pr-render. The diff between them is a set operation — exactly the
kind of check `internal/consistency` exists for. Only nobody computes it.

## Decision

**A `model-drift` consistency check (warn severity), plus a full-scan advisory
twin in `nugit doctor` — one core, two surfaces.**

1. **Unit inventory** (`modelfacts.Units`): the deterministic
   buildable/deployable units of the working tree — deployable containers from
   `internal/deploy` (SPEC-003), CMake target dirs from `internal/cmake`, and
   Go package dirs (Go is an enforced backend; this also mechanizes AGENTS.md's
   own "new package → add a component" rule on nugit itself). Deduped by
   directory; deploy evidence wins.
2. **Drift = a detected unit with no corresponding model element**: no
   component/container path glob maps its directory (`mapping.ResolveDir`, same
   prefix bridging as every other check) *and* no element id or name matches
   the unit's name (an element that exists but lacks `paths` is a binding gap,
   not absence — model-health territory, not double-reported here).
3. **In pr-render, the check is PR-scoped**: it fires only for a drifted unit
   whose directory this PR touches, and only when none of the touched files
   under it resolve to any element (the pilot's retrieval-miss signature:
   `component: ""`). The finding is a **warn** naming the unit, its detector
   evidence, and the two remediations: run the nugit-model skill, or add a DSL
   stub with `properties { paths "<dir>/**" }`.
4. **In `nugit doctor`, the twin is a full-repo scan, advisory** (never gates
   the exit code): the "periodic facts-vs-DSL diff" the pilot trailer asked
   for, on the surface ADR-0010 designated for whole-repo pre-flight.
5. **The check never feeds the significance verdict** (`IsUndeclaredEdge`
   excludes it): drift is model hygiene, not an architectural change in the PR.

Why touched-dirs-only at PR time *and* a doctor full scan, rather than one or
the other: a full-repo warn on every PR bills the whole modeling backlog to
whoever touches the repo next — on a pilot 11 units deep, that is eleven warns
on an unrelated one-line fix, and warns that read as wallpaper get muted. The
PR check nags the person with context, at the moment they have it (the same
touched-scope rule `flagDirEdges` already applies to edges); the backlog view
stays where backlogs belong. Detectors read the working tree (== head in CI),
the precedent set by the Python/TS checks; the diff stays deterministic and
LLM-free (ADR-0006).

## Rejected

- **Keep refresh a ritual (status quo, better docs).** The pilot ran the ritual
  and still missed three detector-visible libs that predated it. Coverage decay
  is continuous; rituals are punctual. Documentation does not page anyone.
- **Full-scan warn on every PR.** Charges the whole backlog to every PR; the
  noise trains people to ignore the one warn that is theirs.
- **Fail severity.** The remediation is a model refresh — an interpretive,
  human-ratified act (ADR-0012). Blocking a code PR on modeling debt its author
  did not create inverts responsibility. Start at warn; promote once the corpus
  is dense (the AGENTS.md spec-linkage precedent).
- **Auto-generate the missing DSL stub.** Writes into the enforced text with no
  review — violates single-writer-via-reviewed-PR (ADR-0011) and the
  AI-drafts/human-ratifies split (ADR-0012). The check names the unit and hands
  the pen to the skill.
- **Diff at the reviewed ref instead of the working tree.** The detectors are
  filesystem walks; materializing tree snapshots per ref costs more than the
  signal is worth, and the working-tree precedent (python<->code, ts<->code)
  has held.

## Consequences

- `internal/modelfacts` grows the `Units`/`Unmodeled` inventory (usable later
  to enrich the grounding bundle itself); `internal/consistency` gains
  `model-drift`; `nugit doctor` gains an advisory "model covers detected
  units" probe. Three extra pruned filesystem walks per pr-render — bounded,
  deterministic, offline.
- **Known detector blind spots, filed by the pilot (documented here, not
  implemented):**
  - *By-path source reuse.* A service that compiles another app's `.cpp`
    directly (`add_executable(foo ../other_svc/src/x.cpp)`) emits no
    `target_link_libraries` edge, so the facts carry no dependency edge for it.
    Drift detection sees unit *existence*, not these edges — such a coupling
    still needs modeling (and eventually detecting) by hand.
  - *Python/Rust lib roots.* The inventory has no Python or Rust unit detector,
    so a Python lib root without CMake — exactly the lib behind the pilot's
    3-month retrieval miss — remains invisible until a
    requirements/pyproject/Cargo walk exists. This check shrinks the drift
    window for CMake/deploy/Go units; it does not close it for every ecosystem
    (ADR-0012's "enforcement reach is bounded by extractors" applies verbatim).
- A unit whose element carries a matching name but no `paths` passes this check
  and stays invisible to retrieval — model-health's missing-binding warnings,
  not drift, are the right home for tightening that.
- On nugit's own repo the Go unit inventory turns AGENTS.md's "new package →
  add a component" convention into a mechanical warn.
- If warn-fatigue proves the wrong default anyway, a `drift.mode` config knob
  (off/warn) is the escape hatch — cheap to add, not needed yet.
