---
schema_version: 1
id: LESSON-a-new-usage-record-source-must-not-count-as-a-context-call
type: lesson
scope: usage
status: proposed
created: 2026-11-21T00:00:00Z
relates_to:
  - constrains:usage
  - informs:ADR-0056
  - informs:ADR-0042
provenance:
  commit: seed
  citation: "nugit, 2026-11-21: usage.Aggregate and findCover treated every record that was not hook or boundary as a context() call, so ADR-0056's evidence and commit records would have inflated call counts and covered edits"
confidence: high
diff_trigger:
  paths: ["internal/usage/**"]
  added: 'Source[A-Z][A-Za-z]* *= *"'
check: "Does Record.IsCall exclude the new source, so stats do not count it as context served and findCover does not let it cover an edit?"
---

# Lesson — a new usage-log record source must not count as a context() call

**Trigger:** a `Source*` constant is added to `internal/usage`.

**Insight:** the usage log is shared by several hooks: context() calls, the
pre-edit hook's edits, compaction boundaries, verification evidence and
commit verdicts. Code that counts calls, or looks for the call that covered an
edit, used to ask "is this a hook or a boundary?" and treat anything else as a
call, so every new record kind became a context() call by default.
`Record.IsCall` is the one place that answers the question, and a new source
belongs there.

**Rejected:** a separate log file per hook. The join between calls, edits and
evidence by session is the point of having one log (ADR-0025, ADR-0042).

**Keywords:** usage, stats, record source, context call, coverage, hooks
