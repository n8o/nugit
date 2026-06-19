package narrative

import (
	"os"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/model"
)

func arch() model.Report {
	return model.Report{Significance: model.Significance{Tier: model.TierArchitectural}}
}

// Generate must be inert (return "" and make NO network call) when disabled, when
// not architectural, or when no API key is present.
func TestGenerateInertWhenOff(t *testing.T) {
	os.Unsetenv("ANTHROPIC_API_KEY")

	if got := Generate(arch(), t.TempDir(), false, ""); got != "" {
		t.Errorf("disabled must return empty, got %q", got)
	}
	trivial := model.Report{Significance: model.Significance{Tier: model.TierTrivial}}
	if got := Generate(trivial, t.TempDir(), true, ""); got != "" {
		t.Errorf("non-architectural must return empty, got %q", got)
	}
	// enabled + architectural but no key -> still inert (no panic, no call, "")
	if got := Generate(arch(), t.TempDir(), true, ""); got != "" {
		t.Errorf("no API key must return empty, got %q", got)
	}
}

func TestCacheRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if _, ok := readCache(dir, "abc"); ok {
		t.Error("empty cache should miss")
	}
	writeCache(dir, "abc", "a summary")
	if got, ok := readCache(dir, "abc"); !ok || got != "a summary" {
		t.Errorf("cache round-trip failed: %q %v", got, ok)
	}
}

func TestBuildPromptFactsOnly(t *testing.T) {
	rep := arch()
	rep.Significance.Reasons = []string{"C4 model changed"}
	rep.Commits = []model.Commit{{Subject: "feat: add widget"}}
	rep.Findings = []model.Finding{{Severity: "warn", Check: "c4<->code", Title: "undeclared a → b"}}
	p := buildPrompt(rep)
	for _, want := range []string{"add widget", "undeclared a → b", "do not invent"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q:\n%s", want, p)
		}
	}
}
