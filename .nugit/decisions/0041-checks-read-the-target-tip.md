---
schema_version: 1
id: ADR-0041
type: decision
scope: global
status: proposed
created: 2026-10-08T00:00:00Z
relates_to:
  - constrains:consistency
  - constrains:doctor
  - constrains:wiring
  - constrains:knowledge
  - amends:ADR-0039
  - amends:ADR-0026
provenance:
  commit: seed
  citation: "pilot repo at 2026-10-08: ADR-JBS-0051 carried by 3 files on master (merged 11:02, 12:19, 14:00 on 2026-08-27 from three bases); 368 of 393 relates_to edges carrying no verb across 162/220 objects; one install pin stale in 1 of 5 surfaces for four weeks; store 87→220 objects while health fell 65→55 and 68/92 components stayed uncovered"
confidence: high
---

# ADR-0041 — A check is only as good as its reference point, its moment, and its vocabulary

## Context

Six weeks of a fast pilot (593 commits, 220 knowledge objects, 24→70 plans)
produced four failures. They look unrelated and are not: each is a check that
was *present and correct* yet could not fire.

**1. An id collided three times and every PR was green.** `ADR-JBS-0051` is
carried by three different files on the pilot's master — fabrics-provider,
offline-site-access, telemetry-producer — merged at 11:02, 12:19 and 14:00 on
one day, from three different bases.

ADR-0039 added exactly this check and it worked as specified: it reads the
store at the reviewed **head**. But a head that has not merged the target
cannot contain a sibling's record, so there is nothing there to collide with.
That is the ordinary state of a pull request whose siblings are landing, and
re-running CI does not help: a `pull_request` workflow fires on a push to the
head branch, never on the base branch advancing. A green check has always meant
*green against the base as of the last push to this branch*.

A hand-assigned sequential id turns that window into a collision generator,
because it is a **shared mutable counter**. Three branches cut before 0051
existed all saw 0050 as the highest and all minted 0051. This is the same
failure as the plan store's merge conflicts (ADR-0040): concurrent writers, one
shared namespace, no coordination — and like that one, it is fixed by changing
what the artifact is keyed on, not by asking people to be careful.

**2. The `relates_to` vocabulary was prose, so most of it did nothing.**
Measured with nugit's own parser over the pilot's store: **368 of 393 edges
carry no verb at all**, across 162 of 220 objects. Of the 25 that do, `amends`
is 15, `informs` 9, and one is `refines` — which no code path reads.

Worse, AGENTS.md told authors to write `relates_to: [constrains:<component>,
prevents:<key>, satisfies:<spec>]`, and **`prevents` and `satisfies` have zero
non-test code references**. nugit's own store uses `prevents:`. The vocabulary
existed only as examples, so an author could not tell a load-bearing edge from a
decorative one, and `ParseEdge` is tolerant by design: it splits on the first
colon and returns whatever it finds. `ammends:ADR-7` parses, resolves in
retrieval's one-hop pull, and amends nothing. `supersedes:` written as an edge
is worse — ADR-0003 derives effective status from the front-matter *field* of
that name, so an edge spelling of it reads like a supersession and declares
none.

LESSON-a-tolerant-reader-owes-the-author-a-linter was written about the plan
store. It applies verbatim here and had not been applied.

**3. Wiring drift outlived the pre-flight that reports it.** The pilot names a
nugit version in five places. Four were aligned by hand; `.claude/skills/nugit/SKILL.md`
sat four weeks behind. `nugit doctor` reported it correctly the entire time,
into a terminal nobody opened. ADR-0039 already made this argument — "a
pre-flight nobody runs on a Tuesday is how this collision survived a week" —
and then applied it to one check only.

**4. The coverage number was true and unusable.** The store grew from 87 to 220
objects while health fell 65→55 and 68 of 92 components stayed uncovered.
Capture lands where capture already is. "68 of 92 are uncovered" names no first
move, and the honest response to it is to ignore it.

## Decision

1. **Uniqueness is read from the target branch's TIP, not the merge base.** A
   delta is measured against the merge base because that is what "what
   changed" means. Uniqueness is not a delta — it is a property of the tree the
   change is about to join — so it reads `opt.Base` at render time, an input
   the engine already received and previously used only to compute the merge
   base. Read there, the answer no longer depends on how stale the branch is.

2. **Only ADDED ids are checked against the target**, and a collision the
   within-PR check already reported is suppressed. A modified record's id is on
   the target by definition; flagging it would fail every edit to every ADR.

3. **The `relates_to` vocabulary is declared in code, next to the parser**, and
   classified by what nugit actually does with a verb: `semantic` (drives
   derived status, amendment, reinforcement, reference attribution), `scope`
   (widens retrieval), `annotation` (traversal only). An entry in that table is
   a claim that something reads it.

4. **`prevents`, `satisfies` and `refines` are classified as `annotation`, not
   removed.** They were documented for months and are in both stores; demoting
   them to "unknown" would turn honest authorship into a wall of warnings. They
   are real "see also" edges. What changes is that the docs stop implying they
   are lifecycle claims.

5. **A bare id is valid and means "see also".** 368 findings would be noise.
   Instead `doctor` reports the store's *shape* — semantic / scope / annotation
   / bare, with the bare share — and says so once when a store is
   overwhelmingly bare, because a lifecycle graph that exists only in prose is
   worth one sentence.

6. **Unusable edges warn at PR time, scoped to touched objects**; front-matter
   fields written as edges (`supersedes:`, `applies_to_paths:`, `scope:`) get
   their own finding naming where they belong.

7. **The wiring scan moves to `internal/wiring`** and runs from both surfaces:
   `doctor` over the checkout (advisory, unchanged), and `pr-render` at the
   reviewed ref (warn), scoped to PRs that touch a wiring artifact. One
   implementation, because a second copy of these rules is the drift they
   detect.

8. **Coverage gains a direction, not a deduction.** `doctor` ranks the
   uncovered components by churn in a bounded 90-day window and names the top
   five. Descriptive only: the orphan ratio is already scored, and
   double-counting would move a number adopters track over time.

## Rejected

- **Requiring branches to be up to date before merge** (GitHub branch
  protection). It would fix the id collision by re-running every check on every
  base advance, and it is the right call for some repos — but it is a policy
  nugit cannot set, costs a rebase per sibling merge on a repo landing ~10 PRs a
  day, and leaves every adopter who has not enabled it exactly where they were.
  Reading the target tip costs one git read and works regardless.
- **Content-addressed or allocated ids instead of hand-assigned sequences.**
  ADR-0001 settled on stable human keys and the reasons still hold. This makes
  the counter's collisions visible; it does not relitigate the counter.
- **`fail` severity for unusable edges.** An unrecognised verb is still a
  readable "see also", and the store must not need a migration to keep
  rendering. Warn, scoped to what the PR touched.
- **Implementing `prevents:` and `satisfies:` semantics so the docs become
  true.** Speculative under ADR-0004: no trigger has asked for either. Telling
  the truth about them costs nothing and can be reversed the day one does.
- **A per-edge finding for bare ids.** 368 of them. The check people mute is
  worth less than no check.
- **Deducting store-health points for churn-heavy uncovered components.** The
  gap is already scored once through the orphan ratio. Scoring it twice would
  make every historical baseline incomparable for no new information.
- **Firing the wiring checks on every PR, not just ones touching wiring.**
  Pre-existing drift is doctor's job. A gate that fires on unrelated work is
  the one people learn to ignore — the same reasoning that scopes
  `duplicate-knowledge-id` to touched objects.
- **Making the PR-time wiring checks `fail`.** The drift is in documentation and
  CI plumbing, and a repo mid-upgrade legitimately passes through a mixed
  state.

## Consequences

- `internal/wiring` is new; `doctor` and `consistency` both consume it.
  `gitutil` gains `Resolve` and a bounded `ChurnSince`.
- **`pr-render` can now fail a PR that passed yesterday** — specifically one
  adding an id a sibling has since landed. The remediation is renumbering one
  record, and no edge resolves to a duplicated id, so nothing breaks. Replayed
  against the pilot: a branch cut before 0051 existed, rendered against current
  master, now fails and names all three holders.
- Two new checks, `edge-vocabulary` and `wiring-drift`, both warn, both with
  `nugit explain` entries. `duplicate-knowledge-id` gains a second shape.
- ADR-0039's severity reasoning is unchanged and its reference point is
  corrected. ADR-0026's wiring scan is unchanged and gains a second surface.
- The pilot's store will warn on its one `refines:` edge and on nothing else,
  and `doctor` will tell it that 94% of its edges carry no verb.
