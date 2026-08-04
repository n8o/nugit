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
