---
schema_version: 1
id: ADR-0045
type: decision
scope: global
status: proposed
created: 2026-10-16T00:00:00Z
relates_to:
  - constrains:replay
  - amends:ADR-0044
  - informs:ADR-0006
provenance:
  commit: seed
  citation: "fix/replay-one-bead-per-task; a real pilot store, 2026-10: one replay task's solution squash implemented its bead AND an unrelated issue, and every hidden test was for the unrelated issue"
confidence: medium
---

# ADR-0045 — A replay task is one bead and one change

## Context

ADR-0044 joins a closed bead to the commit(s) that mention it and calls that
the solution. On a real pilot store one task's solution was a squash that did
two things: the bead (a small chart/config fix) and an unrelated issue that
was larger. Its hidden tests were entirely for the unrelated issue. The agent
under test was prompted with the bead and graded against tests for something
else; the careful arm refused ("the acceptance test is for a different
issue"), and the arms that simply made the tests pass "won". That task
measures obedience to the tests, not the bead.

The same class shows up three other ways: one commit or PR closing several
beads (`Closes xx-1, xx-2`), one squash whose body lists several beads' work,
and a solution that closes a GitHub issue the bead never mentions. In each,
the solution diff — and therefore the hidden tests — is a union of changes,
and a prompt for one of them is not the task they grade.

## Decision

1. **Multi-bead.** A task is `multi` when either holds:
   - **another bead was mined to the same solution commit**, outside this
     bead's family. Family is: equal, an ancestor (`gr-12` / `gr-12-1`), or a
     lettered sub-step (`gr-12-7` / `gr-12-7a`) — one piece of work by
     construction, and `gr-1` / `gr-12` are not family;
   - **an implementing commit closes something else**: a closing verb
     (`close[sd]`, `fix(e[sd])`, `resolve[sd]`, `implement[sd]`) governing a
     known bead id outside the family, including every id of a list
     (`Closes xx-1, xx-2 and xx-3`); or a GitHub closing keyword governing an
     issue (`Closes #12`, `fixes: #3, #4`, `resolves owner/repo#9`) that the
     bead's own title, description or `external_ref` never references (`#12`,
     `issues/12`, `gh-12`).

   A commit touching only `.beads/` / `.nugit/` implements nothing and is not
   read. A bead merely **named** in passing ("follow-up to x", "x still in
   progress") is a cross-reference, not a second piece of work. A squash
   subject's bare `(#123)` is a PR number, not a close.
2. **Tests-unrelated.** A task's hidden tests are `tests_unrelated` when NO
   hidden test has any link to the bead's title + description (read at base,
   like the prompt). A test links when any of:
   1. a bead term occurs in the test's **path** (prose words ≥ 4 letters), or
      in its **content** (prose words ≥ 5 letters);
   2. a bead term (≥ 4) occurs in the path of a non-test solution file **in
      the test's own directory** — the code the test exercises;
   3. the test sits in the **primary path's directory** AND that directory's
      **added solution lines** share at least **two** distinct bead terms.

   Bead terms are: backticked spans whole and split into identifier words;
   path-like tokens' base names, stems and segments; identifier-shaped words
   (camelCase, snake_case, digits) whole and split; and plain words above the
   length floor — minus a short, generic stopword list. Words on both sides are
   lowercased, and identifiers are split on case and punctuation. A bead with
   no distinctive terms is never judged.
3. **Default skip, `-allow-multi` keeps.** Skips are reported by name in the
   stderr summary: `multi-bead`, `tests-unrelated` (multi-bead is decided
   first). `nugit export -format tasks -allow-multi` keeps them instead, with
   `tags.multi` / `tags.tests_unrelated` set to true — new fields; the schema
   contract (add, never rename) holds.
4. Git-only and deterministic (ADR-0006): no network, no model, no randomness;
   the same history yields the same task set.

## Why these thresholds

- **A path match is cheaper than a content match.** A test's file name is a
  deliberate choice of topic, so a shared 4-letter word there
  means something; a file's content is large and noisy, so it needs 5.
- **Sitting next to the primary path is not enough on its own.** When one
  commit did two things, the larger one decides the primary path (most changed
  lines), and its test sits right beside it — that was exactly the observed
  case. Rule 2.3 therefore asks the primary directory's added lines to be
  *about* the bead too, and asks for two shared terms: one coincidental word
  in a diff of any size (as in the observed case) is not a topic.
- **Every miss errs toward keeping.** A missing stopword, an extra term, or a
  bead with no distinctive terms can only make a test look more related.

## Rejected

- **An LLM judge of test relevance.** Nondeterministic — the same history
  would yield a different benchmark per run, so a score change could not be
  attributed — costly per export, and outside the keystone (ADR-0006).
- **Drop only by commit-message count** (skip when a message names two or
  more bead ids). It misses the observed case outright: a single bead id plus
  an unrelated fix that carries no id at all. And it misfires the other way on
  a message that merely names a second bead in passing.
- **Treat any other bead id in the message as multi** (the first cut). On the
  pilot store it marked known-good tasks whose squash body mentioned a sibling
  bead "in progress", or closed the bead's own lettered sub-step. Closing verbs
  and a shared solution commit are the signals that a second piece of work
  actually landed.
- **Keep any test in the primary path's directory** (the first cut of rule
  2.3). It keeps the observed case, for the reason above.

## Consequences

- On the pilot store (`-require-tests`): 168 → 120 tasks; 60 closed beads
  skipped `multi-bead` (47 of them had hidden tests — most are groups of 2–7
  beads sharing one squash) and 1 `tests-unrelated` (the observed case). A
  hand-checked set of nine known-good tasks were all kept.
- Yield drops, deliberately: a benchmark of fewer tasks that each grade the
  prompt beats more tasks some of which grade something else. `-allow-multi`
  recovers them, tagged, for anyone who wants to study the multi-purpose ones.
- The heuristic is lexical. A bead written in prose that shares no word with
  its own test, its code's file names, or two words with its code's added
  lines, AND whose solution also does something else, will be dropped; one
  that shares a stray word with an unrelated test will be kept.
