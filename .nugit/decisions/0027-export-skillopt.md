---
schema_version: 1
id: ADR-0027
type: decision
scope: global
status: proposed
created: 2026-08-03T00:00:00Z
relates_to:
  - constrains:knowledge
  - informs:ADR-0011
  - informs:ADR-0013
provenance:
  commit: seed
  citation: "feat/export-skillopt"
confidence: high
---

# ADR-0027 — Export the knowledge store as skill-optimizer eval cases

## Context

A skill-optimization benchmark (SkillOpt, `microsoft/SkillOpt`) was built for a
pilot repo's incident-diagnosis domain: 102 cases (61 train / 21 val / 20 test)
as JSONL. Every case was **hand-mined once** from issues, PRs and project memory
files, with **no regeneration path** — so the corpus froze on its mining date and
the next month's incidents never entered it. Two optimizer runs (21 steps, two
gate metrics, zero accepted edits) established that the binding constraint is
corpus **freshness and coverage**, not optimizer tuning.

nugit's store already holds exactly that raw material — a lesson is a symptom
plus a resolution — and it grows with the repo via capture and `distill`. Making
the corpus a *derived artifact* of the knowledge store closes the loop: capture
improves the store, the store regenerates the benchmark. That is the
outbound-projection shape [[0011-external-tool-integration-single-writer]]
already licenses (IcePanel for the model, Notion for the corpus): git text is
the sole authoritative writer and the external artifact is a full derivation, so
divergence cannot accumulate.

The hard part is not the transform. It is that **an eval case is worthless if
its input reveals its answer** — worse than worthless, because a leaky case
silently inflates the benchmark and every measurement taken afterwards is a lie.
Three leak vectors, all observed in a real pilot store:

1. **The id and title state the conclusion.** Knowledge objects are named after
   what was learned, so an id like `LESSON-<subsystem>-reaps-non-protected-<thing>-tags`
   *is* the answer.
2. **The Trigger is not a symptom.** `nugit distill` seeds a lesson's Trigger
   from the commit subject that carried the trailer, so machine-distilled
   lessons routinely open with a conventional-commit line ("feat: … — setup
   pre-flight checks") — a task description, not an observation. Hand-written
   ones sometimes open with a review or an issue reference instead.
3. **The Trigger restates the Insight** — including a compressed form where an
   otherwise clean symptom carries the author's conclusion inside its closing
   citation parenthetical.

## Decision

1. **A new top-level verb, `nugit export -format skillopt`**, backed by a new
   `internal/skillopt` package. Top-level, not under `nugit c4 export`: that one
   projects the *model*; this projects the *knowledge store*. JSONL goes to
   stdout so it stays pipeable, and every other word goes to stderr.
2. **`input` is built ONLY from the Trigger section.** Never the title, the id,
   the filename or the keywords. `title` and `source` still ship as case
   metadata (the harness does not show them to the model under test), but the
   code path that fills `input` reads one section and nothing else. This is the
   whole defence against leak vector 1, and it is structural rather than
   heuristic. `gold_answer` is the Insight (or a composed `Cause` + `Fix`), with
   the `Rejected` rationale appended as explicit negative guidance.
3. **A deterministic leakage/quality gate that refuses rather than degrades.**
   Signals, each reported by name: `trigger-too-short` (below eight content
   words a trigger is a label, not a symptom); `trigger-commit-subject` (a
   Conventional Commits shape); `trigger-not-a-symptom` (no observable-behaviour
   cue — a failure/wrongness/absence lexicon, negated observation, a contrasted
   observation, or a quoted error artifact such as a 4xx/5xx status or a
   `SCREAMING_SNAKE` constant); `trigger-echoes-answer` (≥60% of the trigger's
   content words also appear in the answer, or its closing parenthetical does);
   `trigger-leaks-title` (≥60% of the answer-bearing title/id words reappear in
   the trigger); plus `no-trigger` / `no-answer`. Every signal is evaluated —
   the report lists all reasons, not the first. The gate is tuned for
   **precision over recall**: a skipped case costs nothing because the store
   regenerates the corpus next month, and a leaky case is permanent.
4. **Tiers and a report, following the `nugit deploy` precedent** (HIGH-3 /
   HIGH-2 / NEEDS-AGENT) and [[0012-ai-drafts-model-code-enforces]]. A trigger
   that clears every gate with ≥14 content words is HIGH-3; one that clears them
   while thin is HIGH-2; anything refused is NEEDS-AGENT with its reasons. Both
   emitting tiers ship by default (`-min-tier high-3` is the precision dial), a
   counts-plus-reasons summary always goes to stderr, and `-report <path>`
   writes the full per-object accounting as JSON.
5. **Superseded, invalidated and proposed objects are excluded**, via the
   derived `EffectiveStatus` ([[0003-supersede-without-mutation]]) rather than
   any authored field. A superseded lesson holds a *dead* answer and a proposed
   one is an unreviewed machine draft (the candidate lane of
   [[0016-candidate-lane-and-ratify]]); either as ground truth trains the wrong
   thing just as surely as a leak does.
6. **Lessons only in v1; decisions are excluded.** An ADR's `Context` is a
   rationale narrative, not an observed symptom — it argues *towards* the
   decision and usually states its shape before the `Decision` section does, so
   `Context → input` leaks by construction and would need a second, differently
   shaped gate. And the resulting task ("what should we decide?") is a design
   question, not the incident diagnosis this benchmark measures. Excluded until
   there is a reason to grade design choices, rather than shipped behind a flag
   nobody can trust.
7. **One stream; the consumer splits it.** The consuming tool accepts both a
   single raw file and a pre-split directory, and a split is a *policy* the
   consumer owns: a corpus that regenerates monthly must not reshuffle
   train/val/test every time it grows, or no two measurements are comparable.
   The stable `number` (`lesson:<slug>`) is exactly the key a consumer hashes to
   get a growth-stable assignment. Emitting an opinionated `-split 6:2:2` would
   put that policy in the wrong repo.
8. **`labels` carry `lesson`, `scope:<component>` and `tier:<t>`** so a run can
   be scored per area of the system and per confidence tier — the latter is how
   the thin-input limitation below gets measured rather than argued about.

## Rejected

- **Emit every lesson unfiltered and let the harness sort it out.** This is the
  status-quo-by-default option and it is the worst one: measured on a real pilot
  store it would have shipped a case whose id spells out its own answer, several
  whose input is a commit subject, and one whose symptom restates its cause.
  Nobody reads 100 generated cases line by line, so leaks would survive
  indefinitely and inflate every number derived from them. A benchmark you
  cannot trust is more expensive than no benchmark.
- **Have an LLM generate the cases at export time.** It would write far better
  inputs — that is exactly the enrichment the thin-trigger limitation calls for.
  But it makes the corpus non-deterministic (the same store yields a different
  benchmark each run, so a score change cannot be attributed), costly per run,
  and self-referential: an LLM-written case graded against an LLM-written gold
  answer measures the generator, not the skill. ADR-0012's line holds — AI may
  draft into a reviewed PR, code enforces; export is the enforcement half.
- **Hang it off `nugit c4 export -format skillopt`.** Wrong tree: that verb
  projects `workspace.dsl`, and every flag and error message there is about the
  model. A knowledge-store projection under a c4 subcommand is a naming lie that
  gets more expensive with each additional export format.
- **Ship `-split 6:2:2` with a seed.** A seeded shuffle is deterministic for a
  *fixed* corpus and unstable for a growing one — adding one lesson re-partitions
  the rest, silently invalidating every prior measurement. Doing it properly
  means hash-partitioning on a stable id, which is the consumer's contract to
  define, not the exporter's.
- **Include decisions behind an `-include decisions` flag.** A flag whose output
  is known to leak is not an option, it is a trap: it exists, someone uses it,
  and the corpus is quietly poisoned. See Decision 6.

## Consequences

- **Deterministically-derived inputs are THINNER than hand-mined ones** — a
  one-line trigger versus a paragraph of observed state. The export buys
  *regenerable coverage*, not richer cases; an agent enrichment pass that
  expands a HIGH-2 trigger into full observed state (grounded by the source
  object, landing as a reviewed PR) is the obvious follow-up, and the
  `tier:` label is there so its effect can be measured.
- **Corpus yield is now a legible measure of capture quality.** Against a real
  pilot store (26 lessons) the gate emits 17 cases and refuses 9, and against
  nugit's own store it emits **zero** — one lesson opens with a commit subject,
  the other with a situation rather than a symptom. That is not a bug in the
  exporter; it is the store telling the truth about its own capture hygiene, and
  the refusal reasons are a concrete to-do list. Expect `nugit doctor` to want
  this signal eventually.
- **The gate is a filter, not an oracle.** Precision-first means real cases are
  refused (a genuine symptom stated in five words is thrown away), and a residual
  leak class survives: a trigger that names the *category* of the resolution in
  the author's own novel phrasing shares too few tokens with the answer to trip
  the overlap signals. Defences are that `input` is structurally section-scoped,
  that the report names every refusal so the store owner can audit both sides,
  and that the corpus is cheap to regenerate once a lesson is rewritten.
- **The observable-symptom lexicon is a maintenance surface.** It was calibrated
  against one real store; a different domain will need vocabulary the list does
  not have, and every addition trades recall for precision in a direction that
  must be re-validated against a real corpus, never guessed at.
- **Regeneration changes the corpus, so a score is only comparable against a
  pinned commit.** The exporter is deterministic (same store → byte-identical
  JSONL, cases sorted by `number`), which makes the corpus reproducible from a
  ref — the consumer must record which one.
- **Proposed lessons never export**, so a store that has just adopted nugit and
  run `distill` yields nothing until its candidates are ratified. That is the
  intended nudge, but it makes ratification a prerequisite for any benchmark
  work rather than an optional hygiene step.
