---
schema_version: 1
id: ADR-0024
type: decision
scope: global
status: proposed
created: 2026-08-01T00:00:00Z
relates_to:
  - constrains:retrieval
  - informs:ADR-0013
provenance:
  commit: seed
  citation: "feat/retrieval-budget-fill"
confidence: high
---

# ADR-0024 — Fill the retrieval budget: path-history capture and consistent matching

## Context

The ADR-0013 usage log gave us the first real telemetry on the retrieval half,
and it says the budget machinery has never once engaged. In the pilot's
`.nugit/.cache/usage.jsonl` (21 events over 25 days), every single bundle ran
1345–3414 estimated tokens against a 4000–6000 budget — 40–70% of the budget
unused on every call, with `dropped: 0` and `truncated: false` on every record.
The binding constraint on context() is not the cap; it is what retrieval can
reach.

What it cannot reach is commit history. The pilot's preview-freeze incident
(#1923) is the concrete miss: the answer existed only as a fix commit's subject
("bump MOXYGEN_TAG v0.3.0-rc4 -> v0.3.0-rc5 — fixes the preview freeze") on the
exact path (`third_party/versions.env`) a later agent would touch. No lesson
was ever written, so no bundle could ever surface it — while the pilot store
holds 38 such orphaned trailers: captured why (ADR-0005's primitive) that never
became a store object. The capture already happened; retrieval just never looks
there.

Separately, the bundle applies two different relevance semantics to one task
string: `matches()` (decisions/lessons/references) is naive substring — "go"
matches "algorithm", "git" matches "digital" — while working-memory
`hasKeyword()` is whole-token over the same `keywords()` tokenization. A
spurious substring hit is not harmless: it occupies a slot and budget that a
real match should have had.

## Decision

1. **Path-history section, derived at read time.** After the existing fill
   order (decisions → lessons → references → glossary → working memory), the
   bundle appends "Recent capture on these paths": the last N commits touching
   the queried path — bounded `git log -n 5 --since "90 days" -- <path>` —
   rendered as the subject plus any `decision:`/`learned:` trailer lines. Zero
   new store writes, no index: the same pure-projection posture as the rest of
   the bundle, and the read-time surface for orphaned trailers. The read is
   best-effort at the same standard ADR-0013 sets for the edges: not a git
   repo, an unborn branch, or a failing git yields an empty section, never a
   failed or altered bundle.
2. **Whole-token matching everywhere.** `matches()` and the glossary line
   matcher unify onto the whole-token semantics `hasKeyword()` already had
   (one shared `overlaps()` over the `keywords()` tokenization). This is a
   deliberate precision-over-recall choice: plural/stem near-misses ("cache"
   vs "cached") no longer match, and neither do substring artifacts. The eval
   corpus gates (internal/eval) are unaffected — they exercise the keystone,
   not retrieval — and the full suite guards against regressions.
3. **Budget discipline is untouched.** Path history is the LAST fill priority:
   it exists only because budget remains, it is the first thing dropped when
   the budget is tight, and every drop lands in `Dropped[]` like every other
   kind — never a silent cut. The usage record gains a `path_history` count so
   the next telemetry review can measure the fill (ADR-0013).

## Rejected

- **Vector/FTS index now** — that is PLAN I2, deferred with an explicit
  trigger (grep/scan over `.nugit/` becomes too slow). The pilot evidence shows
  a *reach* gap, not a *ranking or speed* gap; an index would add a build
  artifact and a staleness surface to solve a problem the telemetry does not
  show. This ADR deliberately does not build it, and does not move the I2
  trigger.
- **Raise the default budget** — headroom is not the problem; every observed
  call is 40–70% under the cap already. A bigger budget fills nothing.
- **Distilling orphaned trailers into store objects at read time** — retrieval
  must stay a pure projection (ADR-0013 rejected even logging inside it);
  writing objects belongs to the deliberate distill → ratify pipeline
  (ADR-0016), not to a read path.
- **Keeping substring matching for recall** — the observed failure mode is
  spurious inclusion, not missed hits, and two matching semantics in one
  bundle make relevance behavior unexplainable. If recall suffers, stemming
  belongs in `keywords()` where every matcher inherits it at once.
- **Unbounded `git log`** — an unbounded walk makes bundle cost proportional
  to repo age. `-n 5` within 90 days keeps the section commit-bounded and
  recency-scoped; older knowledge worth keeping should be distilled, not
  re-read from history forever.

## Consequences

- The captured-but-orphaned why (38 trailers in the pilot today) becomes
  retrievable on exactly the paths it was captured against; the #1923 class of
  miss is closed without a single new byte in the store.
- The bundle gains a time-dependent section: a commit aging out of the 90-day
  window changes output between runs at the same ref. Accepted — the section
  is explicitly "recent capture", it is the lowest-priority filler, and the
  keystone's determinism guarantee (delta/consistency/significance) is not
  involved.
- Whole-token matching narrows recall; the mitigation lives in one place
  (`keywords()`), and `Dropped[]`/usage telemetry will show whether real
  matches thin out.
- retrieval grows two declared edges — `retrieval -> gitutil` (bounded log)
  and `retrieval -> trailers` (trailer parsing) — both read-only; the c4↔code
  check keeps them honest.
- Bundles get slightly larger on active paths; the estimator and `Dropped[]`
  keep them capped exactly as before, and `path_history` in usage.jsonl makes
  the fill measurable at the next pilot review.
