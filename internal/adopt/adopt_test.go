package adopt

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- synthetic repo scaffolding ---------------------------------------------

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
	}
}

func initRepo(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "init", "-q", "-b", "main")
	git(t, dir, "config", "user.email", "t@example.com")
	git(t, dir, "config", "user.name", "t")
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", msg)
}

// service writes a synthetic deployable: apps/<name>/Dockerfile shipping
// apps/<name>. That is the shape modelfacts.Units resolves to a unit dir.
func service(t *testing.T, dir, name string) {
	t.Helper()
	write(t, dir, "apps/"+name+"/Dockerfile",
		"FROM scratch\nCOPY apps/"+name+" /src\nRUN go build ./...\n")
	write(t, dir, "apps/"+name+"/main.txt", name+"\n")
}

// baseRepo is the fixture the inventory-diff tests share: three real services,
// two prose inventories that disagree with the tree and with each other, and a
// runbook shelf. Every name here is synthetic.
func baseRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	for _, s := range []string{"parcel-service", "crate-service", "pallet-service"} {
		service(t, dir, s)
	}
	// A real Python service the detectors CANNOT see (ADR-0021 blind spot).
	write(t, dir, "apps/hopper-service/main.py", "print('hi')\n")

	write(t, dir, "AGENTS.md", `# Agent instructions

## Services

| service | port | source |
|---|---|---|
| `+"`parcel-service`"+` | 8080 | `+"`apps/parcel-service`"+` |
| `+"`crate-service`"+` | 8081 | `+"`apps/crate-service`"+` |
| `+"`depot-service`"+` | 8082 | `+"`apps/depot-service`"+` |
| `+"`hopper-service`"+` | 8083 | `+"`apps/hopper-service`"+` |
`)
	write(t, dir, "docs/architecture.md", `# Architecture

| component | port | dir |
|---|---|---|
| `+"`parcel-service`"+` | 9090 | `+"`services/parcel`"+` |
| `+"`crate-service`"+` | 8081 | `+"`apps/crate-service`"+` |
| `+"`depot-service`"+` | 8082 | `+"`apps/depot-service`"+` |
`)
	commitAll(t, dir, "seed")
	return dir
}

func run(t *testing.T, dir string) Report {
	t.Helper()
	rep, err := Run(Options{RepoDir: dir})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return rep
}

func names(as []Absent) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

func has(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// --- the core inventory diff -------------------------------------------------

func TestDocumentedButAbsent(t *testing.T) {
	rep := run(t, baseRepo(t))
	got := names(rep.Absent)
	if !has(got, "depot-service") {
		t.Errorf("documented-but-absent = %v, want it to contain depot-service", got)
	}
	for _, a := range rep.Absent {
		if a.Name != "depot-service" {
			t.Errorf("unexpected phantom %q (rule %s, %v) — precision over recall", a.Name, a.Rule, a.Mentions)
		}
		if a.Rule != RuleColocated {
			t.Errorf("depot-service admitted by %q, want %q", a.Rule, RuleColocated)
		}
		if len(a.Mentions) == 0 {
			t.Error("a phantom with no mentions is unauditable")
		}
	}
}

// A real unit the DETECTORS cannot see must never be called a phantom: that is
// the false positive that discredits the whole report (ADR-0021 blind spots).
func TestUndetectedButRealDirectoryIsNotAPhantom(t *testing.T) {
	rep := run(t, baseRepo(t))
	if has(names(rep.Absent), "hopper-service") {
		t.Errorf("hopper-service reported absent, but apps/hopper-service exists on disk: %v", names(rep.Absent))
	}
}

func TestPresentButUndocumented(t *testing.T) {
	rep := run(t, baseRepo(t))
	var got []string
	for _, u := range rep.Undocumented {
		got = append(got, u.Name)
	}
	if !has(got, "pallet-service") {
		t.Errorf("present-but-undocumented = %v, want it to contain pallet-service", got)
	}
	if has(got, "parcel-service") || has(got, "crate-service") {
		t.Errorf("a documented service was reported undocumented: %v", got)
	}
}

func TestDisagreements(t *testing.T) {
	rep := run(t, baseRepo(t))
	var ports, paths *Disagreement
	for i := range rep.Disagreements {
		d := &rep.Disagreements[i]
		if d.Unit != "parcel-service" {
			continue
		}
		switch d.Attr {
		case "port":
			ports = d
		case "path":
			paths = d
		}
	}
	if ports == nil {
		t.Fatalf("no port disagreement for parcel-service; got %+v", rep.Disagreements)
	}
	if len(ports.Claims) != 2 {
		t.Fatalf("port claims = %+v, want one per document", ports.Claims)
	}
	seen := map[string]string{}
	for _, c := range ports.Claims {
		seen[c.Doc] = c.Values
		if c.Line == 0 {
			t.Errorf("claim %+v has no line — a disagreement must be checkable", c)
		}
	}
	if seen["AGENTS.md"] != "8080" || seen["docs/architecture.md"] != "9090" {
		t.Errorf("port claims = %v, want AGENTS.md 8080 vs docs/architecture.md 9090", seen)
	}
	if paths == nil {
		t.Fatalf("no path disagreement for parcel-service; got %+v", rep.Disagreements)
	}
	// crate-service agrees on both attributes in both documents: silence and
	// agreement must never be reported as conflict.
	for _, d := range rep.Disagreements {
		if d.Unit == "crate-service" {
			t.Errorf("crate-service agrees in both documents but was reported: %+v", d)
		}
	}
}

// --- staleness ---------------------------------------------------------------

func TestStalenessCountsCommitsSince(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service")
	service(t, dir, "crate-service")
	write(t, dir, "AGENTS.md", "# Agents\n\n| service | port |\n|---|---|\n| `parcel-service` | 8080 |\n| `crate-service` | 8081 |\n")
	commitAll(t, dir, "seed with the inventory")

	for i := 0; i < 5; i++ {
		write(t, dir, "apps/parcel-service/f"+string(rune('a'+i))+".txt", "x\n")
		commitAll(t, dir, "later change")
	}
	rep := run(t, dir)
	var agents *Doc
	for i := range rep.Docs {
		if rep.Docs[i].Path == "AGENTS.md" {
			agents = &rep.Docs[i]
		}
	}
	if agents == nil {
		t.Fatalf("AGENTS.md missing from %+v", rep.Docs)
	}
	if agents.CommitsSince != 5 {
		t.Errorf("commits since AGENTS.md was touched = %d, want 5", agents.CommitsSince)
	}
	if agents.LastCommit == "" || agents.LastDate == "" {
		t.Errorf("staleness without a commit/date is unciteable: %+v", agents)
	}
	if rep.TotalCommits != 6 {
		t.Errorf("total commits = %d, want 6", rep.TotalCommits)
	}
	// A document touched by the newest commit is not stale.
	write(t, dir, "docs/fresh.md", "# Fresh\n\n| service | port |\n|---|---|\n| `parcel-service` | 8080 |\n| `crate-service` | 8081 |\n")
	commitAll(t, dir, "fresh doc")
	rep = run(t, dir)
	for _, d := range rep.Docs {
		if d.Path == "docs/fresh.md" && d.CommitsSince != 0 {
			t.Errorf("fresh doc reported %d commits stale", d.CommitsSince)
		}
	}
}

// --- the hard constraint: no .nugit/ at all ---------------------------------

func TestRunsInARepoThatNeverAdoptedNugit(t *testing.T) {
	dir := baseRepo(t)
	if _, err := os.Stat(filepath.Join(dir, ".nugit")); err == nil {
		t.Fatal("fixture must have no .nugit/")
	}
	rep := run(t, dir)
	if rep.HasStore {
		t.Error("HasStore true in a repo with no .nugit/")
	}
	if len(rep.Units) == 0 || len(rep.Docs) == 0 || len(rep.Absent) == 0 ||
		len(rep.Undocumented) == 0 || len(rep.Disagreements) == 0 {
		t.Fatalf("a repo that never adopted nugit must still get a full report: %d units, %d docs, %d absent, %d undocumented, %d disagreements",
			len(rep.Units), len(rep.Docs), len(rep.Absent), len(rep.Undocumented), len(rep.Disagreements))
	}
	// Read-only: the default run writes nothing at all.
	if _, err := os.Stat(filepath.Join(dir, ".nugit")); err == nil {
		t.Error("a default adopt run created .nugit/ — adopt is read-only")
	}
	if md := rep.Markdown(); !strings.Contains(md, "No `.nugit/` here") {
		t.Error("the pitch must say the report was computed without a store")
	}
}

// --- runbooks ----------------------------------------------------------------

func runbookRepo(t *testing.T) string {
	t.Helper()
	dir := baseRepo(t)
	write(t, dir, "docs/runbooks/crash-loop.md", `# Pods crash-loop after a config reload

## Symptom

The intake workers enter a crash loop within 30s of a config reload and the readiness probe never passes.

## Resolution

The config volume was mounted read-only; remount it read-write and restart the workload.
`)
	write(t, dir, "docs/runbooks/rotate-credentials.md", `# Rotate credentials

## Steps

Run the rotation job, then restart the consumers.
`)
	write(t, dir, "docs/notes/design.md", "# Design notes\n\nSome prose about the design.\n")
	commitAll(t, dir, "runbooks")
	return dir
}

func TestRunbookCandidates(t *testing.T) {
	rep := run(t, runbookRepo(t))
	byPath := map[string]Runbook{}
	for _, rb := range rep.Runbooks {
		byPath[rb.Path] = rb
	}
	crash, ok := byPath["docs/runbooks/crash-loop.md"]
	if !ok {
		t.Fatalf("crash-loop runbook not detected; got %+v", rep.Runbooks)
	}
	if crash.Detected != "runbooks-dir" {
		t.Errorf("detected_by = %q, want runbooks-dir", crash.Detected)
	}
	if !strings.Contains(crash.Trigger, "crash loop") {
		t.Errorf("trigger = %q, want the Symptom section's observation", crash.Trigger)
	}
	if crash.TriggerSource != "section:symptom" {
		t.Errorf("trigger_source = %q", crash.TriggerSource)
	}
	if !strings.Contains(crash.Insight, "read-only") {
		t.Errorf("insight = %q, want the Resolution section", crash.Insight)
	}
	if !crash.Complete() {
		t.Errorf("a runbook with both halves extracted should be complete: %+v", crash)
	}

	// Refuse to fabricate: no symptom section, and a title that is a task.
	rot, ok := byPath["docs/runbooks/rotate-credentials.md"]
	if !ok {
		t.Fatal("rotate-credentials runbook not detected")
	}
	if rot.Trigger != "" {
		t.Errorf("trigger = %q, want an empty trigger and a named gap (ADR-0028)", rot.Trigger)
	}
	if len(rot.Gaps) == 0 {
		t.Error("a candidate missing its symptom must name the gap")
	}

	if _, ok := byPath["docs/notes/design.md"]; ok {
		t.Error("an ordinary design note was classified as a runbook")
	}
	if _, ok := byPath["AGENTS.md"]; ok {
		t.Error("a prose inventory was classified as a runbook")
	}
}

func TestWriteCandidatesLandsInTheCandidateLane(t *testing.T) {
	dir := runbookRepo(t)
	rep, err := Run(Options{RepoDir: dir, WriteCandidates: true, Now: "2026-08-04T00:00:00Z"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rep.Written) != len(rep.Runbooks) {
		t.Fatalf("wrote %d candidates for %d runbooks", len(rep.Written), len(rep.Runbooks))
	}
	var sawTODO bool
	for _, rel := range rep.Written {
		b, rerr := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if rerr != nil {
			t.Fatalf("read %s: %v", rel, rerr)
		}
		s := string(b)
		if !strings.Contains(s, "status: proposed") {
			t.Errorf("%s is not in the candidate lane (ADR-0016):\n%s", rel, s)
		}
		if !strings.Contains(s, "**Trigger:**") || !strings.Contains(s, "**Insight:**") {
			t.Errorf("%s is not a house-format lesson:\n%s", rel, s)
		}
		if strings.Contains(s, "TODO — the observable symptom") {
			sawTODO = true
		}
	}
	if !sawTODO {
		t.Error("the candidate with no extractable symptom must carry the TriggerTODO placeholder, not an invented one")
	}
	// Idempotent: a second run clobbers nothing.
	rep2, err := Run(Options{RepoDir: dir, WriteCandidates: true, Now: "2026-08-04T00:00:00Z"})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(rep2.Written) != 0 {
		t.Errorf("second run wrote %v, want nothing", rep2.Written)
	}
}

// --- the precision guard -----------------------------------------------------

func TestPrecisionGuardRejectsWordyProse(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service")
	service(t, dir, "crate-service")
	write(t, dir, "README.md", `# The platform

Some background about the Architecture and the deployment story. See
`+"`README.md`"+` and release `+"`v0.20.0`"+` for details. The overall
`+"`deployment-strategy`"+` is documented elsewhere, as is the
`+"`docs/adr-index`"+` we keep for decisions. Everything you need to know
about observability lives in the handbook.

- Background reading
- Operational overview
- Contributing guide
`)
	commitAll(t, dir, "seed")
	rep := run(t, dir)
	if len(rep.Absent) != 0 {
		t.Errorf("wordy prose produced phantoms %v — a false absent discredits the whole report", names(rep.Absent))
	}
}

// The affix rule is the one that can fire on prose. Assert it fires only on a
// token shaped like this repo's units, and record the shape it needs.
func TestFamilyAffixNeedsTwoUnitsInTheSamePosition(t *testing.T) {
	sh := newShape([]unitRef{
		{Dir: "apps/parcel-service", Name: "parcel-service"},
		{Dir: "apps/crate-service", Name: "crate-service"},
		{Dir: "apps/lonely-widget", Name: "lonely-widget"},
	})
	if !sh.familyAffix("depot-service") {
		t.Error("depot-service shares the -service tail with two units; want admitted")
	}
	if sh.familyAffix("spare-widget") {
		t.Error("only ONE unit ends in -widget; a family needs two")
	}
	if sh.familyAffix("warehouse") {
		t.Error("a token with no separator has no family shape")
	}
}

func TestPlausibleTokenRejects(t *testing.T) {
	for _, tok := range []string{
		"v0.20.0", "1.2", "README.md", "main.go", "https", "example.com",
		"service", "component", "architecture", "ab", "8080",
		"CACHE_ENABLED", "MAX_RETRIES", // env vars normalize into unit-shaped names
	} {
		if plausibleToken(tok) {
			t.Errorf("plausibleToken(%q) = true, want false", tok)
		}
	}
	for _, tok := range []string{"parcel-service", "apps/parcel-service", "depot_service"} {
		if !plausibleToken(tok) {
			t.Errorf("plausibleToken(%q) = false, want true", tok)
		}
	}
}

// The dominant false positive measured on a real 21-service monorepo: generic
// unit names (single common words) make ordinary prose tables look like
// inventories, and every bare English word in them became a phantom.
func TestBareWordsAndEnvVarsNeverBecomePhantoms(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	for _, s := range []string{"spool", "hopper", "parcel-service"} {
		service(t, dir, s)
	}
	write(t, dir, "README.md", `# Platform

| product | latency | spool | video |
|---|---|---|---|
| Hopper | sub-second | Full | Yes |
| Spool | seconds | Origin | New |

Set `+"`CACHE_ENABLED=true`"+` and read `+"`@acme-corp/wire-client`"+` for the wire format.

- Hopper lanes get every announcement
- Spool sessions are sticky
- Origin nodes run the intake path
`)
	commitAll(t, dir, "seed")
	rep := run(t, dir)
	if len(rep.Absent) != 0 {
		t.Errorf("prose produced phantoms %v — bare words, env vars and @scope packages must never be claimed units", names(rep.Absent))
	}
}

// Rule 2 needs BOTH a quorum and a share: two real names inside a long list of
// prose must not turn the whole list into an inventory.
func TestColocationNeedsAMajority(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service")
	service(t, dir, "crate-service")
	write(t, dir, "docs/notes.md", `# Notes

- `+"`parcel-service`"+` owns the boot flow
- `+"`crate-service`"+` fans out
- `+"`spindle-service`"+` is a name we considered
- `+"`legacy-service`"+` was never built
- `+"`future-service`"+` is a placeholder
- `+"`another-service`"+` likewise
- `+"`spare-service`"+` likewise
- `+"`extra-service`"+` likewise
`)
	commitAll(t, dir, "seed")
	sh := newShape([]unitRef{
		{Dir: "apps/parcel-service", Name: "parcel-service"},
		{Dir: "apps/crate-service", Name: "crate-service"},
	})
	d, _ := readDoc(dir, "docs/notes.md")
	for _, sl := range scanDoc(d, sh) {
		if sl.Colocated {
			t.Fatalf("a list that is 2/8 real units was treated as an inventory (%q)", sl.Text)
		}
	}
	// The same names in a table column that IS mostly real units do qualify.
	write(t, dir, "docs/table.md", `# Services

| service | port |
|---|---|
| `+"`parcel-service`"+` | 8080 |
| `+"`crate-service`"+` | 8081 |
| `+"`spindle-service`"+` | 8082 |
`)
	d2, _ := readDoc(dir, "docs/table.md")
	got := false
	for _, sl := range scanDoc(d2, sh) {
		if sl.Colocated && sl.Text == "spindle-service" {
			got = true
		}
	}
	if !got {
		t.Error("a table column that is 2/3 real units must vouch for its third entry")
	}
}

func TestPathAnchorFindsAPhantomUnderARealParent(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "anvil")
	service(t, dir, "chisel")
	write(t, dir, "README.md", "# Platform\n\nThe milling path lives in `apps/lathe` and the forging path in `apps/anvil`.\n")
	commitAll(t, dir, "seed")
	rep := run(t, dir)
	var got *Absent
	for i := range rep.Absent {
		if rep.Absent[i].Name == "lathe" {
			got = &rep.Absent[i]
		}
	}
	if got == nil {
		t.Fatalf("apps/lathe is documented and absent under a real parent; got %v", names(rep.Absent))
	}
	if got.Rule != RulePathAnchor {
		t.Errorf("rule = %q, want %q", got.Rule, RulePathAnchor)
	}
}

// --- output shapes -----------------------------------------------------------

func TestMarkdownShape(t *testing.T) {
	md := run(t, runbookRepo(t)).Markdown()
	for _, want := range []string{
		"# nugit adopt —",
		"## The argument",
		"## Prose inventories, and how far behind they are",
		"## Documented but absent",
		"## Present but undocumented",
		"## Disagreements between documents",
		"## Runbook candidates",
		"It is a report, not a gate.",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown missing %q:\n%s", want, md)
		}
	}
	if !strings.Contains(md, "depot-service") {
		t.Error("the pitch must name the phantom")
	}
}

func TestJSONShape(t *testing.T) {
	rep := run(t, runbookRepo(t))
	b, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{
		"repo", "has_nugit_store", "total_commits", "units", "prose_inventories",
		"documented_but_absent", "present_but_undocumented", "disagreements",
		"runbook_candidates",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("json missing key %q", k)
		}
	}
	if _, ok := m["written_candidates"]; ok {
		t.Error("written_candidates must be omitted on a read-only run")
	}
}

// --- degenerate inputs -------------------------------------------------------

func TestEmptyRepoStillReports(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	write(t, dir, "x.txt", "hi\n")
	commitAll(t, dir, "seed")
	rep, err := Run(Options{RepoDir: dir})
	if err != nil {
		t.Fatalf("Run on a bare repo must not fail: %v", err)
	}
	if len(rep.Notes) == 0 {
		t.Error("a repo with no docs and no units must SAY so rather than render an empty pitch")
	}
	if rep.Markdown() == "" {
		t.Error("empty markdown")
	}
}

func TestNonGitDirectoryDoesNotFail(t *testing.T) {
	dir := t.TempDir()
	service(t, dir, "parcel-service")
	write(t, dir, "README.md", "# x\n")
	rep, err := Run(Options{RepoDir: dir})
	if err != nil {
		t.Fatalf("Run outside git must not fail: %v", err)
	}
	if len(rep.Notes) == 0 {
		t.Error("no git history must be reported as a note")
	}
}
