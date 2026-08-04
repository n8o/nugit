package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/knowledge"
	"github.com/n8o/nugit/internal/model"
)

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestUntypedObjects(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/good.md",
		"---\nschema_version: 1\nid: ADR-1\ntype: decision\nscope: global\nstatus: accepted\n---\n\n# ok\n")
	// A bug seen in the wild: supersedes as a YAML list fails the string-typed schema and
	// silently untypes the whole object.
	write(t, dir, ".nugit/decisions/list-supersedes.md",
		"---\nschema_version: 1\nid: ADR-2\ntype: decision\nsupersedes:\n  - ADR-1\n---\n\n# broken\n")
	write(t, dir, ".nugit/lessons/no-frontmatter.md", "# just prose, no fence\n")
	write(t, dir, ".nugit/decisions/.gitkeep", "")

	bad := untypedObjects(dir)
	if len(bad) != 2 {
		t.Fatalf("want 2 problem files, got %d: %v", len(bad), bad)
	}
	var joined string
	for _, bf := range bad {
		joined += bf.Rel + " (" + bf.Detail + ")\n"
	}
	if !strings.Contains(joined, "list-supersedes.md") || !strings.Contains(joined, "no-frontmatter.md") {
		t.Fatalf("wrong files flagged: %v", bad)
	}
	if strings.Contains(joined, "good.md") {
		t.Fatalf("valid object flagged: %v", bad)
	}
}

// ADR-0022: the supersedes-as-list case gets a TARGETED diagnosis naming the
// field, not the generic untyped message.
func TestSupersedesListTargetedDiagnosis(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/list-supersedes.md",
		"---\nschema_version: 1\nid: ADR-2\ntype: decision\nsupersedes:\n  - ADR-1\n---\n\n# broken\n")

	bad := untypedObjects(dir)
	if len(bad) != 1 {
		t.Fatalf("want 1 problem file, got %d: %v", len(bad), bad)
	}
	if len(bad[0].ListFields) != 1 || bad[0].ListFields[0] != "supersedes" {
		t.Fatalf("ListFields = %v, want [supersedes]", bad[0].ListFields)
	}
	if !strings.Contains(bad[0].Detail, "`supersedes:`") || !strings.Contains(bad[0].Detail, "YAML list") {
		t.Errorf("detail must name the field and the list mistake, got %q", bad[0].Detail)
	}

	// The health reason is targeted too.
	rep := Run(dir)
	if rep.Health == nil {
		t.Fatal("expected store health")
	}
	found := false
	for _, r := range rep.Health.Reasons {
		if strings.Contains(r, "`supersedes:`") && strings.Contains(r, "list-valued") {
			found = true
		}
	}
	if !found {
		t.Errorf("health reasons must name the list-valued field, got %v", rep.Health.Reasons)
	}
}

// A generic schema failure (no list involved) keeps the generic message and
// the generic health reason.
func TestUntypedGenericStillGeneric(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/no-id.md",
		"---\nschema_version: 1\ntype: decision\nscope: global\n---\n\n# missing id\n")

	bad := untypedObjects(dir)
	if len(bad) != 1 || len(bad[0].ListFields) != 0 {
		t.Fatalf("want 1 generic problem file, got %v", bad)
	}
	rep := Run(dir)
	if rep.Health == nil {
		t.Fatal("expected store health")
	}
	found := false
	for _, r := range rep.Health.Reasons {
		if strings.Contains(r, "untyped front-matter") && !strings.Contains(r, "list-valued") {
			found = true
		}
	}
	if !found {
		t.Errorf("generic untyped reason expected, got %v", rep.Health.Reasons)
	}
}

func TestUntypedObjectsCheckInRun(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/list-supersedes.md",
		"---\nid: ADR-2\ntype: decision\nsupersedes:\n  - ADR-1\n---\n\n# broken\n")
	rep := Run(dir)
	found := false
	for _, c := range rep.Checks {
		if c.Name == "knowledge objects are typed" {
			found = true
			if c.OK {
				t.Error("check must fail when an object is silently untyped")
			}
			if !strings.Contains(c.Detail, "list-supersedes.md") {
				t.Errorf("detail should name the file: %s", c.Detail)
			}
		}
	}
	if !found {
		t.Fatal("knowledge-objects-typed check missing from doctor report")
	}
}

// ADR-0021: the full-repo drift scan lists detected units the model misses,
// but stays advisory — modeling debt never fails the pre-flight.
func TestModelCoverageScanIsAdvisory(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/architecture/workspace.dsl",
		"workspace \"m\" {\n  model {\n    s = softwareSystem \"m\" {\n"+
			"      core = component \"Core\" { properties { paths \"libs/core/**\" } }\n"+
			"    }\n  }\n}\n")
	write(t, dir, "libs/core/CMakeLists.txt", "add_library(core core.cpp)\n")
	write(t, dir, "libs/core/core.cpp", "int core(){return 0;}\n")
	write(t, dir, "libs/newlib/CMakeLists.txt", "add_library(newlib newlib.cpp)\n")
	write(t, dir, "libs/newlib/newlib.cpp", "int newlib(){return 0;}\n")

	rep := Run(dir)
	var found *Check
	for i := range rep.Checks {
		if rep.Checks[i].Name == "model covers detected units" {
			found = &rep.Checks[i]
		}
	}
	if found == nil {
		t.Fatal("model-coverage check missing from doctor report")
	}
	if found.OK {
		t.Error("an unmodeled detected unit must surface in the coverage scan")
	}
	if !found.Advisory {
		t.Error("the coverage scan must be advisory (never gates the exit code)")
	}
	if !strings.Contains(found.Detail, "libs/newlib") || !strings.Contains(found.Detail, "nugit-model") {
		t.Errorf("detail must name the unit and the remedy, got %q", found.Detail)
	}
	if strings.Contains(found.Detail, "libs/core") {
		t.Errorf("mapped unit must not be listed, got %q", found.Detail)
	}
}

// The twin: every detected unit mapped -> the scan reports OK.
func TestModelCoverageScanCleanWhenMapped(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/architecture/workspace.dsl",
		"workspace \"m\" {\n  model {\n    s = softwareSystem \"m\" {\n"+
			"      core = component \"Core\" { properties { paths \"libs/core/**\" } }\n"+
			"    }\n  }\n}\n")
	write(t, dir, "libs/core/CMakeLists.txt", "add_library(core core.cpp)\n")
	write(t, dir, "libs/core/core.cpp", "int core(){return 0;}\n")

	rep := Run(dir)
	for _, c := range rep.Checks {
		if c.Name == "model covers detected units" && !c.OK {
			t.Fatalf("fully mapped inventory must be OK, got %q", c.Detail)
		}
	}
}

// ADR-0016: the pending-ratification line is informational — it names the
// proposed objects but never fails the pre-flight.
func TestPendingRatificationIsInformational(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/p.md",
		"---\nschema_version: 1\nid: ADR-P\ntype: decision\nscope: a\nstatus: proposed\n---\n\n# p\n")

	rep := Run(dir)
	var found *Check
	for i := range rep.Checks {
		if rep.Checks[i].Name == "proposed objects pending ratification" {
			found = &rep.Checks[i]
		}
	}
	if found == nil {
		t.Fatal("pending-ratification check missing from doctor report")
	}
	if !found.OK {
		t.Error("pending ratification must never fail the pre-flight")
	}
	if !strings.Contains(found.Detail, "ADR-P") || !strings.Contains(found.Detail, "ratify -list") {
		t.Errorf("detail must name the pending id and the remedy, got %q", found.Detail)
	}
}

// ADR-0022: the pending line carries each object's age from `created:` so a
// stuck candidate lane is visible; objects without a date stay bare.
func TestPendingRatificationShowsAge(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/p.md",
		"---\nschema_version: 1\nid: ADR-P\ntype: decision\nscope: a\nstatus: proposed\ncreated: 2026-06-17T00:00:00Z\n---\n\n# p\n")
	write(t, dir, ".nugit/decisions/q.md",
		"---\nschema_version: 1\nid: ADR-Q\ntype: decision\nscope: a\nstatus: proposed\n---\n\n# q\n")

	objs := loadObjs(t, dir)
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	detail := pendingDetail(objs, now)
	if !strings.Contains(detail, "ADR-P (45d)") {
		t.Errorf("detail must show the age in days, got %q", detail)
	}
	if !strings.Contains(detail, "ADR-Q") {
		t.Errorf("dateless object must still appear, got %q", detail)
	}
	if strings.Contains(detail, "ADR-Q (") {
		t.Errorf("dateless object must not fabricate an age, got %q", detail)
	}
}

func loadObjs(t *testing.T, dir string) []model.KnowledgeObject {
	t.Helper()
	objs, err := knowledge.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return objs
}

// ADR-0022: a supersession declared only in prose is advisory-flagged with the
// exact edge to add; adding the edge (or amends:) silences it.
func TestProseSupersessionCheck(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/references/ref.md",
		"---\nschema_version: 1\nid: REF-T-14\ntype: reference\nscope: a\nstatus: active\n---\n\n# draft 14\n")
	write(t, dir, ".nugit/decisions/new.md",
		"---\nschema_version: 1\nid: ADR-20\ntype: decision\nscope: a\nstatus: accepted\n---\n\n# d16\n\nSupersedes REF-T-14 — that reference is now stale.\n")

	rep := Run(dir)
	c := checkByName(t, rep, "supersession edges match prose")
	if c.OK {
		t.Error("prose-only supersession must flag the check")
	}
	if !c.Advisory {
		t.Error("prose-supersession must stay advisory (never gates the pre-flight)")
	}
	if !strings.Contains(c.Detail, "ADR-20") || !strings.Contains(c.Detail, "REF-T-14") ||
		!strings.Contains(c.Detail, "supersedes: REF-T-14") {
		t.Errorf("detail must name both objects and the edge to add, got %q", c.Detail)
	}

	// With the front-matter edge, prose and graph agree: check passes.
	write(t, dir, ".nugit/decisions/new.md",
		"---\nschema_version: 1\nid: ADR-20\ntype: decision\nscope: a\nstatus: accepted\nsupersedes: REF-T-14\n---\n\n# d16\n\nSupersedes REF-T-14 — that reference is now stale.\n")
	rep = Run(dir)
	if c := checkByName(t, rep, "supersession edges match prose"); !c.OK {
		t.Errorf("edge present: check must pass, got %q", c.Detail)
	}
}

// ADR-0022: provenance sanity — literal HEAD, explicit-empty commit and
// unknown provenance keys flag; seed/bootstrap/sha/slug values do not.
func TestProvenanceSanity(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/head.md",
		"---\nschema_version: 1\nid: ADR-1\ntype: decision\nscope: a\nstatus: accepted\nprovenance:\n  commit: HEAD\n---\n\n# a\n")
	write(t, dir, ".nugit/decisions/empty.md",
		"---\nschema_version: 1\nid: ADR-2\ntype: decision\nscope: a\nstatus: accepted\nprovenance:\n  commit: \"\"\n---\n\n# b\n")
	write(t, dir, ".nugit/decisions/unknown-key.md",
		"---\nschema_version: 1\nid: ADR-3\ntype: decision\nscope: a\nstatus: accepted\nprovenance:\n  commit: seed\n  issues:\n    - 42\n---\n\n# c\n")
	write(t, dir, ".nugit/decisions/ok-seed.md",
		"---\nschema_version: 1\nid: ADR-4\ntype: decision\nscope: a\nstatus: accepted\nprovenance:\n  commit: seed\n  citation: somewhere\n---\n\n# d\n")
	write(t, dir, ".nugit/decisions/ok-sha.md",
		"---\nschema_version: 1\nid: ADR-5\ntype: decision\nscope: a\nstatus: accepted\nprovenance:\n  commit: d3912d39\n---\n\n# e\n")
	write(t, dir, ".nugit/decisions/ok-slug.md",
		"---\nschema_version: 1\nid: ADR-6\ntype: decision\nscope: a\nstatus: accepted\nprovenance:\n  commit: bootstrap\n---\n\n# f\n")

	objs := loadObjs(t, dir)
	issues := provenanceIssues(dir, objs)
	if len(issues) != 3 {
		t.Fatalf("want 3 issues, got %d: %v", len(issues), issues)
	}
	joined := strings.Join(issues, "\n")
	for _, want := range []string{"head.md", "empty.md", "unknown-key.md", "literal HEAD", "issues"} {
		if !strings.Contains(joined, want) {
			t.Errorf("issues must mention %q, got %v", want, issues)
		}
	}
	for _, silent := range []string{"ok-seed.md", "ok-sha.md", "ok-slug.md"} {
		if strings.Contains(joined, silent) {
			t.Errorf("%s must not flag, got %v", silent, issues)
		}
	}

	rep := Run(dir)
	c := checkByName(t, rep, "provenance is sane")
	if c.OK || !c.Advisory {
		t.Errorf("provenance check must flag advisorily: OK=%v Advisory=%v", c.OK, c.Advisory)
	}
}

func TestProvenanceSanityCleanStore(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".nugit/decisions/ok.md",
		"---\nschema_version: 1\nid: ADR-1\ntype: decision\nscope: a\nstatus: accepted\nprovenance:\n  commit: seed\n---\n\n# ok\n")
	rep := Run(dir)
	if c := checkByName(t, rep, "provenance is sane"); !c.OK {
		t.Errorf("clean store must pass, got %q", c.Detail)
	}
}

func checkByName(t *testing.T, rep Report, name string) Check {
	t.Helper()
	for _, c := range rep.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("check %q missing from doctor report", name)
	return Check{}
}
