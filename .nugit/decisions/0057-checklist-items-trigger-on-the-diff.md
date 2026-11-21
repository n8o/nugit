---
schema_version: 1
id: ADR-0057
type: decision
scope: global
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:checklist
  - constrains:knowledge
  - constrains:retrieval
  - constrains:engine
  - constrains:render
  - amends:ADR-0020
  - informs:ADR-0035
  - informs:ADR-0032
provenance:
  commit: seed
  citation: "Claude Code team, 'How the Claude Code team thinks about verification' (2026-11): a reliability checklist of 39 items, each with a trigger that can be read from a diff, a check, and the incidents or PRs behind it; a diff usually matches 3 to 10"
confidence: medium
---

# ADR-0057 — Checklist items trigger on the diff

## Context

The Claude Code team keeps a reliability checklist of 39 items. Each item has
three parts: a trigger that can be read from a diff, a check, and the
incidents or PRs behind it. One is "`--resume` loads old transcripts". When
session storage or message types change, they ask whether a new field loads
safely from transcripts written before it existed. The reviewer checks only
the items whose trigger matches, usually 3 to 10. Nobody has to remember every
past outage, and nobody has to read 39 questions to find the three that
apply.

nugit's lessons are the place these items belong. A lesson already has a
`**Trigger:**`, an insight and provenance. But its trigger is prose, so only a
reader can match it. ADR-0020's `applies_to_paths` goes part of the way: it
binds a record to files, so context() serves it for them and stale-knowledge
watches them. A path is too coarse for a checklist item, though. "A field
was added to a serialized type" lives in a few lines of `model/`, not in
every change to it, and an item that fires on every edit to a directory is
one people learn to skip.

## Decision

1. **A checklist item is a lesson with a `diff_trigger:` and a `check:`.**

   ```yaml
   diff_trigger:
     paths: ["internal/model/**"]   # required; applies_to_paths dialect
     added: 'json:"'                # optional RE2, tested on added lines
     removed: 'func Retry\('        # optional RE2, tested on removed lines
   check: does a record written before this field existed still load?
   ```

   The trigger matches when a changed file is under `paths` and, if a line
   pattern is given, an added line matches `added` or a removed line matches
   `removed`. With no pattern, any change under the paths matches. `check:`
   is the question to answer. The incidents behind the item are its
   provenance and `relates_to`, as for any record. It is a field on a lesson,
   not a new kind, so it is retrieved, ranked, superseded, reinforced and
   ratified the way every lesson already is.

2. **pr-render lists the matched items** in a *Checklist* section, always
   open. Each entry shows the item's question and the first evidence: the
   file and, for a line pattern, the line. The items are not findings, since
   each one is a question, not a verdict. The structured JSON carries them for
   a reviewer agent.

3. **The diff is read once and bounded.** The engine reads the range's diff
   only for changed files some item's paths match, and only when some item
   tests lines. Past 500 candidate files it matches on paths alone, which can
   only over-report.

4. **context() serves an item for a file under its trigger paths**, whatever
   the task says, with `check` set on the item and shown in the text bundle.
   The file being in play is enough. Whether the change matches the line
   pattern is decided at PR time, from the diff.

5. **An item becomes every repo's through the org hub (ADR-0035).** An item
   read from the configured `org.hub` peer fires in this repo, at PR time
   (read from the hub's checkout, the way contracts are) and in context(), if
   it is admissible there: global and ratified. Promotion is the review step
   that says its paths are written for any tree. Any other peer's trigger
   paths name files in that peer's tree, so they never fire here (ADR-0032).

6. **A broken item is reported.** An item with no paths, an invalid glob or
   RE2 pattern, or no `check:` is a `model-health` warn when the PR adds or
   modifies it, and `nugit doctor` reports the whole store.

## Rejected

- **A new `checklist` record kind.** Every lesson path (retrieval, effective
  status, reinforcement, ratification, promotion, distill) would need to learn
  it. An item *is* a lesson whose trigger a machine can read.
- **Extending `applies_to_paths` with line patterns.** It would change what
  ADR-0020's binding means for stale-knowledge and for every decision that
  uses it. A separate field keeps both meanings exact.
- **Making a match a warn finding.** It would turn every matched question
  into a red or yellow mark, and under `fail_on: warn` into a failed build.
  The checklist asks; the reviewer answers.
- **Matching lines in context().** At edit time the change has not been
  made. Serving the item while its file is in play puts the question in front
  of the agent before it writes the line.
- **Firing items from every peer.** A peer's `internal/**` is not this
  repo's. Only a hub item has been promoted on the claim that its paths are
  general.
- **Generating items with a model**, by mining review comments and reverts
  and having agents try to disprove a candidate. That belongs in a skill or
  a repo's own tooling. nugit gives the result a slot. The deterministic path
  stays model-free (ADR-0006).

## Consequences

- New `internal/checklist` component: `engine -> checklist`,
  `checklist -> {knowledge, model}` are declared in `workspace.dsl`.
  `FrontMatter` gains `diff_trigger` and `check`. `knowledge` gains
  `TriggerCovers` and `InvalidTriggers`.
- The PR view gains a section that is empty for every repo that writes no
  items, so nothing changes for them.
- Two items seed nugit's own store, both from traps hit while building
  ADR-0055 and ADR-0056: a new config knob is governed unless it describes the
  tree, and a new usage-log source must not count as a context() call.
- A path-only item behaves like `applies_to_paths` that also shows up in
  the PR view. That is the cheap way to start an item, and a line pattern can
  be added once the item turns out to be noisy.
