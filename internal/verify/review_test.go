package verify

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/n8o/nugit/internal/config"
	"github.com/n8o/nugit/internal/model"
	"github.com/n8o/nugit/internal/trailers"
	"github.com/n8o/nugit/internal/usage"
)

// A released commit is stamped as released even when its author wrote a
// claim, and the PR view counts it unverified.
func TestReleaseOverridesAnUnbackedClaim(t *testing.T) {
	m := "feat: y\n\nverified: tests\n"
	recs := []usage.Record{
		{Time: t0, Source: usage.SourceCommit, Session: "s1", Verdict: usage.VerdictBlock},
		{Time: t0.Add(time.Second), Source: usage.SourceCommit, Session: "s1", Verdict: usage.VerdictBlock},
	}
	d := Decide(cfg("enforce"), Commit{Message: m, Session: "s1", Time: t0.Add(time.Minute)}, recs)
	if d.Verdict != usage.VerdictRelease || !strings.Contains(d.Stamp, "released after 2") {
		t.Fatalf("a release is always stamped: %+v", d)
	}
	stamped := appendTrailer(m, d.Stamp)
	tr := trailers.Parse(stamped[strings.IndexByte(stamped, '\n')+1:])
	v := View([]model.Commit{{SHA: "abc", Subject: "feat: y", Trailer: tr}}, Enforce)
	if v.Verified != 0 || len(v.Unverified) != 1 || !v.Unverified[0].Released {
		t.Fatalf("the view must show the released commit as unverified: %+v", v)
	}
	if fs := Findings(v); len(fs) != 1 || !strings.Contains(fs[0].Detail, "released by the gate") {
		t.Errorf("enforce warns on it: %+v", fs)
	}
}

// With no log there is no evidence and no record of blocks to release on,
// so the gate stands down instead of blocking forever.
func TestEnforceStandsDownWithoutALog(t *testing.T) {
	dir := gitInit(t)
	c, _ := config.LoadBytes([]byte("verify:\n  gate: enforce\nusage:\n  log: off\n"))
	f := filepath.Join(dir, "MSG")
	_ = os.WriteFile(f, []byte(msg), 0o644)
	env := map[string]string{usage.SessionEnv: "s1"}
	for i := 0; i < 3; i++ {
		var stderr bytes.Buffer
		if code := CommitMsg(dir, f, &stderr, Options{Now: t0, Config: &c, Getenv: func(k string) string { return env[k] }}); code != 0 {
			t.Fatalf("attempt %d blocked with the log off: %s", i+1, stderr.String())
		}
	}
}

// An edit the context gate denied never changed the tree, so evidence
// before it is still fresh.
func TestDeniedEditDoesNotStaleEvidence(t *testing.T) {
	no := false
	recs := []usage.Record{
		ev(t0, "s1", "tests"),
		{Time: t0.Add(time.Minute), Source: usage.SourceHook, Session: "s1", Gate: "block", Covered: &no},
	}
	d := Decide(cfg("enforce"), Commit{Message: msg, Session: "s1", Time: t0.Add(2 * time.Minute)}, recs)
	if d.Verdict != usage.VerdictVerified {
		t.Fatalf("a denied edit must not make the tests stale: %+v", d)
	}
}

// Merge commits carry no work of their own and no hook could stamp one.
func TestViewSkipsMerges(t *testing.T) {
	v := View([]model.Commit{
		{SHA: "a", Subject: "feat", Trailer: model.Trailer{Verified: "tests"}},
		{SHA: "m", Subject: "Merge branch 'main' into feat", Merge: true},
	}, Enforce)
	if v.Commits != 1 || len(v.Unverified) != 0 || len(Findings(v)) != 0 {
		t.Fatalf("the merge is not a commit to verify: %+v", v)
	}
}

func TestTestCommandIgnoresQuotesAndHeredocs(t *testing.T) {
	p := config.DefaultVerifyTests
	for _, tc := range []struct {
		cmd, want string
	}{
		{`git commit -m "fix; go test green"`, ""},
		{"git commit -F - <<'EOF'\nfeat: x\n\nmake test passes now\nEOF", ""},
		{"git commit -F - <<'EOF'\nmsg\nEOF\ngo test ./...", "go test"},
		{`cd api && GOFLAGS=-count=1 go test ./... 2>&1 | tail -5`, "go test"},
		{`echo 'a | go test' ; npm test`, "npm test"},
		{`go vet ./... && pytest -q`, "pytest"},
	} {
		if got := TestCommand(tc.cmd, p); got != tc.want {
			t.Errorf("TestCommand(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}
