---
schema_version: 1
id: ADR-0043
type: decision
scope: global
status: proposed
created: 2026-10-15T00:00:00Z
relates_to:
  - constrains:retrieval
  - constrains:mcp
  - constrains:usage
  - constrains:config
  - amends:ADR-0024
  - informs:ADR-0013
  - informs:ADR-0042
provenance:
  commit: seed
  citation: "a large pilot repo, 2026-10: context() on one busy component file at budget_tokens=24000 returned ~24k estimated tokens, ~96,000 characters, 76-86 decisions; Claude Code docs (MCP output limits): results over ~50,000 characters are persisted to a file and the model receives a path, a warning fires above 10,000 tokens, MAX_MCP_OUTPUT_TOKENS defaults to 25,000"
confidence: high
---

# ADR-0043 — A bundle must fit the window it is delivered through, and stop when relevance does

## Context

Agents commonly call the MCP `context` tool with `budget_tokens: 24000`. On a
large pilot repo, one busy component file returned about 24k estimated tokens:
about **96,000 characters** carrying 76 to 86 decisions.

That bundle never reached the agent. Claude Code persists any MCP tool result
over about 50,000 characters to a file and hands the model only the file path.
It also warns above 10k tokens, and `MAX_MCP_OUTPUT_TOKENS` caps at 25k by
default. The agent did what it was told, nugit served what was asked, and the
knowledge stayed on disk. The usage log recorded the call as served, so the
failure was invisible from our side too.

There are two separate faults here.

**1. The budget measured the wrong thing.** `budget_tokens` bounds an
*estimate*: content runes / 4. The bytes on the wire are the bundle's JSON
encoding. Field names, quoting, the `dropped` list, and escaping all add to it,
and `json.Marshal` writes each `<`, `>` and `&` as six characters. A store full
of `a -> b` arrows can blow past any char limit while staying under its token
budget. And whatever the estimate, the requested budget was honoured as asked,
so a large request was a large result.

**2. The bundle filled to the budget, not to relevance.** ADR-0024 made path
history the section that "spends budget the typed sections left unused". The
typed sections had the same posture by accident: every in-scope decision was
admitted, sorted, and cut only when the budget ran out. With no task, every
global decision is in scope for every path. Past the first dozen, the sort has
no signal left: every remaining item is "same scope, next id". Eighty
decisions for one file is the whole store, not scoped context.

Checking the sort also turned up a gap. A superseded or invalidated decision
reached through a `relates_to` edge was excluded, as ADR-0003 intends. The same
decision reached *directly*, because it was scoped to the component, was
served. It was labelled superseded and sorted last, but it was still there.

## Decision

**1. The MCP result has a hard ceiling: 40,000 characters, measured on the
bytes sent.** That leaves a 10k margin under the client's file-persistence line.
The MCP server passes two limits to retrieval:

- `MaxBudgetTokens = 8000` clamps the requested budget from above. 8000
  estimated tokens is about 32k characters of content, which leaves room for
  framing.
- `MaxChars = 40000` is the guarantee. After truncation the bundle is
  JSON-encoded exactly as the server will send it. If it is over the ceiling,
  the budget shrinks by a fifth and the truncation re-runs from the untruncated
  bundle, until it fits.

This is deterministic: the same store gives the same bytes. Shrinking stops at
200 tokens. Below that only the mandatory c4+spec baseline is left, and that is
never cut.

When the budget is clamped, the bundle says so in `notices` ("budget clamped
from 24000 to 8000 to fit the MCP output window"), sets `clamped` and
`requested_budget_tokens`, and the usage record carries `clamped`,
`requested_budget`, and `chars` (the served size). A bundle that does not fit
is now visible in `nugit stats` input instead of passing as served. The
`dropped` list is capped at 20 entries plus a "+N more" line, with the true
count in `dropped_count`, so the list of cuts cannot outgrow the bundle.

**The CLI path is not clamped.** `nugit context -budget N` is mostly a human at
a terminal asking for N, and a terminal has no 50k-character file cliff. An
agent reading through Bash meets that tool's own output limit, and the relevance
caps below already bound the bundle to a few tens of kilobytes whatever the
budget. Clamping the CLI would override an explicit human request to defend
against a window that path does not have.

**2. Each section is filled by relevance, then capped.** Every directly
in-scope item gets an ordinal rank, strongest first:

1. bound to the exact path (`applies_to_paths`) or by the org landscape
2. scoped to the path's component
3. scoped to the component's parent container
4. global
5. pulled in by a one-hop `relates_to` edge

Within each tier, an item that matches the task's keywords outranks one that
does not. Origin stays the outermost dimension: local before peer (ADR-0032).
The rest of the existing order (status, then qualified id) breaks ties.

Each section is capped *before* the budget is applied: **12 decisions, 8
lessons, 6 references**. Contracts are never capped, because they are
obligations and are already bounded by "contracts that named this repo". Twelve
decisions, each with its rejected rationale, come to about 14k characters at the
pilot's ~1.2k characters per decision, about a third of the window, which leaves room for
everything else. It is also as many standing constraints as an agent can
actually weigh for one edit. Past that, the order is id order, not relevance.
The caps are overridable under `retrieval: {max_decisions, max_lessons,
max_references}` in `config.yml`. There is no "unbounded" value; zero means the
default.

What the cap cuts is counted by kind in `omitted` (and summed into the usage
record) and named in a notice: "N more in-scope decisions omitted (relevance
cap); call with a narrower path or task". That is the actionable next move, not
a silent cut.

**The cap is also the floor for traversal.** Only items that survive the cap
seed the one-hop pull and the reverse `informs:` pass. A hop from a record too
weak to show is weaker still, and admitting it would let the omitted tail back
in through the side door.

**3. A superseded or invalidated decision, lesson, or reference is never live
context**, whether it is reached directly or through an edge. The direct path
now applies the rule the traversal always applied.

All of this is ordinal ranking over declared structure. There is no score,
model call, or embedding (ADR-0006).

## Rejected

- **Rely on `MAX_MCP_OUTPUT_TOKENS`.** It is a client-side setting in the
  agent's environment, so it is not ours to set and we cannot see it. It also
  only raises the token cap: the client still persists any result over about
  50,000 characters to a file, so raising it leaves this failure exactly as it
  was.
- **Paginate the bundle (a cursor, a `page` argument).** Agents do not page. An
  agent that got a first page and a cursor has what it believes is "the
  context" and moves on. Ranking so that the first page *is* the context is the
  same effort and it actually gets read.
- **Lower the default budget only.** The default (4000) was never the problem.
  The failing calls passed 24000 explicitly, and any requested budget can still
  overflow. A default bounds the case nobody is in.
- **Bound the char size by the token estimate alone (clamp, no measurement).**
  The estimate does not count JSON framing or escaping, and escaping alone can
  inflate a rune sixfold. A ceiling has to be enforced on the bytes.
- **A numeric relevance score with a threshold.** It invites tuning a magic
  number per repo, and the tiers are already the only signal there is. A cap
  over ordinal tiers says the same thing without pretending to precision.
- **Clamp the CLI too.** It is a different window with no file cliff, and an
  explicit human `-budget` should be honoured. See the Decision.

## Consequences

- An MCP `context` result is always under 40,000 characters, so it reaches the
  model inline. Large requested budgets are clamped, visibly.
- Bundles on dense components shrink from "the whole store" to the 12 most
  relevant decisions, and the footer says how many more exist and how to
  narrow. Small stores are unchanged: nothing is clamped at the default budget
  and nothing is omitted.
- A component-scoped superseded decision no longer appears in its component's
  bundle. Its superseder does.
- The usage log gains `clamped`, `requested_budget`, `chars`, and `omitted`
  (all omitempty, so old records still parse). Delivery size becomes a
  measurable property of served calls instead of something guessed after the
  fact.
