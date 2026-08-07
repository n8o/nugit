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
	var ports *Disagreement
	for i := range rep.Disagreements {
		d := &rep.Disagreements[i]
		if d.Unit == "parcel-service" && d.Attr == "port" {
			ports = d
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
	// crate-service agrees in both documents: silence and agreement must never
	// be reported as conflict.
	for _, d := range rep.Disagreements {
		if d.Unit == "crate-service" {
			t.Errorf("crate-service agrees in both documents but was reported: %+v", d)
		}
	}
}

// The two documents in baseRepo place parcel-service in two different
// directories (`apps/parcel-service` vs `services/parcel`). That is NOT reported
// as a disagreement, and the omission is deliberate: a document listing files
// under a component is not asserting the component's location, and on a real
// monorepo every single "path disagreement" (7 of 7) was two documents citing
// different, individually-correct file subsets. Only scalar facts — where two
// values cannot both be true — are contradictions. See ADR-0036.
func TestSetValuedPathDifferencesAreNotDisagreements(t *testing.T) {
	rep := run(t, baseRepo(t))
	for _, d := range rep.Disagreements {
		if d.Attr != "port" {
			t.Errorf("non-scalar disagreement reported: %+v", d)
		}
	}
	// And the headline must not count them either.
	for _, line := range rep.Headlines() {
		if strings.Contains(line, "disagreement") && !strings.Contains(line, "1 disagreement") {
			t.Errorf("headline counts more than the one scalar conflict: %q", line)
		}
	}
}

// A bare number in a table cell is a port only when the column heads as one.
// A service-configuration table whose "Default" column holds a poll interval in
// milliseconds otherwise manufactures a port conflict for every row it has.
func TestBareNumbersAreOnlyPortsInAPortColumn(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service")
	service(t, dir, "crate-service")
	write(t, dir, "AGENTS.md", "# Agents\n\n| service | port |\n|---|---|\n| `parcel-service` | 8080 |\n| `crate-service` | 8081 |\n")
	write(t, dir, "docs/config.md", `# Configuration

| service | variable | default | description |
|---|---|---|---|
| `+"`parcel-service`"+` | `+"`POLL_INTERVAL_MS`"+` | 15000 | Collector poll interval |
| `+"`crate-service`"+` | `+"`FLUSH_MS`"+` | 2500 | Flush interval |
`)
	commitAll(t, dir, "seed")
	rep := run(t, dir)
	if len(rep.Disagreements) != 0 {
		t.Errorf("a millisecond interval in a `default` column was read as a port: %+v", rep.Disagreements)
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

// The measured failure of the removed "family affix" rule, as a fixture.
//
// That rule admitted any token sharing a same-position separator segment with
// two detected units. It is sharp when a repo's units share a distinctive ROLE
// suffix and it collapses when they share ordinary DOMAIN NOUNS — which is what
// this synthetic repo does (press-*, sheet-*, *-folder, *-binder). Every token
// below carried the family shape and none of them is a unit: a struct field, two
// local variables, a Dockerfile name, and a hyphenated noun phrase in a bullet.
// All of these shapes were confirmed admitted by the old rule, on this exact
// fixture, before it was removed.
func TestDomainNounAffixesAreNotUnitClaims(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	for _, s := range []string{"press-folder", "press-binder", "sheet-folder", "sheet-binder"} {
		service(t, dir, s)
	}
	write(t, dir, "docs/design.md", `# Bindery pipeline design

The feeder assigns `+"`sheet_margin = Stock::Heavy`"+` before the first signature is
handed to the folder, and the plural form is already carried in `+"`sheet_margin[]`"+`.

Inside the fold loop we keep a local `+"`press_width`"+` next to `+"`sheet_gain`"+` so the
trimmer does not have to re-read the descriptor every tick.

Build the offline binder with `+"`Dockerfile.press-binder`"+`; the base layer is
shared with the online one.

- The `+"`sheet-latency`"+` budget is dominated by the folder, not the trimmer
- The press path is the one to watch under load
`)
	commitAll(t, dir, "seed")
	rep := run(t, dir)
	if len(rep.Absent) != 0 {
		t.Errorf("code identifiers and config keys became phantoms %v — resemblance to a unit name is not evidence about the repo", names(rep.Absent))
	}
}

// The leading half of the filename test. A trailing extension is caught by
// fileExt; `Dockerfile.press-binder` puts the type word FIRST and normalizes
// into a perfectly unit-shaped two-segment name.
func TestFileTypeLeadingSegmentIsNotAUnit(t *testing.T) {
	for _, tok := range []string{
		"Dockerfile.press-binder", "dockerfile.gpu-base", "Makefile.local",
		"CMakeLists.shared", "README.internal", "Jenkinsfile.release",
	} {
		if plausibleToken(tok) {
			t.Errorf("plausibleToken(%q) = true — a build file is not a unit", tok)
		}
	}
	// The word only disqualifies in the LEADING position: a unit may be named
	// after what it does with those files.
	if !plausibleToken("dockerfile-linter") {
		t.Error(`plausibleToken("dockerfile-linter") = false; "dockerfile" leads a hyphenated NAME here, and the reject is for "<type>.<subject>" filenames`)
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
	}, dir)
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

// --- path-anchor needs this repo's own history (ADR-0038) --------------------

// citingRepo is the cross-repo fixture: two real services, and a document that
// cites `apps/lathe` under the same real parent. Whether that citation is this
// repo's phantom is NOT decidable from the layout — two sibling repos in one org
// share a layout — so the history decides it.
func citingRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "anvil")
	service(t, dir, "chisel")
	write(t, dir, "README.md", "# Platform\n\nThe milling path lives in `apps/lathe` and the forging path in `apps/anvil`.\n")
	commitAll(t, dir, "seed")
	return dir
}

// THE DEFECT. A document about the boundary with a sibling repo names the
// sibling's directories; the parent exists here (both repos lay their services
// out the same way) and the full path does not, so path-anchor called it this
// repo's phantom. Measured on a real repo: 5 of 6 documented-but-absent were
// this shape, every one of them a real directory in the sibling.
func TestAPathThisRepoNeverContainedIsNotItsPhantom(t *testing.T) {
	rep := run(t, citingRepo(t))
	if has(names(rep.Absent), "lathe") {
		t.Errorf("apps/lathe was never in this repo's history, but it was asserted as this repo's phantom: %v", names(rep.Absent))
	}
	var got *Elsewhere
	for i := range rep.Elsewhere {
		if rep.Elsewhere[i].Name == "lathe" {
			got = &rep.Elsewhere[i]
		}
	}
	if got == nil {
		t.Fatalf("a cited path this repo never contained belongs in its own bucket; got %+v", rep.Elsewhere)
	}
	if got.Rule != RulePathAnchor {
		t.Errorf("rule = %q, want %q", got.Rule, RulePathAnchor)
	}
	if got.Peer != "" {
		t.Errorf("no peers were configured, so nothing may be attributed to one: %+v", got)
	}
	if len(got.Mentions) == 0 {
		t.Error("an unattributed citation with no mentions is unauditable")
	}
	md := rep.Markdown()
	if !strings.Contains(md, "## Cited here, but never in this repo") {
		t.Errorf("the bucket must be its own clearly-labelled section:\n%s", md)
	}
	if !strings.Contains(md, "No peers are configured") {
		t.Error("with no peers the report must say it could only rule this repo out")
	}
}

// ...and the same rule with the history AGREEING is still a phantom, now with
// the commit that removed it. Deterministic, cheap, and the same principle
// ADR-0037 established: ask the version-control system, it already knows.
func TestADeletedPathIsStillReportedAsAPhantom(t *testing.T) {
	dir := citingRepo(t)
	service(t, dir, "lathe")
	commitAll(t, dir, "add the milling service")
	if err := os.RemoveAll(filepath.Join(dir, "apps", "lathe")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "retire the milling service")

	rep := run(t, dir)
	var got *Absent
	for i := range rep.Absent {
		if rep.Absent[i].Name == "lathe" {
			got = &rep.Absent[i]
		}
	}
	if got == nil {
		t.Fatalf("this repo tracked and deleted apps/lathe and still documents it; got absent=%v elsewhere=%+v", names(rep.Absent), rep.Elsewhere)
	}
	if got.Rule != RulePathAnchor {
		t.Errorf("rule = %q, want %q", got.Rule, RulePathAnchor)
	}
	if got.DeletedIn == "" || got.DeletedPath != "apps/lathe" {
		t.Errorf("a history-corroborated phantom must cite the commit that removed it: %+v", got)
	}
	for _, e := range rep.Elsewhere {
		if e.Name == "lathe" {
			t.Errorf("lathe is in both buckets: %+v", e)
		}
	}
	if !strings.Contains(rep.Markdown(), "this repo deleted `apps/lathe` in") {
		t.Error("the pitch must show the deletion that corroborates the finding")
	}
}

// Co-location is untouched by the narrowing. Its corroboration is two
// independent documents of THIS repo naming the same missing unit — a claim the
// repo makes about itself — and requiring the index to agree as well would
// silence the genuine finding measured on a real repo, where the one true
// positive of six was admitted by exactly this rule.
func TestColocatedPhantomsDoNotNeedTheHistory(t *testing.T) {
	rep := run(t, baseRepo(t))
	var got *Absent
	for i := range rep.Absent {
		if rep.Absent[i].Name == "depot-service" {
			got = &rep.Absent[i]
		}
	}
	if got == nil {
		t.Fatalf("the co-located phantom must survive: absent=%v elsewhere=%+v", names(rep.Absent), rep.Elsewhere)
	}
	if got.Rule != RuleColocated {
		t.Errorf("rule = %q, want %q", got.Rule, RuleColocated)
	}
	if got.DeletedIn != "" {
		t.Errorf("depot-service was never in this repo's history; it must not claim a deletion: %+v", got)
	}
}

// ...and where the history DOES have one, the co-located finding cites it. The
// true positive measured on a real repo is a bare NAME in three tables — the
// prose never writes its directory — so the slot it would occupy under a
// unit-bearing parent is asked about too. Without that the report can assert
// "this unit is gone" but cannot show when.
func TestACoLocatedPhantomCitesItsDeletionWhenThereIsOne(t *testing.T) {
	dir := baseRepo(t)
	service(t, dir, "depot-service")
	commitAll(t, dir, "add the depot service")
	if err := os.RemoveAll(filepath.Join(dir, "apps", "depot-service")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "retire the depot service")

	rep := run(t, dir)
	var got *Absent
	for i := range rep.Absent {
		if rep.Absent[i].Name == "depot-service" {
			got = &rep.Absent[i]
		}
	}
	if got == nil {
		t.Fatalf("absent = %v, want the deleted depot-service", names(rep.Absent))
	}
	if got.Rule != RuleColocated {
		t.Errorf("rule = %q, want %q — the history corroborates, it does not re-admit", got.Rule, RuleColocated)
	}
	if got.DeletedPath != "apps/depot-service" || got.DeletedIn == "" {
		t.Errorf("a bare-name phantom whose slot this repo deleted must cite the commit: %+v", got)
	}
}

// With no history to ask, the layout alone may not assert a phantom — and the
// report says why rather than silently reporting one number or the other.
func TestWithoutAHistoryTheLayoutAloneAssertsNothing(t *testing.T) {
	dir := t.TempDir() // deliberately NOT a git repo
	service(t, dir, "anvil")
	service(t, dir, "chisel")
	write(t, dir, "README.md", "# Platform\n\nThe milling path lives in `apps/lathe`.\n")
	rep, err := Run(Options{RepoDir: dir})
	if err != nil {
		t.Fatalf("Run outside git must not fail: %v", err)
	}
	if len(rep.Absent) != 0 {
		t.Errorf("no history is readable, so nothing may be asserted from the layout: %v", names(rep.Absent))
	}
	var noted bool
	for _, n := range rep.Notes {
		if strings.Contains(n, RulePathAnchor) {
			noted = true
		}
	}
	if !noted {
		t.Errorf("the report must name what it could not corroborate: %v", rep.Notes)
	}
}

// --- peers earn their keep (ADR-0032 spent at adoption time) -----------------

func TestPeerAttributionNamesTheRepoThatHasIt(t *testing.T) {
	sibling := t.TempDir()
	initRepo(t, sibling)
	service(t, sibling, "lathe")
	commitAll(t, sibling, "seed")

	dir := citingRepo(t)
	rep, err := Run(Options{RepoDir: dir, Peers: []Peer{{Name: "bindery", Dir: sibling}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if has(names(rep.Absent), "lathe") {
		t.Errorf("a path that lives in a configured peer must not be called absent: %v", names(rep.Absent))
	}
	var got *Elsewhere
	for i := range rep.Elsewhere {
		if rep.Elsewhere[i].Name == "lathe" {
			got = &rep.Elsewhere[i]
		}
	}
	if got == nil {
		t.Fatalf("elsewhere = %+v, want lathe attributed to the peer", rep.Elsewhere)
	}
	if got.Peer != "bindery" || got.PeerPath != "apps/lathe" {
		t.Errorf("attribution = %+v, want peer bindery at apps/lathe", got)
	}
	if len(rep.Peers) != 1 || !rep.Peers[0].Present {
		t.Errorf("peer status = %+v, want one present peer", rep.Peers)
	}
	if md := rep.Markdown(); !strings.Contains(md, "lives in peer `bindery`") {
		t.Errorf("the pitch must name the repo that has it:\n%s", md)
	}
}

// A peer that is not checked out is the NORMAL state in CI (ADR-0032 clause 3):
// it is reported and contributes nothing, and the report falls back to history
// corroboration alone.
func TestAbsentPeerDegradesToTheHistoryRule(t *testing.T) {
	dir := citingRepo(t)
	rep, err := Run(Options{RepoDir: dir, Peers: []Peer{{Name: "bindery", Dir: filepath.Join(dir, "no-such-checkout")}}})
	if err != nil {
		t.Fatalf("an absent peer must never error the report: %v", err)
	}
	if has(names(rep.Absent), "lathe") {
		t.Errorf("an unreachable peer must not turn a cross-repo citation into a phantom: %v", names(rep.Absent))
	}
	if len(rep.Elsewhere) != 1 || rep.Elsewhere[0].Peer != "" {
		t.Errorf("elsewhere = %+v, want lathe with no attribution", rep.Elsewhere)
	}
	if len(rep.Peers) != 1 || rep.Peers[0].Present {
		t.Errorf("peer status = %+v, want one absent peer", rep.Peers)
	}
	var noted bool
	for _, n := range rep.Notes {
		if strings.Contains(n, "bindery") {
			noted = true
		}
	}
	if !noted {
		t.Errorf("an absent peer must be reported, not silently ignored: %v", rep.Notes)
	}
}

// A unit this repo really deleted and a sibling really has is BOTH: still this
// repo's phantom (its prose is stale) and now the peer's code. The finding stays
// where a reader needs it and carries the peer as extra information.
func TestAMovedUnitIsStillThisReposPhantom(t *testing.T) {
	sibling := t.TempDir()
	initRepo(t, sibling)
	service(t, sibling, "lathe")
	commitAll(t, sibling, "seed")

	dir := citingRepo(t)
	service(t, dir, "lathe")
	commitAll(t, dir, "add the milling service")
	if err := os.RemoveAll(filepath.Join(dir, "apps", "lathe")); err != nil {
		t.Fatal(err)
	}
	commitAll(t, dir, "move the milling service to the bindery repo")

	rep, err := Run(Options{RepoDir: dir, Peers: []Peer{{Name: "bindery", Dir: sibling}}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got *Absent
	for i := range rep.Absent {
		if rep.Absent[i].Name == "lathe" {
			got = &rep.Absent[i]
		}
	}
	if got == nil {
		t.Fatalf("a unit this repo deleted is still its phantom; got %+v / %+v", rep.Absent, rep.Elsewhere)
	}
	if got.DeletedIn == "" || got.Peer != "bindery" {
		t.Errorf("a moved unit must carry both the deletion and the peer: %+v", got)
	}
}

// The co-location quorum spends "names a unit of this repo", not "has a
// detector". The detectors have documented blind spots (ADR-0021: no Python, no
// Rust), so a service table in which most rows are real directories under a
// unit-bearing parent IS an inventory of this repo — and the one row that has no
// directory is the finding. Measured: with the narrower currency such a table
// failed to qualify at all, and its genuine phantom had to be admitted by a
// lexical rule that admitted forty other things with it.
func TestUndetectedSiblingsCarryTheQuorum(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service") // the only DETECTED unit
	// Real services the detectors cannot see: a directory, no Dockerfile.
	for _, s := range []string{"crate-service", "pallet-service", "hopper-service", "chute-service"} {
		write(t, dir, "apps/"+s+"/main.py", "print('hi')\n")
	}
	inventory := "# Services\n\n| service | port |\n|---|---|\n" +
		"| `parcel-service` | 8080 |\n| `crate-service` | 8081 |\n| `pallet-service` | 8082 |\n" +
		"| `hopper-service` | 8083 |\n| `chute-service` | 8084 |\n| `depot-service` | 8085 |\n"
	write(t, dir, "AGENTS.md", inventory)
	write(t, dir, "docs/architecture.md", inventory)
	commitAll(t, dir, "seed")

	rep := run(t, dir)
	got := names(rep.Absent)
	if !has(got, "depot-service") {
		t.Errorf("documented-but-absent = %v, want depot-service — 5 of 6 rows are real, so the table is an inventory", got)
	}
	for _, a := range rep.Absent {
		if a.Name != "depot-service" {
			t.Errorf("a real-but-undetected service was reported absent: %q (%s)", a.Name, a.Rule)
		}
		if a.Rule != RuleColocated {
			t.Errorf("rule = %q, want %q", a.Rule, RuleColocated)
		}
	}
}

// ...and the currency stops at SIBLINGS of units. A directory at the repo root
// is not a unit, and counting one turns a license table's "Status" column into
// an inventory — measured, on a real repo, where it minted `production-ready`.
func TestRootDirectoriesAreNotQuorumCurrency(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service")
	service(t, dir, "crate-service")
	write(t, dir, "build/.keep", "")
	write(t, dir, "scripts/.keep", "")
	write(t, dir, "docs/choices.md", `# Tooling choices

| concern | tool | status |
|---|---|---|
| Packaging | Nix | build |
| Linting | Ruff | scripts |
| Release | Custom | production-ready |
`)
	commitAll(t, dir, "seed")
	rep := run(t, dir)
	if len(rep.Absent) != 0 {
		t.Errorf("a tooling table became an inventory because `build/` and `scripts/` exist: %v", names(rep.Absent))
	}
}

// Co-location's evidence is statistical — "most of this group is real, so the
// rest of it is too" — and one qualifying group in one document is one editor's
// list. A plan document tabling a service it intends to build, or a migration
// step naming a database table, both produce one. Two independent documents
// naming the same missing unit is the shape of the finding this report is for.
func TestOneDocumentIsNotEnoughForACoLocatedPhantom(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service")
	service(t, dir, "crate-service")
	service(t, dir, "pallet-service")
	write(t, dir, "docs/plan.md", `# Plan

| service | language | role |
|---|---|---|
| `+"`parcel-service`"+` | Go | intake |
| `+"`crate-service`"+` | Go | fan-out |
| `+"`pallet-service`"+` | Go | storage |
| `+"`depot-service`"+` | Go | planned, not built |
`)
	commitAll(t, dir, "seed")
	if rep := run(t, dir); len(rep.Absent) != 0 {
		t.Errorf("a single plan document minted %v — one document is one editor", names(rep.Absent))
	}
	// A second document naming the same missing service corroborates it.
	write(t, dir, "AGENTS.md", `# Agents

| service | port |
|---|---|
| `+"`parcel-service`"+` | 8080 |
| `+"`crate-service`"+` | 8081 |
| `+"`depot-service`"+` | 8082 |
`)
	commitAll(t, dir, "second inventory")
	rep := run(t, dir)
	if !has(names(rep.Absent), "depot-service") {
		t.Errorf("two documents name depot-service and no such directory exists; got %v", names(rep.Absent))
	}
}

// A real directory must never be reported absent, and prose does not spell
// directories the way the filesystem does. `libs/crate_index` written with
// underscores answers to the claim filed under `crate-index`, and a claim filed
// under the BASENAME of a deep path (`.../api/v1` files under `v1`) is checked
// against the path the document actually wrote.
func TestRealDirectoriesAreNeverPhantoms(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	service(t, dir, "parcel-service")
	service(t, dir, "crate-service")
	write(t, dir, "libs/crate_index/README.md", "# index\n")
	write(t, dir, "apps/parcel-service/api/v1/schema.txt", "x\n")
	// A unit that is itself a workspace, repeating the layout inside itself.
	write(t, dir, "apps/parcel-service/apps/console/index.txt", "x\n")
	doc := "# Layout\n\n" +
		"| component | source |\n|---|---|\n" +
		"| `parcel-service` | `apps/parcel-service` |\n" +
		"| `crate-service` | `apps/crate-service` |\n" +
		"| `crate-index` | `libs/crate_index` |\n" +
		"| `schema` | `apps/parcel-service/api/v1` |\n" +
		"| `console` | `apps/console` |\n"
	write(t, dir, "AGENTS.md", doc)
	write(t, dir, "docs/architecture.md", doc)
	commitAll(t, dir, "seed")
	if rep := run(t, dir); len(rep.Absent) != 0 {
		t.Errorf("every name in this document resolves to a real directory; reported %v", names(rep.Absent))
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
