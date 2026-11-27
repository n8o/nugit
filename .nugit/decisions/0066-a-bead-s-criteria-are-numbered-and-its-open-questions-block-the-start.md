---
schema_version: 1
id: ADR-0066
type: decision
scope: objective
status: accepted
created: 2026-11-27T00:00:00Z
relates_to:
  - amends:ADR-0048
  - amends:ADR-0049
  - constrains:objective
  - constrains:claim
  - constrains:render
provenance:
  commit: seed
  citation: "evaluation of github/spec-kit (v1.4.5, 2026-11-25): its requirement ids and clarify step are prompts an LLM judges; its own issues report ids renumbering on edit (#4065) and specs drifting from code (#620, #1191)"
confidence: medium
---

# ADR-0066 — A bead's criteria are numbered, and its open questions block the start

## Context

A bead is the spec nugit has. ADR-0048 made it the objective a PR names, and
the PR view prints its `acceptance_criteria` as one truncated line: "done
when: …". Nothing says which criteria this PR meets, which an earlier PR met,
or which are still open when the bead is closed. A bead closed with a
criterion unmet looks the same as one that met them all.

A bead also starts too early. An agent that claims a bead with an unsettled
question either asks (and the answer lives only in that session's
transcript) or guesses (and the guess ships). Nothing in the store marks the
question, so the next session on the same bead meets it again.

spec-kit has both ideas as prompts: requirement and success-criterion ids
(`FR-001`, `SC-001`) that an LLM `converge` step judges against the code, and
`[NEEDS CLARIFICATION: …]` markers that a `/clarify` step resolves. Neither is
checked by anything deterministic, its ids renumber when a spec is edited
(issue #4065), and its specs drift from the code (#620, #1191). The ideas are
sound; nugit can keep them in the bead and check them from git.

## Decision

### Numbered criteria

1. **A criterion is a line of the bead's `acceptance_criteria` that starts
   with `AC-<n>:`** (a leading `- ` is allowed). A bead whose criteria carry
   no such line renders exactly as today; numbering is opt-in per bead.
2. **A criterion's id is `<bead-id>/AC-<n>` and never changes.** Editing a
   criterion keeps its number; removing one leaves a gap; a new one takes the
   next unused number. A number is never reused for a different criterion.
3. **A commit says which criteria it meets with a `meets:` trailer**:
   `meets: AC-1, AC-3`. The ids belong to the commit's `objective:`. Like
   `objective:` and `verified:` it is not capture, so it needs no `learned:`.
4. **The Objective section lists each criterion** as met in this PR (the
   commits that say so), met earlier (commits on the target branch that name
   the same objective and meet it), or open. The scan of earlier commits is
   bounded to the commits naming the objective, found by trailer.
5. **`objective-focus` warns** (never fails) when:
   - a `meets:` names a criterion the bead does not have, or a commit with
     `meets:` names no objective;
   - the PR closes the bead while a numbered criterion is still open. The
     warning names each open criterion; closing anyway is allowed, and the
     reviewer sees what was left.

### Open questions

6. **An open question is `[NEEDS CLARIFICATION: <question>]`** anywhere in
   the bead's title, description or acceptance criteria. The marker is
   spec-kit's, so a bead written by either tool reads the same.
7. **`nugit objective claim` refuses a bead with open questions** and prints
   them, so the agent asks the user before any code. `-force` claims anyway and
   the claim records how many questions were open. The SessionStart digest and
   the pre-edit reminder list the questions for a branch whose objective came
   from its name or its commits.
8. **A question is answered in the store**: the bead is edited, the marker
   replaced by the answer, in the PR that acts on it. The answer is then in
   the bead's history, not in a transcript.
9. **The PR view shows open questions** in the Objective section, and
   `objective-focus` warns when a PR closes a bead that still carries one.

## Rejected

- **Judge criteria against the code with a model** (spec-kit's `converge`).
  Not deterministic (ADR-0006), and it answers "does this look done", which
  the reviewer already answers. nugit's job is to show which criteria the
  change claims and which nobody has claimed.
- **Infer met criteria from the files a PR changes.** A file says nothing
  about which sentence it satisfies; a wrong inference is worse than an open
  line.
- **Separate requirement files** (`specs/NNN/spec.md` with `FR-001`). The bead
  already holds the criteria and the PR already names the bead; a second
  artifact is a second thing to drift.
- **Store-wide criterion numbers.** Concurrent branches would collide on them,
  as ADR numbers did (ADR-0065's context). Numbers local to the bead cannot.
- **Fail a PR that closes a bead with open criteria.** Dropping a criterion
  can be the right call; the view makes it visible instead.
- **Cap a bead at three questions** (spec-kit caps `/clarify` at three). The
  count is not the problem; an unanswered one is.

## Consequences

- `trailers` gains `meets:`; `beads` gains criterion and marker parsing;
  `objective` gains the per-criterion status; `claim` refuses on markers.
- Agents write `AC-<n>:` criteria when they create a bead and `meets:` when a
  commit satisfies one; the skill says so.
- greenhouse's dispatcher should skip a bead with open questions and surface
  them instead of starting a session; that change is greenhouse's own.
- Existing beads keep working unchanged until someone numbers their criteria.
