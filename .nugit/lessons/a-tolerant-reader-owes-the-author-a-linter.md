---
schema_version: 1
id: LESSON-a-tolerant-reader-owes-the-author-a-linter
type: lesson
scope: global
status: active
created: 2026-08-21T00:00:00Z
relates_to:
  - constrains:beads
  - informs:ADR-0040
provenance:
  commit: seed
  citation: "pilot repo: scripts/check-beads.sh, ~250 lines reimplementing internal/beads/beads.go in bash, pinned by a version comment"
confidence: high
---

# Lesson — every silent tolerance in a reader eventually gets reimplemented, badly, by its users

**Trigger:** writing a parser that skips what it cannot use — an unparseable
line, a missing field, an unrecognised enum value, a duplicate key.

**Insight:** each tolerance is a case where the author's input renders as
something other than what they wrote, with nothing said. A step that vanishes
for a missing field looks exactly like a step that was never written. On the
pilot every one of these produced a real, separately-diagnosed incident, and the
repo's answer was a shell script that reverse-engineered the reader's package —
in another language, in another repo, against internals it pinned by a version
comment in a header, and which drifted the moment the reader changed. The rules
were never theirs to own: they are properties of *our* parser.

If a reader tolerates silently, it owes its users a surface that says what it
tolerated. Ship the linter in the same package as the reader, so the rule and
its explanation cannot disagree, and expose it both as a command they can run
and as a finding at review time — the pre-flight nobody runs on a Tuesday is how
these survive for weeks.

**Rejected:** making the reader strict instead. The tolerance is correct — the
store's schema belongs to another tool and will grow fields we do not read, and
a hard failure on an unknown status would make every upstream release a
breakage. Tolerate, then report.

**Keywords:** parser, tolerance, silent-skip, lint, ergonomics, shell script,
reimplementation, drift
