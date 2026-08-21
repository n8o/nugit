---
schema_version: 1
id: ADR-0040
type: decision
scope: global
status: proposed
created: 2026-08-21T00:00:00Z
relates_to:
  - constrains:beads
  - constrains:delta
  - constrains:render
  - constrains:config
  - amends:ADR-0022
  - informs:ADR-0039
provenance:
  commit: seed
  citation: "pilot repo, .beads/issues.jsonl at 67 epics across 17 plans; PR rendering the whole board as its own position"
confidence: high
---

# ADR-0040 — A plan store holds many plans; a PR renders the one it moved

## Context

`nugit pr-render` reads a Beads store and renders **Plan position** — done /
current / remaining, plus what this PR moved. It was designed against a store
holding *the* plan. Under agent fan-out that premise is false.

`bd` keeps **one database per repository**, resolved from the main checkout even
when invoked from a linked worktree. A repo where every task gets its own
worktree therefore has every concurrent session writing one store. Measured on
the pilot: **67 epics across 17 distinct plans in one `issues.jsonl`**, two of
them in flight at once, authored by different sessions on different days.

Three failures follow, and all three were reported from the pilot as separate
complaints before anyone noticed they were the same one.

1. **A PR claims progress it did not make.** A change touching one subsystem
   rendered all 67 epics: 48 done, 17 remaining, and a `current` belonging to an
   unrelated plan another agent was executing. The section answers "where is
   this work" with the state of the repo's whole board, and a reader cannot tell
   which part of it this change is responsible for. The delta line was correct
   throughout — but it sat under a position that was not this PR's, and the
   position is what a reader sees first.

2. **The store is a merge-conflict generator.** `bd export` serializes the whole
   database in the database's own order, so closing one step rewrites the file
   and can move unrelated lines. Two agents who touched two *different* steps
   still produce two whole-file rewrites over the same path. The pilot's
   documented remediation was to hand-resolve with `bd` — reset to the base's
   version, re-import, re-apply your own closes, re-export — on a file where a
   hand-merge invents states no `bd` command produced.

3. **Everything the reader silently tolerates is a step that vanishes.** The
   reader skips an unparseable line, drops a duplicate id last-write-wins,
   renders an unrecognised status as `remaining`, hides non-epics behind a
   footnote, and shows only the first in-progress issue as `current`. Each is a
   deliberate tolerance and each is silent. The pilot ended up with a
   **250-line shell script** reverse-engineering `internal/beads/beads.go` to
   report them — a second implementation of this package's semantics, in another
   language, in another repo, pinned to a version comment.

The common cause: nugit modelled the store as one plan, and the store is a
shared, concurrently-written, repo-wide board.

## Decision

1. **A plan is a first-class grouping, resolved most-explicit-first:**
   an authored `plan` field on the line; else the **store file's stem** when the
   store spans more than one file; else the **id family** — the id minus its last
   dash-separated segment, and only when the id has three or more segments.
   `bd`'s native ids are `<prefix>-<n>`, so a two-segment id (`acme-118`) is one
   bead that is its own plan; collapsing it to `acme` would put every bead in the
   repo into one plan named after the issue prefix, which is the bug inverted.

2. **`plan.scope: touched` is the default.** A PR renders only the plans whose
   steps it moved between base and head. Other plans are counted, and the ones
   with a step actually in flight are **named** — "also in flight elsewhere:
   acme-deploy" is how an author notices a neighbour, where "16 other plans" is
   noise. `plan.scope: all` opts back into the whole board.

3. **Scoping runs after the diff, never before.** Which plans a PR touched is
   itself a fact about the diff. Computing the full position first is also what
   lets the note state honestly how many plans it is not showing.

4. **A PR that moves no step renders no plan at all** — not the board, which is
   the failure this decision exists to end. The note says so, and a `warn`
   finding fires when the PR also changed code.

5. **The position is per-plan; the delta is always only this PR's.** Even under
   `scope: all`, `NewlyCompleted` / `NewlyStarted` / `Regressed` are the union
   over what this PR moved. Movement renders inside the block of the plan it
   belongs to, never pooled into one undifferentiated "changes since base".

6. **`nugit plan check` lints the store by nugit's own reader semantics** — the
   rules are properties of this package, so a repo that wants them must not have
   to reimplement them. Surfaced identically at PR time as check `plan-store`.

7. **`nugit plan normalize` canonicalizes the store**: one stable line per bead,
   sorted by plan then natural id, keys sorted, every unread field preserved
   byte-exact through `json.Number`. Two agents editing two different beads then
   produce **disjoint hunks that git merges by itself**, and the only conflict
   left is the honest one where both edited the same step. `-split` writes one
   file per plan, which removes even that overlap and makes the file the plan.

8. **`plan.mode: warn` (default) | `fail` | `off`.** The checks ramp in at warn.

## Rejected

- **Scope by branch name, PR title, or the author's session.** All three are
  guesses about intent from outside the diff. The store already records what
  this PR did to the plan; that is not a heuristic and cannot be wrong.
- **A per-worktree store, so each agent's plan is isolated.** Rejected upstream
  at the pilot and re-rejected here: it trades mild confusion for divergent
  plans that reconcile only through git conflicts, and destroys the one thing a
  shared store buys — a session seeing that another has already taken a step or
  is blocked on it. The problem is the *rendering*, not the sharing.
- **Keeping `scope: all` as the default and adding `touched` as an opt-in.** The
  default is what every repo gets, and the failure is silent — a PR that
  overstates its progress reads as a working feature. A knob nobody knows to set
  would leave every adopter with the bug.
- **`fail` severity for a duplicate plan-step id, as ADR-0039 ruled for
  knowledge ids.** ADR-0039's premise was that the check cannot be wrong *and*
  the fix is a one-line edit in a file this repo owns. The first half holds here;
  the second does not. `.beads/` is written by `bd`, from a database nugit
  cannot read, and a duplicate can arrive from an export or a hand-resolved
  merge nobody remembers making. Failing a gate over another tool's state, on
  the upgrade that introduced the check, is the ADR-0016 ramp this repo already
  chose for `contracts.mode` and `c4.mode`. `plan.mode: fail` is the deliberate
  step up, and this is a *narrowing* of ADR-0039's reasoning, not a reversal:
  severity follows from whether the check can be wrong **and** whether the repo
  can act on it.
- **Making nugit write the store (close a bead, run the export).** ADR-0011's
  second-writer rule. `bd` owns the store; nugit reads it and, in `normalize`,
  rewrites only the serialization — never a status, never a field's value.
- **Auto-splitting the store on read.** `normalize -split` is explicit and
  reviewable because it moves committed files. A reader that silently reshaped
  the store on disk would be a write nobody asked for.
- **Deducing the plan from bead dependency edges (`blocked_by`) instead of ids.**
  Structurally attractive and empirically wrong on the pilot: cross-plan
  dependencies are common and deliberate (`acme-151-5` blocked by
  `acme-153-c`, authored by two sessions), so the connected component spans
  plans that are genuinely separate work.
- **Nagging a repo with no store to adopt `bd`.** No store ⇒ no findings, ever,
  the same rule the peer and contract paths follow.

## Consequences

- `internal/beads` gains `Store`, `Read`, `ReadDir`, `Position`, `Lint`,
  `Normalize`; `PlanPosition` gains `Tracks` / `PlansTotal` / `Hidden`, all
  `omitempty` so a repo with no store keeps its JSON contract byte-for-byte.
- **A PR's rendered plan changes shape on upgrade** — from the repo's board to
  this PR's plans. That is the point, and it is the one visible break.
- The pilot's 250-line `check-beads.sh` is superseded by `nugit plan check`. The
  duplicate-id rule found a **live collision on the pilot's `master`** the first
  time it ran, which the script had been in place to catch.
- One rule the script carried is **not** absorbed: "the PR body claims to close
  a step the store says is open". It needs the PR body, which pr-render does not
  read. Named here so it is a deferred decision rather than a silent gap.
- "Two in-progress epics" stops being a warning across plans, where it is just
  what concurrency looks like, and becomes one **within** a plan, where it means
  a step was started and never closed.
