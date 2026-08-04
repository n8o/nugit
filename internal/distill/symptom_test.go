package distill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/trailers"
)

// commitOf builds a model.Commit the way Distill sees one (subject + body +
// parsed trailers), so these tests exercise the real seeding path.
func commitOf(subject, body string) model.Commit {
	return model.Commit{SHA: "0123456789ab", Subject: subject, Body: body, Trailer: trailers.Parse(body)}
}

// The author's `symptom:` trailer wins over everything else: it is the only
// source that KNOWS what was observed.
func TestTriggerPrefersSymptomTrailer(t *testing.T) {
	body := strings.Join([]string{
		"The nightly job crashed with a nil map write while merging the shard results.",
		"",
		"symptom: the nightly rollup wrote a partial report and the operator saw yesterday's numbers on the dashboard all morning",
		"learned: initialize the accumulator map at construction, not at first write",
		"keywords: rollup, nil-map",
	}, "\n")
	got := triggerFor(commitOf("fix(rollup): initialize the accumulator map", body))
	want := "the nightly rollup wrote a partial report and the operator saw yesterday's numbers on the dashboard all morning"
	if got != want {
		t.Errorf("trigger = %q, want the symptom trailer %q", got, want)
	}
}

// With no `symptom:` trailer, a symptom-shaped observation in the body is used.
func TestTriggerScavengesBody(t *testing.T) {
	body := strings.Join([]string{
		"Every pipeline run failed at the publish step with a garbled manifest,",
		"but only on the third attempt of any given day.",
		"",
		"learned: verify the manifest checksum before publishing",
		"keywords: pipeline, manifest",
	}, "\n")
	got := triggerFor(commitOf("fix(publish): verify the manifest checksum", body))
	want := "Every pipeline run failed at the publish step with a garbled manifest, but only on the third attempt of any given day."
	if got != want {
		t.Errorf("trigger = %q, want the scavenged observation %q", got, want)
	}
}

// The whole point of ADR-0028: when nothing observable is knowable, distill
// refuses to fabricate and leaves a visible TODO — it never falls back to the
// commit subject.
func TestTriggerPlaceholderNeverCommitSubject(t *testing.T) {
	subject := "feat: nugit doctor — setup pre-flight health checks"
	body := strings.Join([]string{
		"This change adds a pre-flight command so setup problems surface before review.",
		"",
		"learned: dogfooding a health-check finds real gaps",
		"keywords: doctor, pre-flight",
	}, "\n")
	got := triggerFor(commitOf(subject, body))
	if !strings.HasPrefix(got, TriggerTODO) {
		t.Errorf("trigger = %q, want the %q placeholder", got, TriggerTODO)
	}
	if strings.Contains(got, "nugit doctor") || strings.Contains(got, "feat:") {
		t.Errorf("trigger leaked the commit subject: %q", got)
	}
}

func TestScavengeHits(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{
			name: "negated observation",
			body: "The commit-msg hook did not run for merge commits, so trailer blocks landed unvalidated in the history.",
			want: "The commit-msg hook did not run for merge commits, so trailer blocks landed unvalidated in the history.",
		},
		{
			name: "error artifact",
			body: "Half the requests to the registry came back 503 while the dashboard kept reporting the service as healthy.",
			want: "Half the requests to the registry came back 503 while the dashboard kept reporting the service as healthy.",
		},
		{
			name: "contrasted observation",
			body: "Two agents produced different bundles from the identical store even though both read the same reviewed ref.",
			want: "Two agents produced different bundles from the identical store even though both read the same reviewed ref.",
		},
		{
			name: "quoted log line in a fence",
			body: "Reproduced on a clean checkout:\n\n```\nlevel=error msg=\"reconcile aborted\" node=worker-3 attempts=5 last_error=DEADLINE_EXCEEDED\n```\n",
			want: `level=error msg="reconcile aborted" node=worker-3 attempts=5 last_error=DEADLINE_EXCEEDED`,
		},
		{
			name: "list item",
			body: "Observed twice this week:\n\n- the retry loop spun for 40 minutes on a single stuck job while the queue depth kept climbing\n",
			want: "the retry loop spun for 40 minutes on a single stuck job while the queue depth kept climbing",
		},
		{
			name: "first observation wins, not the later remedy prose",
			body: "The cache served a stale bundle for an hour after the store changed underneath it.\nThe fix invalidates on write so a failed read can never be served twice.",
			want: "The cache served a stale bundle for an hour after the store changed underneath it.",
		},
		{
			name: "hard-wrapped prose is unwrapped into one sentence",
			body: "The exporter silently emitted zero cases for a store\nwith two lessons in it, and the run still exited clean.",
			want: "The exporter silently emitted zero cases for a store with two lessons in it, and the run still exited clean.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scavengeSymptom(tc.body, ""); got != tc.want {
				t.Errorf("scavengeSymptom = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScavengeMisses(t *testing.T) {
	cases := []struct{ name, body, learned string }{
		{
			name: "task description in the narrator's voice",
			body: "This change adds a second index to the lookup table so the planner has a cheaper path.",
		},
		{
			name: "conventional-commit subject quoted into the body",
			body: "Follows feat(distill): seed the lesson trigger from an observable symptom rather than the subject line.",
		},
		{
			name: "plain design prose with no observable cue",
			body: "The parser walks the DSL and records each component with its declared path globs and tags.",
		},
		{
			name: "an observation too thin to diagnose from",
			body: "The build broke.",
		},
		{
			name: "work described, not behavior seen",
			body: "Adds a bounded retry around the publish step and moves the checksum verification ahead of the upload.",
		},
		{
			name: "nothing but the trailer block",
			body: "learned: start consistency checks at warn and promote to fail once the corpus is dense\nkeywords: consistency, spec",
		},
		{
			name:    "the insight restated is the cause, not a symptom",
			body:    "The reader crashed because the accumulator map was never initialized at construction time.",
			learned: "the reader crashed because the accumulator map was never initialized at construction time",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scavengeSymptom(tc.body, tc.learned); got != "" {
				t.Errorf("scavengeSymptom = %q, want no symptom", got)
			}
		})
	}
}

// --- hard-wrapped units, change descriptions, and cut-off candidates ---

// A commit body is hard wrapped at ~72 columns, so a list item's continuation
// lines belong to the item: scoring the first physical line truncates the
// candidate ("… whose dirs the") and orphans its tail as a second unit that
// starts mid-sentence. Numbered items, nested items and wrapped trailer values
// all obey the same rule.
func TestObservationUnitsJoinHardWraps(t *testing.T) {
	body := strings.Join([]string{
		"The loader stalled for six hours before anyone looked at the",
		"queue depth.",
		"",
		"1. the nightly loader timed out after ten minutes and left the",
		"   staging table half written",
		"   - the retry then crashed with a nil map write on the same rows",
		"* the digest job reported success to the scheduler even though",
		"  nothing at all had been written that night",
		"",
		"learned: initialize the accumulator before the first write and",
		"  verify the row count after every batch",
		"keywords: loader, batch",
	}, "\n")
	want := []string{
		"The loader stalled for six hours before anyone looked at the queue depth.",
		"the nightly loader timed out after ten minutes and left the staging table half written",
		"the retry then crashed with a nil map write on the same rows",
		"the digest job reported success to the scheduler even though nothing at all had been written that night",
	}
	got := observationUnits(body)
	if len(got) != len(want) {
		t.Fatalf("observationUnits = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("unit %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// The wrapped bullet is judged as a WHOLE — and a whole observation is kept
// whole, not cut at the wrap.
func TestScavengeJoinsWrappedListItem(t *testing.T) {
	body := strings.Join([]string{
		"Seen twice on the release train:",
		"",
		"- the nightly loader timed out after ten minutes and left the",
		"  staging table half written, while the run still reported success",
		"  to the scheduler",
		"",
		"learned: verify the row count after every batch",
		"keywords: loader, batch",
	}, "\n")
	want := "the nightly loader timed out after ten minutes and left the staging table half written, while the run still reported success to the scheduler"
	if got := scavengeSymptom(body, "verify the row count after every batch"); got != want {
		t.Errorf("scavengeSymptom = %q, want the joined bullet %q", got, want)
	}
}

// ...and once whole, a bullet that describes what the patch DOES is refused,
// even though it carries a failure-lexicon word. Its head is a component label
// plus a change verb phrase: a changelog entry, not something anyone observed.
// Before the fix this body yielded the item's first physical line, truncated at
// the wrap on a dangling "the".
func TestScavengeRefusesChangeDescriptionBullet(t *testing.T) {
	bodies := []struct{ name, body string }{
		{
			name: "label plus a new-thing head",
			body: "Two things move here:\n\n" +
				"- ledger: new `stale-batch` warn, scoped to accounts whose rows the\n" +
				"  import touches and whose totals reconcile to nothing; excluded\n" +
				"  from the nightly digest\n",
		},
		{
			name: "label plus an imperative head",
			body: "- loader: add a crash-safe checkpoint so a timed-out batch never\n" +
				"  leaves the staging table half written\n",
		},
		{
			name: "label plus a now-does head",
			body: "- digest: now reports the failed batch count instead of swallowing\n" +
				"  it, so a stalled night is visible the next morning\n",
		},
		{
			name: "change verb heading the item, label after it",
			body: "- new checkpoint-is-sane check: a timed-out batch and a crashed\n" +
				"  retry both flag, while a clean run stays silent\n",
		},
	}
	for _, tc := range bodies {
		t.Run(tc.name, func(t *testing.T) {
			if got := scavengeSymptom(tc.body, ""); got != "" {
				t.Errorf("scavengeSymptom = %q, want no symptom (the bullet describes the change)", got)
			}
		})
	}
}

// The label is not the tell — what follows it is. A genuine observation keeps
// its component label and is still accepted.
func TestScavengeAcceptsLabelledObservation(t *testing.T) {
	body := "- loader: every batch after the first timed out against the staging\n" +
		"  table, and the scheduler still recorded the night as clean\n"
	want := "loader: every batch after the first timed out against the staging table, and the scheduler still recorded the night as clean"
	if got := scavengeSymptom(body, ""); got != want {
		t.Errorf("scavengeSymptom = %q, want the labelled observation %q", got, want)
	}
}

// The structural backstop, independent of how the candidate was assembled: a
// Trigger that stops mid-clause is refused outright.
func TestCutOffGuard(t *testing.T) {
	cases := []struct {
		s   string
		cut bool
	}{
		{"the batch failed on every retry while the queue depth climbed to", true},
		{"the batch failed on every retry while the queue depth climbed from", true},
		{"every write to the staging table was silently dropped by the", true},
		{"the loader timed out and the digest job reported success,", true},
		{"the loader timed out and the digest job reported success;", true},
		{"the loader timed out and the digest job reported success —", true},
		{"the loader timed out on the third batch of the night", false},
		{"the loader timed out on the third batch of the night.", false},
		{`level=error msg="batch aborted" attempts=5 last_error=DEADLINE_EXCEEDED`, false},
		{"half the rows went missing (the run still exited clean)", false},
	}
	for _, tc := range cases {
		if got := cutOff(tc.s); got != tc.cut {
			t.Errorf("cutOff(%q) = %v, want %v", tc.s, got, tc.cut)
		}
	}
}

// A cut-off candidate is refused by the scavenger even when it carries a cue,
// and finishing the same clause makes it acceptable.
func TestScavengeRefusesCutOffCandidate(t *testing.T) {
	cut := "- the importer crashed on every retry while the staging queue depth climbed steadily to\n"
	if got := scavengeSymptom(cut, ""); got != "" {
		t.Errorf("scavengeSymptom = %q, want no symptom (candidate ends mid-clause)", got)
	}
	whole := "- the importer crashed on every retry while the staging queue depth climbed steadily to four thousand\n"
	want := "the importer crashed on every retry while the staging queue depth climbed steadily to four thousand"
	if got := scavengeSymptom(whole, ""); got != want {
		t.Errorf("scavengeSymptom = %q, want %q", got, want)
	}
}

// Nothing this package produces may end on a dangling function word — the
// end-to-end statement of the defect, asserted with a check written
// independently of the implementation's own regexes.
func TestNoTriggerEndsOnADanglingWord(t *testing.T) {
	bodies := []string{
		"Seen twice on the release train:\n\n" +
			"- the nightly loader timed out after ten minutes and left the\n" +
			"  staging table half written\n\n" +
			"learned: verify the row count after every batch\nkeywords: loader\n",
		"- ledger: new `stale-batch` warn, scoped to accounts whose rows the\n" +
			"  import touches and whose totals reconcile to nothing\n\n" +
			"learned: reconcile before publishing\nkeywords: ledger\n",
		"The digest job reported success to the scheduler even though\n" +
			"nothing at all had been written that night.\n\n" +
			"learned: assert the row count, never the exit code\nkeywords: digest\n",
		"1. every retry after the first crashed with a nil map write on the\n" +
			"   same rows, and the run still exited clean\n\n" +
			"learned: initialize the accumulator at construction\nkeywords: retry\n",
		"Reproduced on a clean checkout:\n\n```\n" +
			"level=error msg=\"batch aborted\" attempts=5 last_error=DEADLINE_EXCEEDED\n" +
			"```\n\nlearned: fail the run when a batch aborts\nkeywords: batch\n",
	}
	dangling := map[string]bool{}
	for _, w := range strings.Fields("a an the of to and or that which whose with for in on at by from") {
		dangling[w] = true
	}
	for _, body := range bodies {
		got := triggerFor(commitOf("fix(loader): checkpoint every batch", body))
		fields := strings.Fields(strings.Trim(got, " \t.)\"'`"))
		last := strings.ToLower(fields[len(fields)-1])
		if dangling[last] {
			t.Errorf("trigger ends on the dangling word %q: %q", last, got)
		}
	}
}

// End to end through Distill: the written lesson file carries the symptom, and
// the commit subject appears nowhere in it.
func TestDistillWritesSymptomTrigger(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")[:40]

	os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", strings.Join([]string{
		"feat(retrieval): rank by scope before truncating",
		"",
		"symptom: agents asking for context on an api file got global lessons first while the component's own ADR was silently dropped at the budget line",
		"decision: order by nearest scope, then truncate",
		"learned: rank by nearest scope before applying the truncation budget",
		"affects: retrieval",
		"keywords: retrieval, budget, scope",
	}, "\n"))

	res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lessons) != 1 {
		t.Fatalf("want 1 lesson, got %v", res.Lessons)
	}
	b, err := os.ReadFile(filepath.Join(dir, res.Lessons[0]))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	if !strings.Contains(got, "**Trigger:** agents asking for context on an api file got global lessons first") {
		t.Errorf("lesson missing the symptom trigger:\n%s", got)
	}
	if strings.Contains(got, "feat(retrieval)") {
		t.Errorf("lesson trigger seeded from the commit subject:\n%s", got)
	}
}

// A commit with no knowable symptom must produce a reviewable gap, not a
// plausible-looking lesson built from its subject line.
func TestDistillWritesPlaceholderTrigger(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q")
	os.WriteFile(filepath.Join(dir, "f"), []byte("a"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "base")
	base := git(t, dir, "rev-parse", "HEAD")[:40]

	os.WriteFile(filepath.Join(dir, "f"), []byte("b"), 0o644)
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", strings.Join([]string{
		"feat: nugit doctor — setup pre-flight health checks",
		"",
		"decision: a dedicated pre-flight command",
		"rejected: fold the checks into pr-render",
		"learned: dogfooding a health-check finds real gaps",
		"keywords: doctor, pre-flight",
	}, "\n"))

	res, err := Distill(Options{RepoDir: dir, Base: base, Head: "HEAD", Now: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Lessons) != 1 {
		t.Fatalf("want 1 lesson, got %v", res.Lessons)
	}
	b, _ := os.ReadFile(filepath.Join(dir, res.Lessons[0]))
	got := string(b)
	if !strings.Contains(got, "**Trigger:** "+TriggerTODO) {
		t.Errorf("lesson missing the TODO trigger:\n%s", got)
	}
	if strings.Contains(got, "nugit doctor — setup pre-flight") {
		t.Errorf("lesson trigger fell back to the commit subject:\n%s", got)
	}
}
