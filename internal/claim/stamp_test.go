package claim

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// commit runs the commit-msg hook's stamping on msg, then commits the staged
// change with whatever the hook left in the file, and returns the objective
// the commit carries ("" for none) and what the hook printed.
func commit(t *testing.T, dir, msg string) (objective, said string) {
	t.Helper()
	f := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
	if err := os.WriteFile(f, []byte(msg), 0o644); err != nil {
		t.Fatal(err)
	}
	var w bytes.Buffer
	StampFile(dir, f, &w)
	git(t, dir, "commit", "-q", "--allow-empty", "-F", f)
	return git(t, dir, "log", "-1", "--format=%(trailers:key=objective,valueonly,separator=)"), w.String()
}

const store = `{"id":"acme-1","title":"make it fast","status":"in_progress"}` + "\n" +
	`{"id":"acme-2","title":"later","status":"open"}` + "\n" +
	`{"id":"acme-3","title":"old","status":"closed"}` + "\n"

// A branch named after its bead is claimed by its name (AGENTS.md), so every
// commit on it is stamped, not only the first. Seen on real branches: the
// first commit was stamped from the name, and the next one, a commit that
// only touched the bead's line in the plan store, went up with no objective.
// The objective's own bead being the only change, or the bead being closed,
// played no part: once one commit on the branch named the objective, the
// branch's commits answered before its name did, and the hook never stamps
// from commits.
func TestBranchNamedAfterBeadStampsEveryCommit(t *testing.T) {
	dir := repo(t)
	git(t, dir, "checkout", "-q", "-b", "feat/acme-1-fast")

	write(t, dir, "fast.go", "package fast\n")
	git(t, dir, "add", "-A")
	if got, _ := commit(t, dir, "feat: make it fast\n"); got != "acme-1" {
		t.Fatalf("first commit objective = %q, want acme-1", got)
	}

	// The bead's notes change on its own (a shard-only diff, `git commit -m`).
	write(t, dir, ".beads/issues.jsonl", strings.Replace(store,
		`"status":"in_progress"}`, `"status":"in_progress","notes":"fast enough on the bench"}`, 1))
	git(t, dir, "add", "-A")
	got, said := commit(t, dir, "chore(beads): note acme-1's bench result")
	if got != "acme-1" {
		t.Errorf("notes-only commit objective = %q, want acme-1", got)
	}
	if !strings.Contains(said, "named by branch feat/acme-1-fast") {
		t.Errorf("the hook should say where the objective came from: %q", said)
	}

	// The commit that closes the bead, the one that most needs the line.
	write(t, dir, ".beads/issues.jsonl", strings.Replace(store,
		`"status":"in_progress"}`, `"status":"closed","notes":"fast enough on the bench"}`, 1))
	git(t, dir, "add", "-A")
	if got, _ := commit(t, dir, "chore(beads): close acme-1\n"); got != "acme-1" {
		t.Errorf("closing commit objective = %q, want acme-1", got)
	}
	if cur := Resolve(dir); cur.Objective != "acme-1" || cur.Source != FromBranchName {
		t.Errorf("resolve after the bead closed = %+v, want acme-1 from the branch name", cur)
	}
	if d := Digest(dir); strings.Contains(d, "not claimed") {
		t.Errorf("a branch named after its bead needs no claim, but the digest asks for one:\n%s", d)
	}
}

// A claimed branch stamps the closing commit too, whatever it changes.
func TestClaimedObjectiveStampsTheClosingCommit(t *testing.T) {
	dir := repo(t)
	if _, err := Add(dir, "acme-1", false); err != nil {
		t.Fatal(err)
	}
	write(t, dir, "fast.go", "package fast\n")
	git(t, dir, "add", "-A")
	commit(t, dir, "feat: make it fast\n")
	write(t, dir, ".beads/issues.jsonl", strings.Replace(store, `"status":"in_progress"`, `"status":"closed"`, 1))
	git(t, dir, "add", "-A")
	if got, _ := commit(t, dir, "chore(beads): close acme-1"); got != "acme-1" {
		t.Errorf("closing commit objective = %q, want acme-1", got)
	}
}

// A stacked branch carries its base branch's commits until the base merges.
// Those commits never decide what this branch's commits are stamped with:
// with no claim and no bead in the name, nothing is stamped; with a bead in
// the name, the name wins.
func TestStackedBranchIsNotStampedWithItsBaseObjective(t *testing.T) {
	dir := repo(t)
	git(t, dir, "checkout", "-q", "-b", "feat/acme-1-fast")
	if got, _ := commit(t, dir, "feat: make it fast\n"); got != "acme-1" {
		t.Fatalf("base commit objective = %q", got)
	}
	git(t, dir, "checkout", "-q", "-b", "on-top")
	if got, said := commit(t, dir, "feat: something else\n"); got != "" {
		t.Errorf("unnamed stacked branch was stamped %q from its base's commits (%s)", got, said)
	}
	git(t, dir, "checkout", "-q", "-b", "feat/acme-2-later")
	if got, _ := commit(t, dir, "feat: the later step\n"); got != "acme-2" {
		t.Errorf("stacked branch named after acme-2 was stamped %q", got)
	}
}

// The hook warns, and leaves the message alone, when it names another
// objective than the branch's.
func TestStampFileWarnsOnAnotherObjective(t *testing.T) {
	dir := repo(t)
	git(t, dir, "checkout", "-q", "-b", "feat/acme-1-fast")
	got, said := commit(t, dir, "feat: x\n\nobjective: acme-2\n")
	if got != "acme-2" || !strings.Contains(said, "works toward acme-1") {
		t.Errorf("objective = %q, said %q", got, said)
	}
}
