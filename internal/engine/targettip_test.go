package engine

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

// adr renders a minimal decision record.
func adr(id, title string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: compa\n" +
		"status: accepted\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n" +
		"# " + id + " — " + title + "\n\n## Context\n\nc\n\n## Decision\n\nd\n\n## Rejected\n\nr\n"
}

func findings(rep model.Report, check string) []model.Finding {
	var out []model.Finding
	for _, f := range rep.Findings {
		if f.Check == check {
			out = append(out, f)
		}
	}
	return out
}

// The failure this is for: two branches cut from the same base, each minting
// the SAME next-free id, and neither one's own history showing a collision.
// Before ADR-0041 both rendered green and the duplicate appeared on master in
// nobody's diff — observed three times in one day on the pilot.
func TestIDTakenOnTargetFailsTheSecondPR(t *testing.T) {
	dir, base := seed(t, true)

	// Sibling PR lands first, on the target branch.
	write(t, dir, ".nugit/decisions/0051-fabrics-provider.md", adr("ADR-0051", "fabrics provider"))
	target := commitAll(t, dir, "sibling: ADR-0051")

	// Our branch was cut from `base`, before that landed, and mints 0051 too.
	git(t, dir, "checkout", "-q", "-b", "mine", base)
	write(t, dir, ".nugit/decisions/0051-offline-access.md", adr("ADR-0051", "offline access"))
	mine := commitAll(t, dir, "mine: ADR-0051")

	rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: mine})
	if err != nil {
		t.Fatal(err)
	}
	fs := findings(rep, "duplicate-knowledge-id")
	if len(fs) != 1 {
		t.Fatalf("want one duplicate-knowledge-id finding, got %d: %+v", len(fs), fs)
	}
	if fs[0].Severity != model.SevFail {
		t.Errorf("severity = %s, want fail", fs[0].Severity)
	}
	if !strings.Contains(fs[0].Detail, "0051-fabrics-provider.md") {
		t.Errorf("the finding must name the file already holding the id: %s", fs[0].Detail)
	}
	// The whole point: the collision is invisible from the merge base, so a
	// render that only looks there has to stay green — proving the new finding
	// comes from reading the TARGET, not from some unrelated change in scope.
	rep2, err := BuildReport(Options{RepoDir: dir, Base: base, Head: mine})
	if err != nil {
		t.Fatal(err)
	}
	if fs2 := findings(rep2, "duplicate-knowledge-id"); len(fs2) != 0 {
		t.Errorf("against the merge base there is no collision to see, got %+v", fs2)
	}
}

// Modifying a record whose id is (of course) already on the target must not
// fire — otherwise every edit to an existing ADR becomes a failure.
func TestModifyingAnExistingRecordIsNotACollision(t *testing.T) {
	dir, _ := seed(t, true)
	write(t, dir, ".nugit/decisions/0051-fabrics-provider.md", adr("ADR-0051", "fabrics provider"))
	target := commitAll(t, dir, "ADR-0051 lands")

	git(t, dir, "checkout", "-q", "-b", "edit")
	write(t, dir, ".nugit/decisions/0051-fabrics-provider.md",
		adr("ADR-0051", "fabrics provider")+"\nAn added paragraph.\n")
	head := commitAll(t, dir, "amend the prose")

	rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	if fs := findings(rep, "duplicate-knowledge-id"); len(fs) != 0 {
		t.Errorf("editing an existing record is not a collision: %+v", fs)
	}
}

// Two files in ONE PR sharing an id is ADR-0039's case and keeps its own
// wording — the target-tip check must not double-report it.
func TestWithinPRCollisionReportedOnce(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/decisions/0051-a.md", adr("ADR-0051", "a"))
	target := commitAll(t, dir, "ADR-0051 lands")

	git(t, dir, "checkout", "-q", "-b", "two", base)
	write(t, dir, ".nugit/decisions/0051-b.md", adr("ADR-0051", "b"))
	write(t, dir, ".nugit/decisions/0051-c.md", adr("ADR-0051", "c"))
	head := commitAll(t, dir, "two more 0051s")

	rep, err := BuildReport(Options{RepoDir: dir, Base: target, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	fs := findings(rep, "duplicate-knowledge-id")
	if len(fs) != 1 {
		t.Fatalf("one id, one finding — got %d: %+v", len(fs), fs)
	}
	if !strings.Contains(fs[0].Title, "duplicate knowledge id") {
		t.Errorf("the within-PR collision should keep ADR-0039's wording, got %q", fs[0].Title)
	}
}

// A `supersedes:` written as a relates_to edge declares no supersession —
// ADR-0003 reads the front-matter field of that name. Unknown verbs warn too;
// a bare id is legitimate and must stay silent.
func TestEdgeVocabularyFindings(t *testing.T) {
	dir, base := seed(t, true)
	obj := func(id, edges string) string {
		return "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: compa\n" +
			"status: accepted\ncreated: 2026-01-01T00:00:00Z\nrelates_to:\n" + edges +
			"provenance:\n  commit: x\n---\n\n# " + id + "\n\n## Decision\n\nd\n\n## Rejected\n\nr\n"
	}
	write(t, dir, ".nugit/decisions/0001-base.md", adr("ADR-0001", "base"))
	commitAll(t, dir, "base record")

	git(t, dir, "checkout", "-q", "-b", "edges")
	write(t, dir, ".nugit/decisions/0002-bad.md",
		obj("ADR-0002", "  - supersedes:ADR-0001\n  - ammends:ADR-0001\n  - ADR-0001\n"))
	head := commitAll(t, dir, "edges")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base, Head: head})
	if err != nil {
		t.Fatal(err)
	}
	fs := findings(rep, "edge-vocabulary")
	if len(fs) != 2 {
		t.Fatalf("want a misplaced-field finding and an unknown-verb finding, got %d: %+v", len(fs), fs)
	}
	joined := fs[0].Title + fs[0].Detail + fs[1].Title + fs[1].Detail
	for _, want := range []string{"supersedes:ADR-0001", "front-matter", "ammends:ADR-0001"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in:\n%s", want, joined)
		}
	}
	// The bare `- ADR-0001` must not be reported anywhere.
	if strings.Contains(joined, "  - ADR-0001") {
		t.Errorf("a bare id is a valid see-also and must not be flagged:\n%s", joined)
	}
	for _, f := range fs {
		if f.Severity != model.SevWarn {
			t.Errorf("edge-vocabulary should warn, not %s", f.Severity)
		}
	}
}

// Wiring drift is doctor's job too, but doctor is advisory and nobody runs it —
// a stale pin sat in one of five surfaces on the pilot for four weeks. At PR
// time it must warn, and only when the PR actually touches a wiring artifact.
func TestWiringDriftWarnsOnlyWhenTouched(t *testing.T) {
	dir, base := seed(t, true)
	write(t, dir, ".nugit/config.yml", "schema_version: 1\nc4:\n  mode: enforce\npr_render:\n  fail_on: fail\n")
	write(t, dir, "CLAUDE.md", "Install: go install github.com/n8o/nugit/cmd/nugit@v0.4.0\n")
	write(t, dir, ".github/workflows/nugit.yml", "jobs:\n  v:\n    steps:\n      - run: nugit pr-render -fail-on fail\n")
	write(t, dir, ".claude/skills/nugit/SKILL.md", "Install nugit@v0.4.0 — this repo runs c4.mode: enforce\n")
	base2 := commitAll(t, dir, "aligned wiring")

	// A PR that touches only code leaves the drift to doctor, even once it exists.
	git(t, dir, "checkout", "-q", "-b", "drifted")
	write(t, dir, ".claude/skills/nugit/SKILL.md", "Install nugit@v0.3.0 — this repo runs c4.mode: enforce\n")
	drift := commitAll(t, dir, "skill pin goes stale")

	rep, err := BuildReport(Options{RepoDir: dir, Base: base2, Head: drift})
	if err != nil {
		t.Fatal(err)
	}
	fs := findings(rep, "wiring-drift")
	if len(fs) != 1 {
		t.Fatalf("want one pin finding, got %d: %+v", len(fs), fs)
	}
	if fs[0].Severity != model.SevWarn {
		t.Errorf("severity = %s, want warn", fs[0].Severity)
	}
	for _, want := range []string{"v0.3.0", "v0.4.0", "SKILL.md"} {
		if !strings.Contains(fs[0].Detail, want) {
			t.Errorf("detail should name both refs and the file; missing %q in %s", want, fs[0].Detail)
		}
	}

	// Same drift present, but this PR does not touch any wiring artifact.
	git(t, dir, "checkout", "-q", "-b", "codeonly", drift)
	write(t, dir, "a/a.go", "package a\n\n// A does a thing, differently.\nfunc A() {}\n")
	codeOnly := commitAll(t, dir, "unrelated code change")
	rep2, err := BuildReport(Options{RepoDir: dir, Base: drift, Head: codeOnly})
	if err != nil {
		t.Fatal(err)
	}
	if fs2 := findings(rep2, "wiring-drift"); len(fs2) != 0 {
		t.Errorf("pre-existing drift is doctor's job, not every PR's: %+v", fs2)
	}
	_ = base
}
