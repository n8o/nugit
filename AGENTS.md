# AGENTS.md — working in the nugit repo

## What this repo is

The thin keystone of nugit (see [PLAN.md](PLAN.md)). Go, no external services.
The whole engine is pure functions over two git refs; keep it that way.

## Build / test / run

```sh
go build ./...
go test ./...
go vet ./... && gofmt -l .          # must be clean
go run ./cmd/nugit pr-render -base main -head HEAD
```

## Conventions

- **`internal/model` imports nothing from sibling packages.** It is the type
  contract; keep it cycle-free.
- **The keystone is deterministic and LLM-free** (ADR-0006). Do not add network
  or model calls to the delta/consistency/significance path.
- **Keep `workspace.dsl` in sync with the import graph.** If you add a
  cross-package import, declare the matching `src -> dst` relationship in
  `.nugit/architecture/workspace.dsl`, or `nugit pr-render` will (correctly) fail
  its own c4↔code check. New package → add a `component` with `properties { paths }`.

## Capturing the why (commit trailers)

When a change carries a decision, append a trailer block (parsed by
`internal/trailers`). `learned:` and `keywords:` are mandatory when a block is
present:

```
feat(consistency): add spec-linkage check

symptom: two PRs cited SPEC-014 and a third cited SPEC-041, which no file in the store defines — nobody noticed for a month
decision: warn (not fail) when a commit references an unknown spec
rejected: fail — too aggressive while specs are sparse
learned: start consistency checks at warn; promote to fail once the corpus is dense
affects: consistency
keywords: consistency, spec, linkage
```

`symptom:` is optional but worth the line: it is what a distilled lesson's
**Trigger** is seeded from, and a Trigger is what a future debugger's query
matches. Write what you SAW, not what you did. With no `symptom:`, `nugit
distill` scavenges the commit body for an observation and, failing that, writes
a `TODO` you will have to fill in at review time — it never falls back to the
commit subject (ADR-0028).

Durable decisions become files under `.nugit/decisions/` (they survive
squash-merge; trailers do not — ADR-0005). Cross-reference other objects by their
stable **key** (`ADR-0002`), never by content hash (ADR-0001).

### `relates_to:` verbs — which ones do something

The vocabulary is declared in `internal/knowledge/relations.go` and classified
by what the engine actually reads (ADR-0041). `nugit doctor` reports the store's
mix; `pr-render` warns on a verb with no rule.

| verb | kind | what reads it |
|---|---|---|
| `amends:<key>` | semantic | `ResolveAmendedBy` — partial override, target stays live (ADR-0015) |
| `reinforces:<key>` | semantic | `ResolveReinforcedBy` — re-confirmation widens retrieval (ADR-0019) |
| `informs:<key>` | semantic | reference attribution in a retrieval bundle (ADR-0014) |
| `constrains:` / `affects:` / `governs:` `<component>` | scope | widens what the object is retrieved for |
| `prevents:` `satisfies:` `refines:` `elaborates:` `relates:` `see:` | annotation | retrieval's one-hop traversal, and nothing else |
| a bare id, no verb | annotation | the same traversal — a plain "see also" |

**Only the semantic verbs make a lifecycle claim.** An annotation verb is a
legitimate cross-reference that carries no further meaning, so reach for a
semantic one when you mean the graph to change.

`supersedes:` is **not** a `relates_to` verb — it is a top-level front-matter
field, and ADR-0003 derives effective status from it. Written as an edge it
parses, resolves, and declares no supersession.

## Before opening a PR

Run `nugit pr-render` against your base. Resolve any `fail` finding (an undeclared
architecture edge is a real layering violation, not a false positive). An
architectural change with no ADR will warn — add the decision record.
