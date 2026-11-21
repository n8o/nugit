---
schema_version: 1
id: ADR-0058
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:hygiene
  - constrains:usage
  - constrains:gitutil
  - amends:ADR-0013
  - informs:ADR-0057
  - informs:ADR-0019
provenance:
  commit: seed
  citation: "Claude Code team, 'How the Claude Code team thinks about verification' (2026-11): 'Test suites, verification steps and .md files only grow if you let them, so we tidy them up on a routine'; the ADR-0013 usage log recorded how many lessons a context() call served, never which"
confidence: medium
---

# ADR-0058 — Rules are pruned on evidence: unused, pointing at nothing, or never fired

## Context

A knowledge store only grows. Capture adds lessons (ADR-0018), recurrence
reinforces them (ADR-0019), and checklist items join them (ADR-0057). Nothing
in nugit ever says a rule has stopped earning its place. A lesson nobody is
shown and one served every day cost a reader the same when they skim the
store. A checklist item whose trigger has not matched a change in a year
either guards something that no longer happens, or has a trigger written too
narrowly to ever fire. The Claude Code team tidies its rules on a routine,
because rule sets only grow if you let them. That routine needs a list to
start from.

The evidence already exists, except for one gap. The ADR-0013 usage log
records each context() call, but only *how many* lessons it served, never
*which*. Git history has every change a checklist trigger could have matched.
The tree says which paths still exist.

## Decision

1. **Usage records name what they served.** `served` lists the ids of a
   bundle's decisions, lessons, references, contracts and spec, with peer ids
   qualified. Records written before this have none. The report says from
   when its retrieval data runs, and never treats the absence of data as
   evidence of disuse.

2. **`nugit stats -rules [-days 90]`** reports the store's live local lessons
   (a checklist item is a lesson with a diff trigger) under three headings:
   - **unused in N days:** served by no context() call in the window, and,
     for a checklist item, matched by no commit in it. A rule younger than
     the window is not judged. The list is **held** until the log's served
     ids cover the whole window: while the earliest one is inside it, the
     report gives how many days of the window the log covers, how many rules
     are unserved so far, and the date it will cover the window, and names
     none. That includes checklist items, since unused means neither served
     nor matched. (A pilot run named every rule "unused in 90 days" from less
     than a day of served ids.)
   - **dead paths:** an `applies_to_paths` or `diff_trigger.paths` glob that
     matches no tracked file, or a backticked file path the body cites that
     is no longer in the tree. To keep prose and other repos' paths out, a
     cited path is checked only when its top-level directory exists here.
   - **never fired:** a checklist item that no commit since it was written
     has matched.

   Matching replays each item's trigger (ADR-0057's matcher) over the
   non-merge commits on origin's default branch (else HEAD). The scan is
   limited to the triggers' literal path prefixes and capped at 2,000
   commits, and the report says when the cap was hit.

3. **The report names candidates and changes nothing.** Each heading carries
   its remedy: supersede or merge an unused rule, re-point or retire a dead
   one, loosen or delete a silent item. The pruning itself is a person's PR,
   like any other store change (ADR-0016).

## Rejected

- **Auto-retiring rules** (marking them `deprecated` past a threshold). A
  rule can be unused because it worked. The incident it guards against has
  not recurred, and that is the rule doing its job. The report shows the
  evidence; whether the rule stays is a judgement call.
- **Putting it in `doctor`.** Doctor is a pre-flight that answers "is nugit
  set up correctly?" in seconds. A history replay is a routine someone runs
  on purpose, and an advisory line that names 30 lessons on every run is the
  kind people learn to skip.
- **Recording matches at PR time** and reading them back. pr-render runs in
  CI and the usage log is local, so the matches would have to be written
  somewhere committed. Replaying history gets the same answer from the git
  history every checkout already has.
- **Scanning cited paths in every backtick.** `a/b` in prose and another
  repo's `internal/...` would read as dead. The top-level filter costs one
  map lookup and removes both.

## Consequences

- The usage log grows by one id list per call. It is still local and
  gitignored.
- New `internal/hygiene` component: `cli -> hygiene` and `hygiene ->
  {knowledge, checklist, gitutil, usage, model}` are declared in
  `workspace.dsl`. `gitutil` gains a bounded `LogPatch`.
- "Unused" stays empty until the log has a window of served ids. That is the
  honest state of a log that has not recorded them for that long.
- Decisions are not judged. A decision is in force whether or not anyone is
  shown it, and ADR-0003's supersession is how one stops applying.
