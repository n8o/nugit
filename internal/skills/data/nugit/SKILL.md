---
name: nugit
description: Use when working on this codebase (or any repo with a .nugit/ store) — load scoped architectural memory before editing a file, respect the declared C4 architecture, and capture the "why" so the knowledge store fills itself. Trigger before non-trivial edits and when making a design decision.
---

# nugit — typed memory for this codebase

This repo carries its architecture, decisions, lessons, references (distilled
external sources), and glossary as typed files under `.nugit/`, validated on
every PR. Use them as memory instead of re-deriving context each session.

## Start of a session — claim one objective

A session works toward **one objective**: one bead in the plan store (ADR-0048).
The PR view opens with it, so the reviewer sees what the change is for. The
SessionStart hook tells you whether this branch already has one.

- **No objective yet:** find the bead for the task, or create one with
  acceptance criteria. Then run `nugit objective claim <bead-id>` on the task's
  branch. The commit-msg hook then adds `objective: <bead-id>` to every commit,
  so you don't have to write it. A branch named after its bead
  (`feat/<bead-id>-short-name`) needs no claim: the hooks read the id from the
  name. Each new branch needs its own objective, including one you start
  mid-session; the pre-edit hook reminds you once if a branch has none.
- **Stay on it.** If the user asks for unrelated work, say so and suggest a
  separate session on its own branch (with agent-deck:
  `agent-deck launch -t "<title>" -c claude -worktree <branch> -b <repo>`)
  instead of adding it to this PR. `objective-focus` warns on a PR that mixes
  objectives or changes files no objective commit touched.
- **Finish it in the PR.** Close the bead in the PR that completes it.
- `nugit objective` shows what this branch works toward.
- **When the user asks what was left unfinished**, run `nugit orphans` and
  present each item with its finish-or-drop choice. Carry out what they pick;
  never delete a branch or close a bead on your own.

## Before editing a file — load context

Call the **`context` MCP tool** (server: `nugit`) with the file path, or run:

```sh
nugit context -path <file> -task "<what you're about to do>"
```

It returns a scoped, budget-bounded bundle: the file's C4 component + its
dependencies, the in-scope decisions (with their **rejected** alternatives — do
not re-propose those), the active spec, matched lessons, matched references,
glossary, and any ephemeral working-memory notes. Read it before proposing a
change.

If the `nugit` MCP server isn't wired into the client yet, `nugit agent` is the
wiring path: `nugit agent -client claude-code -install` writes the
project-scoped `.mcp.json`; `nugit agent -client cursor|codex|opencode` prints
the snippet for that client plus where it goes.

To feed external research in (papers, vendor docs, standards): distill the
project-relevant claims into `.nugit/references/<slug>.md` (front-matter with
`type: reference`, `source:` URL, scope + keywords; `relates_to: [informs:<ADR>]`
for the decision it grounds) and land it as a PR — never paste the full document.

## While working — respect the architecture

- The component a file belongs to and its allowed dependencies are declared in
  `.nugit/architecture/workspace.dsl`. **Do not introduce a cross-component import
  the model doesn't declare** — `nugit pr-render` will flag it (`*<->code`). If a
  new dependency is genuinely needed, add the `src -> dst` relationship to the DSL
  in the same change.
- Run `nugit pr-render -C . -base <base> -head HEAD` before finishing to self-check
  (consistency green, significance, deltas). Exit non-zero = a finding to fix.
- **Answer the checklist.** When `context` marks a lesson `☑️ check:`, or the PR
  view lists a **Checklist** item, the file you are changing is covered by a
  question a past incident raised (ADR-0057). Answer it before you commit:
  change the code, or say in the PR why the question does not apply.
- **Do not lower a gate to get past it.** A gate's mode in
  `.nugit/config.yml` (`fail_on`, `c4.mode`, `context_gate`, `verify.gate`,
  and the rest) is read from the branch you merge into, not from yours
  (ADR-0055). Changing it in your branch does not take effect until it merges,
  and the PR view reports the change as its own finding. If a gate is wrong,
  say so to the user. Do not edit it in the same PR that trips it.

## Before committing — verify, and say how

- **Run the repo's verify skill before every commit.** Look for a skill named
  `verify` or `verify-*` under `.claude/skills/`, nearest the code you
  changed. It starts and drives the app the way a person would: a browser for
  a web change, a simulator for mobile, the service for a backend. Run it
  after your last edit. A check that ran before you changed the tree checked
  a different tree. If the repo has no verify skill, run its tests, build and
  linter, and tell the user that a verify skill would make the check real.
- **Say how the commit was verified.** With nugit's hooks installed, the
  commit-msg hook stamps `verified: e2e`, `tests` or `review` from what ran in
  your session, and `verified: none` when nothing did (ADR-0056). If you did
  not verify, say why in the message, as
  `verified: none — docs-only change`. Never write a kind you did not run.
  The hook cannot see a check you ran somewhere else, so a claim like that
  needs a note saying where it ran.
- **If the commit is blocked** (`verify.gate: enforce`), run the check, or
  give the reason. Do not retry the same commit unchanged. The gate lets a
  third attempt through so a wrong block cannot strand you, and the PR view
  shows that commit as released by the gate.

## When a check fails

- **Fix the cause, not the signal.** Never make a failing test pass by
  skipping it, adding a sleep, adding retries, or re-running it until it goes
  green. That teaches everyone to re-run instead of fix.
- **A flaky test gets the same treatment.** Find why it is flaky. If you
  cannot find the root cause, delete the test and record why: put a
  `symptom:` and a `decision:` in the commit trailer saying what you saw and
  what you tried. Then the deletion is a decision someone can review, not a
  test that quietly vanished.
- **Three rounds, then a human.** If a check is still failing after three
  rounds of fixes, stop. Tell the user which check it is, what you tried each
  round, what you saw, and what you now think the cause is. A fourth guess
  costs more than their five minutes.

## Before pushing — cheap checks first

Run the tests, the build, the linter and `nugit pr-render` locally before you
push or open a PR. CI and review are shared and slow. A failure you could
have caught locally spends other people's time and gets slower feedback to
you.

## Capture the "why" — so the store fills itself

- **Cross-reference with a verb that means something.** `relates_to:` accepts
  anything, and only some entries change the knowledge graph: `amends:<key>`
  (partial override), `reinforces:<key>` (re-confirmation), `informs:<key>`
  (a reference grounding a decision), and `constrains:<component>` (widens
  scope). Everything else — `prevents:`, `refines:`, `see:`, or a bare id — is a
  plain "see also" that retrieval follows and nothing else reads. `supersedes:`
  is a top-level front-matter field, **not** a `relates_to` verb; written as an
  edge it declares no supersession and both records stay live. `nugit doctor`
  reports which of your edges are load-bearing.

- **Accepted or proposed — say which you mean.** A record you write to
  capture a decision the user made, or one the change you are shipping
  implements, is `status: accepted` (a lesson: `active`). Use `status:
  proposed` only for an option that is still open: the PR view marks it
  "stays proposed after merge", and it governs nothing until someone ratifies
  it, so merging it records the option without deciding it. Never write
  `proposed` just to be modest; a decision that ships as proposed reads as
  undecided forever.

- Jot ephemeral findings mid-task: `nugit remember -text "..." -scope <component> -keywords a,b`
  (gitignored; surfaces in future `context` calls).
- For a deliberate decision, put a **trailer block** in the commit message so
  `nugit distill` promotes it into a durable ADR/lesson (survives squash-merge):

  ```
  symptom: <what was OBSERVED — the failure you saw, not the work you did>
  decision: <what was chosen>
  rejected: <the alternative and why not>
  learned: <a reusable lesson, if any>
  affects: <component(s)>
  keywords: <terms>
  ```

- `symptom:` is optional but high-value: it seeds the distilled lesson's
  **Trigger**, which is what a future debugger's search actually matches. Without
  it, `nugit distill` scavenges the body for an observation and otherwise writes
  a `TODO` for review — never the commit subject (ADR-0028).

- After merging a batch: `nugit distill -base <base> -head HEAD` writes the ADRs/
  lessons into the PR for review.

- **Turn a repeated miss into a checklist item.** When the same kind of bug
  has reached review in two or more PRs, and you can tell from a diff that a
  change risks it, write a lesson with a `diff_trigger:` and a `check:`
  (ADR-0057). From then on the PR view asks the question on every change that
  matches, and `context` asks it while the file is open:

  ```yaml
  diff_trigger:
    paths: ["internal/model/**"]   # where the risk lives
    added: 'json:"'                # optional: the line that signals it (RE2)
  check: does a record written before this field existed still load?
  ```

  Keep the trigger as narrow as the risk. A question that fires on every edit
  to a directory is one people learn to skip. `nugit stats -rules` names items
  that never fire, and lessons nobody is shown, so the store can be pruned.

## If this repo keeps a plan store (`.beads/`)

Skip this section if there is no `.beads/` directory.

`.beads/**/*.jsonl` records the plan being executed **now** — one `epic` per
step — and `nugit pr-render` renders it as **Plan position**. It is not a mirror
of the issue tracker: epics and the backlog live in GitHub, a bead exists
because someone is working that step this week, and the two are linked by naming
the issue in the bead's id or body.

- **Write multi-PR work down as beads before the first commit**, and **move the
  bead in the same PR that does the work.** A step closed in a later PR is a
  step nobody can tell was ever finished. Close it when the step is *done*, not
  when the enabling groundwork lands.
- **Name step ids `<plan>-<step>`** (`api-rewrite-3`, `gh-142-2b`). nugit groups
  a store into plans by that family and shows your PR only the plans it moved —
  other agents' plans are named, never merged into your position. A bare
  `<prefix>-<n>` id is its own plan of one.
- **The store is shared.** `bd` keeps one database per repository, resolved from
  the main checkout even when you run it from a worktree — so `bd export` from a
  worktree writes into the *main* checkout's branch, not yours, and other
  sessions' bead changes can ride along in your diff. Export into the checkout
  you are committing from, and never attribute store lines in a diff to the PR
  carrying them.
- **After exporting, before `git add`:**

  ```sh
  nugit plan normalize -write   # one stable line per bead — this is what stops
                                # concurrent agents conflicting on the store
  # sharded store + a local bd database that may be behind main:
  nugit plan normalize -split -merge HEAD -merge-base <previous export> -write
  nugit plan check              # what pr-render will say about it
  ```

- **Resolve a store conflict with `bd`, never by hand** — reset the file to the
  base's version, re-import, re-apply only your own closes, re-export,
  normalize. Hand-merging JSONL invents states no `bd` command produced.

## Quick reference

| Need | Command |
|---|---|
| Load memory for a file | `nugit context -path <file> -task "..."` |
| Architecture as a diagram | `nugit c4 render` |
| Why did a check fire? | `nugit explain <check>` |
| Claim / show this session's objective | `nugit objective claim <bead-id>` · `nugit objective` |
| What was started and left? | `nugit orphans` |
| Save a working note | `nugit remember -text "..."` |
| Promote trailers → ADRs | `nugit distill -base <base> -head HEAD` |
| The unified PR view | `nugit pr-render -base <base> -head HEAD` |
| How commits were verified; gate stats | `nugit stats` (verify section) |
| Rules worth pruning | `nugit stats -rules` |
| Lint / canonicalize the plan store | `nugit plan check` · `nugit plan normalize -write` |
