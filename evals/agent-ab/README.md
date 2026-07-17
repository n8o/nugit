# agent-ab — does `context()` change agent behavior?

An A/B harness for the outcome half of the measurement question ("rung 2";
rung 1 is the usage log + `nugit stats`, ADR-0013). It runs the **same coding
tasks** through a headless agent twice — once with the nugit MCP server wired,
once without — and scores every resulting diff with **`nugit pr-render -format
json` as a deterministic judge**. No LLM is needed to grade the primary
metrics.

## Hypothesis

An agent with scoped typed memory produces PRs with:

1. **fewer consistency findings** (`c4↔code` violations, stale-knowledge,
   decision-coverage) — it respects the declared architecture;
2. **fewer re-proposals of rejected alternatives** — the ADRs' `Rejected`
   sections actually steer it;
3. comparable or lower **cost/turns** — it re-derives less.

## Metrics (per run, one JSON line in `results/results.jsonl`)

| field | source | deterministic |
|---|---|---|
| `findings_fail` / `findings_warn` | `pr-render -format json` on the run's diff | ✅ |
| `context_calls` | the worktree's `.nugit/.cache/usage.jsonl` (needs nugit ≥ the usage-log feature) — **the manipulation check**: a "with" run that never called the tool tells you the wiring, not the memory, failed | ✅ |
| `cost_usd`, `num_turns`, `duration_ms` | `claude -p --output-format json` | ✅ |
| `committed` | whether the run produced a commit at all | ✅ |
| `reproposed` | optional `-j` LLM judge: rejected alternatives vs the diff | ❌ opt-in |

## Fairness rules

- Both arms get the **identical prompt** from `tasks.json`. The only differences
  in the "with" arm: `--mcp-config` exposing the nugit `context` tool, plus one
  system-prompt line directing the agent to call it before editing (mirroring
  how a repo wires nugit via CLAUDE.md — the treatment is *wired memory*, not
  just tool availability).
- Every run starts from a **fresh worktree at the same base ref**; runs never
  share state.
- Score only committed work; a run that produced no diff is recorded as
  `committed: false`, not silently dropped.
- Do at least `-n 3` runs per arm before reading anything into a difference;
  agent runs are high-variance.

## Usage

```sh
# 1. define tasks against a target repo that has a .nugit/ store (e.g. JBS)
cp tasks.example.json tasks.json && $EDITOR tasks.json

# 2. run both arms, 3 runs each, scoring as it goes
./run.sh -t tasks.json -r ~/Development/jeket/JBS -n 3

# 3. compare arms
./summarize.sh results/results.jsonl
```

Requirements: `claude` CLI, `nugit` on PATH, `jq`, and a target repo with a
`.nugit/` store. Runs are billed agent invocations — size `tasks.json × -n × 2`
accordingly.

## Joining usage to outcomes

Usage records carry the git branch they were served on (`branch` field; empty
when the repo dir isn't a git repo, `"HEAD"` when detached), which makes the
branch the join key between the two halves of the measurement question —
without any harness at all, on real day-to-day branches:

```sh
# per-branch context() usage (how much memory was pulled, per branch)
nugit stats -format json | jq '.by_branch'

# per-branch outcomes (what pr-render found on that branch's diff)
nugit pr-render -base <target> -head <branch> -format json | jq '.findings | length'
```

Correlate the two across branches: if branches with heavy `context()` use
consistently land with fewer findings than branches without, the memory is
earning its keep. Records also carry `references` (distilled external sources
served per bundle) so reference-heavy bundles can be sliced out separately.

All of this is local-only: the usage log lives in gitignored
`.nugit/.cache/usage.jsonl` and never leaves the repo — no telemetry
(ADR-0013).

## Caveats (read before quoting numbers)

- pr-render findings measure *architecture/knowledge conformance*, not task
  success. A run can be finding-free and still wrong. Pair with your own
  acceptance check per task (`verify` field: a shell command run in the
  worktree, e.g. a targeted test).
- If `c4.mode: warn` in the target repo, `c4↔code` violations appear as
  warnings — compare `findings_warn` too, not just `findings_fail`.
- The `reproposed` judge is an LLM grading an LLM: use it for triage, and read
  the transcripts (kept per run under `results/`) before believing it.
