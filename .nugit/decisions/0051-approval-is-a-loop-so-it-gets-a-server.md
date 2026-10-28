---
schema_version: 1
id: ADR-0051
type: decision
scope: review
status: proposed
created: 2026-10-26T00:00:00Z
relates_to:
  - constrains:review
  - constrains:cli
  - amends:ADR-0016
  - elaborates:ADR-0006
provenance:
  commit: seed
  citation: "24 records sat at status: proposed across two stores when this was built — 15 in this repo (oldest ADR-0037, 81 days) and 9 in the pilot (oldest ADR-JBS-0023, 100 days); `nugit ratify` had essentially never been run in either repo despite shipping with ADR-0016"
confidence: high
---

# ADR-0051 — Approval is a loop, so it gets a local server

## Context

ADR-0016 gave the store a candidate lane: distilled and drafted records land
`status: proposed` and a human promotes them with `nugit ratify <id>`. The lane
works. The promotion does not get used.

Measured 2026-10-26 across both stores: **24 records proposed, the oldest 100
days** — 15 of them in this repo — and `ratify` run almost never in either,
including on this repo's own ADR-0037 through ADR-0041. The lane is not a backlog that someone is
working; it is a backlog nobody opens.

The reason is the shape of the task, not the command. Deciding on a record
means reading its Context, its Decision and — above all — its Rejected
alternatives, forming a view, and moving to the next one. That is a loop with
a document at each step. The surfaces available were:

- **`ratify -list`** — a table of ids and paths. It tells you what is waiting
  and nothing about any of it.
- **the files** — complete, but it means an editor, two dozen tabs, and
  retyping each id into a second tool to act.
- **`pr-render`** — shows candidates pending, as a count, on unrelated PRs.

None closes the loop, so the loop never ran. The first attempt at a fix was a
hosted page outside the repo, which the user rejected on the right grounds: the
approval surface for a git-native tool should ship with the tool and write git,
not live in someone else's datastore and hand the result back for a human to
apply.

## Decision

1. **`nugit review` serves the candidate lane over loopback**, in a browser,
   and writes the store directly. One record per card, opened to Context /
   Decision / **Rejected**, oldest first, with the two actions on the card.

2. **Rejected is rendered with the same weight as Decision.** It is the field
   ADR-0016's lane exists to protect — the anti-hallucination field that stops
   an alternative being re-proposed — and a reviewer deciding without it is
   deciding on half the record.

3. **Accept calls `ratify.Ratify`.** Not a reimplementation: the same function
   the CLI calls, and the status it grants is read from `ratify.GrantedStatus`
   so the button can say `active` for a lesson and `accepted` for a decision
   without a second copy of that table.

4. **Decline deletes the candidate file.** There is deliberately no `rejected`
   status (see Rejected below).

5. **Nothing is committed.** The command writes the working tree, reports the
   files it touched, and says to commit them. Approving is a judgement; turning
   it into a commit is still the user's move, and `git checkout` undoes a
   mis-click.

6. **It is not a service.** Loopback only, started by hand, no state of its
   own, no background process, and nothing in the delta path — the engine never
   calls it and the rendered report is byte-identical whether it has ever run.
   This elaborates ADR-0006 rather than bending it: the determinism rule is
   about what computes a report, and this computes none.

7. **Every mutating route requires a per-run token**, minted at startup and
   carried in the URL the command prints. Loopback keeps other machines out; the
   token keeps other local processes out of a route that deletes files.

8. **One repo per server.** The queue is the store of the repo it was launched
   in. A peer's records are readable but never writable (ADR-0032), so a queue
   spanning repos would offer buttons that cannot work.

## Rejected

- **A `rejected` status, or reusing `invalidated`.** `invalidated` means a
  record that WAS live and is now void. A declined candidate carrying it would
  be governed knowledge in a dead state: `stale-knowledge` fires on any PR
  touching code governed by an invalidated object, so a declined ADR scoped to
  a component would warn on every PR touching that component, forever. That is
  how a check earns a mute. A candidate that is not adopted leaves the lane, and
  git keeps it — `git show` on the deleting commit is the archive, and a
  rejected direction worth serving to a future reader belongs in the `Rejected`
  section of whatever decision replaced it, where retrieval reaches it.
- **A hosted page outside the repo.** Tried first and rejected by the user. It
  put approval state in a datastore nugit does not own, needed a human or an
  agent to carry verdicts back into git, and would not work for an adopter who
  simply installed the binary.
- **A terminal TUI.** Fewer moving parts and no socket, and it was offered. It
  cannot render a document well, it is keyboard-only at the machine running it,
  and the records are prose — which is exactly what a browser is for.
- **One generated "ratify everything pending" PR.** Also offered. It is a good
  fit for a clean lane and a bad one for a 100-day backlog: the unit of
  judgement is the record, and a single PR makes two dozen of them one
  yes-or-no.
- **Committing each approval.** A commit per click produces a branch nobody
  wants to read and no moment to change your mind. Writing the tree and naming
  the files is the reversible version.
- **Binding anything but loopback.** The process deletes files in the repo.
- **Auto-opening without a way to opt out.** `-no-open` exists because this
  will be run over SSH, and a command that tries to launch a browser on a
  headless box and says nothing is a command that looks broken.
- **Serving the whole record body.** An excerpt plus the file path is enough to
  decide, and the file is right there. Streaming full bodies would also make the
  page the authority on content it does not own.

## Consequences

- `internal/review` is new (`cli -> review`), embedding its own page: no CDN, no
  webfonts, a CSP that allows nothing off the machine.
- `internal/ratify` gains `GrantedStatus`, so the UI and the action share one
  table.
- **`nugit review` is the first nugit command that listens on a socket and the
  first that writes the store without an explicit id argument.** Both are
  confined to this command.
- The lane now has a surface proportional to the work, so the backlog is a
  sitting rather than a project.
- Plan approval is the obvious next tenant: beads have no approval state at all
  yet, and when they get one the same page is where it belongs.
