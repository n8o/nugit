# nugit

> Git-native typed knowledge + a unified PR view. Every PR shows how the
> architecture is changing, what the code is changing, what the project's
> knowledge is changing, where this sits in the plan, and **why** — computed from
> typed artifacts that live in the same git history as the code.

This repository is the **thin keystone**: the one feature with clear ROI shipped
first, on the cheapest substrate, per the re-shaped plan ([PLAN.md](PLAN.md)).

## What works today

`nugit pr-render` computes, deterministically and with no external datastore:

1. **C4 delta** — structural diff of `.nugit/architecture/workspace.dsl`.
2. **Code delta** — `git diff` grouped by C4 component via `properties { paths }`.
3. **Knowledge delta** — added/changed/superseded decisions, specs, lessons, with
   each decision's `Rejected` rationale.
4. **Plan position** — from `.nugit/plan.yml` (a stand-in until Beads lands).

…then runs **cross-artifact consistency checks** that make the view *verify*
rather than present:

- **c4↔code** (`fail`) — code introduced an import edge between two components that
  the model does not declare. *Computed from the real import graph via `go/parser`.*
- **stale-knowledge** (`warn`) — a PR touches code governed by a
  `superseded`/`invalidated` object without updating it.
- **decision-coverage** (`warn`) — an architectural change with no ADR or
  `decision:` trailer.
- **spec-linkage** (`warn`) — a commit claims a spec that does not exist in-tree.

…and gates disclosure by **significance** (trivial / feature / architectural).

## Quickstart

```sh
go build -o nugit ./cmd/nugit

# render the PR view for the current branch vs a base
./nugit pr-render -C . -base main -head HEAD                # markdown (default)
./nugit pr-render -base main -head HEAD -format check-run   # GitHub Checks JSON
./nugit pr-render -base main -head HEAD -format json        # structured deltas for agents
```

Exit code is non-zero when a finding reaches `-fail-on` severity (default `fail`),
so it drops straight into CI.

## nugit models itself

`.nugit/architecture/workspace.dsl` is a C4 model of nugit's own packages, with
each component bound to its source via `properties { paths }`. The c4↔code check
therefore validates nugit's own import graph — the "bootstrapping spike" from the
plan. Add an import that violates the declared architecture and `nugit pr-render`
fails the check naming the offending edge.

## Layout

```
cmd/nugit/            CLI entrypoint
internal/
  model/              shared types (no internal deps — the contract)
  gitutil/            git plumbing
  trailers/           commit-trailer parser
  goimports/          Go import analysis (the spike)
  c4/                 Structurizr-DSL-subset parser + structural diff
  mapping/            file→component glob resolver
  knowledge/          .nugit/** reader + supersede-graph status
  delta/              the four deterministic deltas
  consistency/        cross-artifact checks
  significance/       disclosure-tier heuristic
  render/             markdown / check-run / JSON
  engine/             orchestration: two refs in, a Report out
.nugit/               nugit's OWN knowledge (model, decisions, spec, glossary)
docs/ · PLAN.md       the re-shaped plan and design decisions
```

## Design decisions

Key decisions (and the review findings they resolve) live as real nugit
knowledge objects under [`.nugit/decisions/`](.nugit/decisions/). Start with
[ADR-0004](.nugit/decisions/0004-thin-keystone-first.md) (why the keystone ships
first) and [ADR-0002](.nugit/decisions/0002-file-to-component-binding.md) (the
file→component primitive).

> Module path is a placeholder (`github.com/burrowfarm/nugit`); change it in
> `go.mod` + imports when the real remote is set.
