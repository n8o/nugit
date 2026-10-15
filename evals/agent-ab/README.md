# agent-ab — does scoped memory change what an agent ships?

A **replay benchmark** for the outcome half of the measurement question
("rung 2"; rung 1 is the usage log + `nugit stats`, ADR-0013). It takes real,
already-merged tasks from a repo's history (`nugit export -format tasks`),
rewinds a leak-free clone to each task's **base**, has a headless agent redo
the task in up to four arms, and scores every run against what actually
shipped: the solution's **hidden tests**, file overlap with the real change, a
re-proposal heuristic, and `nugit pr-render -format json`. No LLM grades the
primary metrics.

## Arms

| arm | nugit tool | extra system prompt | measures |
|---|---|---|---|
| `none` | no (scrubbed control) | — | the baseline agent |
| `nugit` | yes, MCP `context` (binary from `-b`, default PATH) | one nudge line: call `context` before editing | wired memory as shipped: retrieval **and** delivery |
| `oracle` | no (scrubbed) | the exact decision files named by the task's `oracle_keys`, read **at base** | the knowledge ceiling: perfect retrieval, perfect delivery |
| `dump` | no (scrubbed) | every effective decision at base, capped at ~120k chars | brute force: no retrieval, everything delivered |

`-a none,nugit,oracle,dump` (default all four). The old names still work:
`without` = `none`, `with` = `nugit`.

- **oracle** finds each key's file by front-matter `id:` (or `key:`) anywhere
  under `.nugit/` at base (`git grep` on the base tree; content via `git show
  <base>:<path>`). A task with empty `oracle_keys`, or whose keys exist nowhere
  at base, is not run in this arm: a line with `skipped: "no-oracle"` is
  recorded instead.
- **dump** takes `.nugit/decisions/*.md` at base, drops any with `status:`
  superseded/deprecated/rejected or named by another file's `supersedes:`
  (ADR-0003), and appends whole files in path order until the next one would
  cross the cap (`DUMP_CAP`, default 120000). Records `dump_chars`,
  `dump_files`, `dump_truncated`.

### Reading the four arms together

- **oracle beats none, nugit doesn't** → the knowledge helps coding, and
  retrieval or delivery is the bottleneck. Check the manipulation columns
  first: 0 `context()` calls means the agent never asked; `over-limit` means it
  asked and got a file path instead of the bundle.
- **oracle doesn't beat none** → on these tasks the decision knowledge does not
  help coding, however well it is retrieved. No retrieval work will move the
  nugit arm; fix the knowledge or pick tasks where decisions matter.
- **dump ≈ oracle** → a big context window is doing retrieval's job at this
  store size; nugit's case rests on cost, turns and scale. **dump < oracle** →
  precision matters, which is what scoped retrieval is for.
- **nugit ≈ oracle** → retrieval is good enough; further gains are in the
  knowledge itself.

## Leak-free workspace

A replay is worthless if the agent can read the future. A `git worktree` of
the source repo shares its object database, so `git show <solution>` would
work. Each run instead gets a fresh clone (`clone-at.sh`):

1. a temp ref at the base commit is created in the source repo;
2. `git clone --no-local --no-tags --single-branch --branch <temp-ref>` copies
   only objects reachable from base;
3. the temp ref is deleted, the clone's `origin` remote removed, and its reflog
   (which names the source path) dropped;
4. **leak check**: `git -C <clone> cat-file -e <solution>` must fail. The run
   records `leak_check: pass|fail` (`n/a` for an old-format task with no
   solution), and the batch **aborts** on `fail`.

Every arm also gets the same `--disallowedTools`: `WebFetch`, `WebSearch`,
`Bash(gh:*)`, `Bash(git fetch:*)`, `Bash(git pull:*)`, `Bash(curl:*)`, plus
best-effort `Bash(git -C:*)` and `Read/Grep/Glob` of the source repo's path.
Clones live in a `mktemp -d` directory (`-w` to choose), outside any repo, so
Claude Code does not pick up a parent directory's CLAUDE.md.

Hidden tests are not in the clone during the run: a test the solution *added*
does not exist at base; one it *modified* is visible in its base version,
which is fair (it was there). Untracked files of the source checkout
(`node_modules`, build output) are not in the clone either; a task that needs
them should install them in `verify`.

### Fairness (unchanged from the two-arm harness)

- Identical prompt and model (`-m`) in every arm.
- **Every arm runs with `--strict-mcp-config`**, so the target repo's
  `.mcp.json` and any user-level MCP server are ignored identically; only the
  nugit arm's config names a server.
- The three no-nugit arms are **scrubbed** (`scrub-nugit.sh`, clone-local):
  `.mcp.json`, nugit-named files under `.claude/`, hook entries in
  `.claude/settings*.json` that call nugit (e.g. `nugit hook pre-edit`), every
  CLAUDE.md / AGENTS.md / `.claude/rules/*.md` block that mentions nugit, and
  `.nugit/` itself. A stub `nugit` that logs and exits 127 is first on the
  agent's `PATH`. The scrub is restored from base before the diff is scored.
  In the nugit arm, `nugit` on the agent's `PATH` is the `-b` binary, so
  CLAUDE.md-wired CLI calls and hooks use the same binary as the MCP server.
- **Contamination check** per run: `context_calls`, `stub_calls`, `mcp_calls`
  (nugit tool_use in the transcript), `contaminated` (any of them in a no-nugit
  arm). Residual leaks the harness cannot see: user-level CLAUDE.md, skills or
  settings outside the clone, identical across arms.

## Blind or tests-visible (`-T`)

By default the hidden tests are withheld: the agent sees only the task text, as
it stood at base. That is the purest replay, but it only scores tasks whose
tests go through names the task text reveals. Tests that call **internal**
functions by name (an unexported Go helper the real solution happened to add)
fail to compile for every arm, whatever the agent did, so a blind pass rate on
such tasks is ~0 and measures nothing.

`-T` commits the hidden tests into the clone before the run and tells the agent
to make them pass (the TDD variant). All arms get the same tests, so the paired
comparison stays fair; what it measures shifts from "can it infer the change"
to "can it implement the change the tests specify, and how cheaply, and does it
respect the architecture". Scores are computed against base + the committed
tests, so the tests never count as the agent's work, and they are re-applied
from the solution before scoring, so editing them gains nothing. Results carry
`tests_visible`. Check which tasks need it before a run: apply each task's
hidden tests at base — compile errors naming undefined identifiers mean blind
mode cannot score that task.

## Scoring (one JSON line per run in `results/results.jsonl`)

| field | what |
|---|---|
| `leak_check` | see above |
| `committed`, `cost_usd`, `num_turns`, `duration_ms`, `tokens`, `agent_ok` | `claude -p --output-format json` |
| `hidden_tests_result` (`pass`/`fail`/`unscored`/`none`), `_exit`, `_cmd` | after the agent finishes, the solution's `hidden_tests` blobs are written into the clone from the source repo and run: `verify` if set, else derived — Go → `go test ./<dir>/...`; `*.test\|spec.{ts,tsx,js,jsx}` → `npx vitest run` (if `vitest` is in package.json) else `npx jest`; `*.py` → `python3 -m pytest`; anything else → `unscored`. An old-format task with only `verify` is scored by it |
| `reference_pass` | the same hidden tests against **the real solution**, once per task (cached in `results/reference/`). A task whose reference fails is excluded from pass rates |
| `file_overlap`, `files_touched` | Jaccard of the agent's touched files vs `solution_files`, both without tests, `.nugit/` and `.beads/` |
| `reproposed_heuristic`, `reproposed_terms` | **heuristic** (`repropose.sh`): code-ish terms from each `rejected[].text` (backticked spans, CamelCase, snake_case, dotted names; ≥ 5 chars; not already in the base version of the touched files) found in the diff's added lines. Expect false positives and negatives |
| `findings_fail`, `findings_warn` | `nugit pr-render -format json` on base..HEAD |
| `context_calls`, `context_est_tokens`, `context_over_limit`, … | the nugit usage log for the run. The clone is its own repo, so nugit logs to the clone's `.nugit/.cache/usage.jsonl`; `usage-window.sh` reads it for the run's time window (pre-edit hook records are counted separately as `context_hook_edits`) |
| `oracle_chars`/`oracle_missing`, `dump_chars`/`dump_truncated` | what the oracle/dump arm was handed |
| `tags`, `model` | carried from the task and the run, for stratification |
| `reproposed` | optional `-j` LLM judge (spends quota) |

Per run, `results/runs/<task>-<arm>-<n>/` keeps the agent JSON, stderr,
transcript, diff, pr-render JSON, hidden-test log and the system prompt sent.

## Summaries

`./summarize.sh [results/results.jsonl]` prints:

1. **per arm**: runs, committed, hidden pass rate (over scored runs with a
   passing or unknown reference), mean overlap, re-proposals, findings, turns,
   cost, `context()` calls, over-limit bundles, contaminated and skipped runs;
2. **paired vs none**: the unit is a (task, model) pair, scored as the mean
   over its runs. For each arm: wins/losses/ties on hidden-test pass, an exact
   two-sided **sign test** p-value, and the mean paired Δ overlap;
3. the same paired table **stratified** by `decision_density` (0 / 1–5 / 6+),
   `kind`, `has_rejected` and model, with every stratum under 10 pairs marked
   **"hint, not result"**;
4. the **manipulation check**: nugit runs with 0 `context()` calls or an
   over-limit bundle, contaminated control runs, agent failures, failed
   references, leak failures, oracle skips, dump truncations.

## Usage

```sh
# 1. export replay tasks from the target repo (a .nugit/ store + plan history)
nugit export -C ~/src/target -format tasks -o tasks.json

# 2. pilot: 10 tasks with hidden tests, all four arms, one run each
./run.sh -t tasks.json -r ~/src/target -f '.tags.has_hidden_tests' -l 10 -m sonnet \
         -b ~/src/nugit/nugit
#   -n 3         runs per arm        -a none,oracle   a subset of arms
#   -k           keep clones          -w dir           where clones go
#   -Q5 80 -Q7 50  stop cleanly (exit 0) when agent-deck reports the five-hour
#                window >= 80% or the seven-day >= 50% (skipped if agent-deck
#                is not installed)   -g cmd  any extra guard; non-zero stops
# 3. compare
./summarize.sh
```

Requirements: `claude`, `git`, `jq`, a nugit binary (PATH or `-b`), and
whatever the hidden tests need (`go`, `npx`, `python3`).

### Sample size and cost

- Aim for **30–50 paired tasks** (tasks × models where both arms are scored)
  before reading the overall sign test; with fewer, only a lopsided result
  means anything. A stratum under 10 pairs is a hint for the next experiment,
  not a result.
- `-n 1` over more tasks beats `-n 3` over fewer for the paired test; use
  repeats to measure run-to-run variance on a handful of tasks.
- Cost ≈ tasks × arms × runs agent sessions (oracle skips tasks without
  `oracle_keys`), plus one reference test run per task. Pilot on 5–10 tasks
  with the cheapest model to measure per-run cost and turns, then size the full
  run. The dump arm's system prompt can be ~30k tokens per turn; it is usually
  the most expensive arm.

## Was the context actually delivered?

Claude Code ([MCP output limits](https://code.claude.com/docs/en/mcp)) warns
above 10,000 tokens, caps tool output at `MAX_MCP_OUTPUT_TOKENS` (default
25,000), and, for a tool that does not declare `anthropic/maxResultSizeChars`,
saves any result over 50,000 characters to a file and hands the agent a
message naming the file. nugit sizes a bundle at runes÷4 per token, so a bundle
near a 24,000-token budget is ~96,000 characters: under the token cap, over the
character threshold. `context_over_limit` is set when either threshold is
crossed. Treat an over-limit nugit run as a measurement of an agent that was
handed a file path, not memory in context.

## Smoke test

`test/smoke.sh` builds a throwaway Go repo (a decision store, a base commit, a
solution commit adding a hidden test) and runs all four arms with a **fake
`claude`** and a fake `agent-deck` (`test/fake-bin/`) on PATH — no quota is
spent. It asserts the leak check (including a negative case), hidden-test
application and scoring, the reference check, oracle/dump delivery (and that
dump drops a superseded decision), the scrub, the usage count from the clone's
log, the heuristic, `summarize.sh`, and the quota guard.

## Joining usage to outcomes outside the harness

Usage records carry the git branch they were served on, so day-to-day branches
can be compared without any harness: `nugit stats -format json | jq
.by_branch` against `nugit pr-render -base <target> -head <branch> -format json`
per branch. All local-only: the usage log is gitignored and never leaves the
repo (ADR-0013).

## Caveats (read before quoting numbers)

- Hidden tests measure whether the agent's code satisfies the tests the real
  change shipped with, written against the real implementation's API. An agent
  that solved the task with a different, reasonable API fails them. Paired
  comparison cancels this across arms; absolute pass rates understate success.
- pr-render findings measure architecture/knowledge conformance, not task
  success. If `c4.mode: warn`, compare `findings_warn` too.
- `reproposed_heuristic` is a term match and `reproposed` (`-j`) an LLM
  grading an LLM: read the transcripts before believing either.
