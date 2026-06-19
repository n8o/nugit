---
name: nugit
description: Use when working on this codebase (or any repo with a .nugit/ store) — load scoped architectural memory before editing a file, respect the declared C4 architecture, and capture the "why" so the knowledge store fills itself. Trigger before non-trivial edits and when making a design decision.
---

# nugit — typed memory for this codebase

This repo carries its architecture, decisions, lessons, and glossary as typed
files under `.nugit/`, validated on every PR. Use them as memory instead of
re-deriving context each session.

## Before editing a file — load context

Call the **`context` MCP tool** (server: `nugit`) with the file path, or run:

```sh
nugit context -path <file> -task "<what you're about to do>"
```

It returns a scoped, budget-bounded bundle: the file's C4 component + its
dependencies, the in-scope decisions (with their **rejected** alternatives — do
not re-propose those), the active spec, matched lessons, glossary, and any
ephemeral working-memory notes. Read it before proposing a change.

## While working — respect the architecture

- The component a file belongs to and its allowed dependencies are declared in
  `.nugit/architecture/workspace.dsl`. **Do not introduce a cross-component import
  the model doesn't declare** — `nugit pr-render` will flag it (`*<->code`). If a
  new dependency is genuinely needed, add the `src -> dst` relationship to the DSL
  in the same change.
- Run `nugit pr-render -C . -base <base> -head HEAD` before finishing to self-check
  (consistency green, significance, deltas). Exit non-zero = a finding to fix.

## Capture the "why" — so the store fills itself

- Jot ephemeral findings mid-task: `nugit remember -text "..." -scope <component> -keywords a,b`
  (gitignored; surfaces in future `context` calls).
- For a deliberate decision, put a **trailer block** in the commit message so
  `nugit distill` promotes it into a durable ADR/lesson (survives squash-merge):

  ```
  decision: <what was chosen>
  rejected: <the alternative and why not>
  learned: <a reusable lesson, if any>
  affects: <component(s)>
  keywords: <terms>
  ```

- After merging a batch: `nugit distill -base <base> -head HEAD` writes the ADRs/
  lessons into the PR for review.

## Quick reference

| Need | Command |
|---|---|
| Load memory for a file | `nugit context -path <file> -task "..."` |
| Architecture as a diagram | `nugit c4 render` |
| Why did a check fire? | `nugit explain <check>` |
| Save a working note | `nugit remember -text "..."` |
| Promote trailers → ADRs | `nugit distill -base <base> -head HEAD` |
| The unified PR view | `nugit pr-render -base <base> -head HEAD` |
