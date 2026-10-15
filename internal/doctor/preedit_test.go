package doctor

import (
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/agentcfg"
	"github.com/n8o/nugit/internal/config"
)

func TestPreEditHookCheck(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	ok, detail := preEditHook(dir, cfg)
	if ok || !strings.Contains(detail, "-install -hook") || !strings.Contains(detail, "context_gate: warn") {
		t.Errorf("not installed: ok=%v detail=%q", ok, detail)
	}
	write(t, dir, ".claude/settings.local.json", `{"hooks":{"PreToolUse":[{"matcher":"Edit","hooks":[{"type":"command","command":"nugit hook pre-edit"}]}]}}`)
	if ok, detail = preEditHook(dir, cfg); !ok || !strings.Contains(detail, "settings.local.json") {
		t.Errorf("local settings: ok=%v detail=%q", ok, detail)
	}

	dir = t.TempDir()
	if _, _, err := agentcfg.InstallClaudeCodeHook(dir, ""); err != nil {
		t.Fatal(err)
	}
	cfg.ContextGate = "block"
	if ok, detail = preEditHook(dir, cfg); !ok || !strings.Contains(detail, "context_gate: block") {
		t.Errorf("installed: ok=%v detail=%q", ok, detail)
	}
	// The check is advisory: it shows up in Run and never gates AllOK.
	for _, c := range Run(t.TempDir()).Checks {
		if c.Name == "pre-edit hook installed" && !c.Advisory {
			t.Errorf("pre-edit hook check must be advisory")
		}
	}
}
