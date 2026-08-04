---
schema_version: 1
id: ADR-0028
type: decision
scope: global
status: proposed
created: 2026-08-03T00:00:00Z
relates_to:
  - constrains:distill
  - constrains:trailers
  - informs:ADR-0005
provenance:
  commit: seed
  citation: "fix/distill-trigger-symptom"
confidence: high
---

# ADR-0028 — A lesson's Trigger is an observable symptom, never the commit subject

## Context

nugit's house lesson body is `**Trigger:** <symptom>` / `**Insight:** <root
cause + fix>` / `**Rejected:** …` / `**Keywords:** …`. The Trigger is the half a
future debugger's query actually matches: they arrive holding what they *saw*,
not the name of the change that once fixed it.

`nugit distill` seeded Trigger from the commit subject, so it minted triggers
like `feat: nugit doctor — setup pre-flight health checks` — a task description.
Two consequences, both real and both checkable in this repo:

1. **Retrieval.** Nobody searches for the subject line of the commit that fixed
   their problem; they search for the failure in front of them. A subject-shaped
   trigger matches almost nothing a debugger would type, so the lesson is
   findable only by someone who already knows it exists.
2. **Measurable.** The eval exporter (ADR-0027, `nugit export -format skillopt`)
   refuses a lesson whose Trigger is a commit subject, via its
   `trigger-commit-subject` and `trigger-not-a-symptom` signals. Run against
   nugit's own two-lesson store, **0 of 2 lessons emit** — one of them precisely
   because distill seeded its Trigger from a commit subject. The capture
   pipeline was producing lessons its own exporter rejects.

The honest constraint: **a symptom cannot be synthesized from a commit
subject.** It is frequently nowhere in the commit at all — the author saw it in
a dashboard, a customer report, a failed CI run. Guessing at it would be worse
than the bug: a fabricated symptom is indistinguishable from a real one at
review time, and it poisons retrieval and the eval corpus with plausible
fiction.

## Decision

1. **A new optional `symptom:` commit trailer**, typed by `internal/trailers`
   into `model.Trailer.Symptom`. `learned:` and `keywords:` remain the only
   mandatory fields — capture stays cheap. When present, `symptom:` is taken
   verbatim as the lesson's Trigger: the author is the only party that knows
   what was observed, so nothing second-guesses them.
2. **Otherwise scavenge the commit body**, deterministically and biased toward
   refusal. The body is split into observation units — one prose sentence, one
   list item, or one line inside a fenced block — and the **first** unit in
   document order that passes every test wins (a body opens with the problem and
   closes with the remedy). A unit passes when it: is not a Conventional Commits
   subject; is not in the narrator's voice (`this change …`, `we will …`,
   leading `add/refactor/rename/…`); carries at least 8 content words; carries an
   observable-behavior cue — a failure-lexicon word, a negated observation
   (`did not run`), a literal error artifact (a 4xx/5xx status, a
   `SCREAMING_SNAKE` constant), or a contrast (`even though …`); and does not
   restate the `learned:` text (≥60% content-word containment either way) —
   a "symptom" that contains its own root cause is the cause.
3. **Refuse to fabricate.** When neither source yields a symptom, distill writes
   the exported placeholder `TriggerTODO` — "TODO — the observable symptom that
   led here", followed by a parenthetical naming the two empty sources — as the
   lesson's Trigger, instead of the commit subject. A visible gap in a
   reviewed PR is worth more than a fluent lesson nobody can retrieve, and the
   placeholder is refused by the export gate by construction, so an un-filled
   lesson can never become an eval case.
4. **distill's cue vocabulary is a subset of the export gate's.** Anything
   distill accepts as a symptom must also read as one to the exporter, otherwise
   the pipeline still mints triggers its own gate refuses. The subset property is
   asserted by a test that replicates the gate's lexicon.

## Rejected

- **Synthesizing a symptom from the subject with an LLM** — the keystone is
  deterministic and LLM-free (ADR-0006), and the input genuinely does not
  contain the answer: the model would invent an observation, and an invented
  symptom is unfalsifiable at review time and permanent in the store.
- **Keeping the subject as a last-resort fallback** — this is the whole bug. A
  subject-seeded trigger looks like a finished lesson, so nobody fixes it; the
  placeholder is worse-looking on purpose.
- **Making `symptom:` mandatory** — capture is opt-in and already thin;
  a third mandatory field buys silence, not symptoms. It is prompted, not
  demanded.
- **Recall-first scavenging (bare `X was Y` / `X returned Y` patterns)** —
  those shapes match ordinary design prose ("the model was leveled", "the
  parser returns components"), and a false accept mints a junk lesson forever
  while a false reject only asks the author for one line.
- **Accepting a bare exit-code observation ("exited 137") as its own cue** —
  the exporter's lexicon does not recognize an exit code alone, so distill
  would mint a trigger its own gate refuses. Such observations reach the store
  through `symptom:`, or through the failure word that almost always
  accompanies them.
- **Rewriting the existing subject-seeded lesson in this store** — a ratified
  object is immutable (ADR-0003); remediation is a supersede, deliberately out
  of scope here.

## Consequences

- Distilled lessons become retrievable by symptom, and a distilled lesson now
  clears the ADR-0027 gate's trigger checks — proven by a round-trip test that
  replicates the gate rather than importing it, so this change stays
  independently mergeable.
- Some commits now distill to a TODO trigger. That is the point: the number of
  TODOs is an honest measure of how much of the "why" was never written down,
  where the old behavior reported 100% coverage of a field it was faking.
- `symptom:` joins the structured JSON commit surface (`Trailer.Symptom`) and
  the capture templates in AGENTS.md and the nugit skill.
- A bare `error: …` line at the start of a body line is read as a trailer line
  by the §6.1 convention and skipped by the scavenger — paste logs inside a
  fenced block, where they are scanned.
- The content-word vocabulary (stopwords, stemmer, cue lexicon) is duplicated
  between distill and the exporter's gate and kept honest by a parity test.
  Once both have landed, folding it into a single package is follow-up work.
- Follow-ups, deliberately not depended on here: the capture nudge (ADR-0023)
  should prompt `symptom:` in its stub, and the PR-time proposal view
  (ADR-0018) should flag a proposed lesson whose Trigger starts with
  `TriggerTODO` so the gap is visible before the lesson lands.
- ADR bodies still take their Context from the commit subject: a decision's
  context legitimately is the change being made, and only the lesson Trigger
  claims to be an observation.
