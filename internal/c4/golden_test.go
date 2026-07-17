package c4

import (
	"flag"
	"os"
	"strings"
	"testing"
)

// The self-model goldens pin nugit's OWN outputs byte-for-byte across the
// containers-become-first-class change: the `app` wrapper container must stay
// invisible to every existing consumer (go-arch-lint parity, Mermaid render).
// Regenerate deliberately with: go test ./internal/c4 -run Golden -update

var update = flag.Bool("update", false, "rewrite golden files")

func selfDSL(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../.nugit/architecture/workspace.dsl")
	if err != nil {
		t.Fatalf("read own workspace.dsl: %v", err)
	}
	return string(b)
}

func checkGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("missing golden %s (run with -update): %v", path, err)
	}
	if got != string(want) {
		t.Errorf("%s drifted from golden — output must stay byte-identical\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// TestGenArchLintSelfGolden: the generated go-arch-lint config for nugit's own
// model must stay byte-identical, and the `app` wrapper container (no own
// paths) must not appear as a go-arch-lint component.
func TestGenArchLintSelfGolden(t *testing.T) {
	got := GenArchLint(Parse(selfDSL(t)))
	if strings.Contains(got, `"app":`) {
		t.Errorf("paths-less wrapper container `app` leaked into the go-arch-lint config:\n%s", got)
	}
	checkGolden(t, "testdata/genarchlint_self.golden", got)
}

// TestMermaidSelfGolden: the full-model Mermaid render of nugit's own model is
// unchanged by container recording (containers stay out of the diagram this PR).
func TestMermaidSelfGolden(t *testing.T) {
	got := Mermaid(Parse(selfDSL(t)))
	checkGolden(t, "testdata/mermaid_self.golden", got)
}
