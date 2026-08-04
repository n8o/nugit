package doctor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/n8o/nugit/internal/config"
)

func loadCfg(t *testing.T, dir string) config.Config {
	t.Helper()
	c, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// The three ways a hub can be useless each get their OWN sentence — the
// remediations are unrelated and a single "no hub" would hide two of them
// (ADR-0035 point 3).
func TestHubDegradationsAreDistinguished(t *testing.T) {
	t.Run("none designated", func(t *testing.T) {
		dir := t.TempDir()
		writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\n")
		ok, detail := orgHub(dir, loadCfg(t, dir))
		if !ok || !strings.Contains(detail, "no org hub designated") {
			t.Errorf("ok=%v detail=%q", ok, detail)
		}
	})

	t.Run("names an unconfigured peer", func(t *testing.T) {
		dir := t.TempDir()
		writeAt(t, dir, ".nugit/config.yml",
			"schema_version: 1\norg:\n  hub: ghost\npeers:\n  - name: real\n    path: ../real\n")
		ok, detail := orgHub(dir, loadCfg(t, dir))
		if ok {
			t.Error("a hub naming no configured peer reported healthy")
		}
		if !strings.Contains(detail, "not one of the configured peers") || !strings.Contains(detail, "ghost") {
			t.Errorf("detail = %q", detail)
		}
		if !strings.Contains(detail, "nothing fails") {
			t.Errorf("detail must say the degradation is not a failure: %q", detail)
		}
	})

	t.Run("configured but not checked out", func(t *testing.T) {
		dir := t.TempDir()
		writeAt(t, dir, ".nugit/config.yml",
			"schema_version: 1\norg:\n  repo: me\n  hub: platform\npeers:\n  - name: platform\n    path: ../platform\n")
		ok, detail := orgHub(dir, loadCfg(t, dir))
		if ok {
			t.Error("an absent hub reported healthy")
		}
		if !strings.Contains(detail, "not checked out") {
			t.Errorf("detail = %q", detail)
		}
	})
}

// An absent hub must never gate: doctor reports it and nothing fails, exactly
// like any absent peer (ADR-0032 point 3).
func TestAbsentHubIsAdvisoryOnly(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: me\n  hub: platform\npeers:\n  - name: platform\n    path: ../platform\n")
	writeAt(t, dir, ".nugit/architecture/workspace.dsl",
		"workspace { model { s = softwareSystem \"s\" { c = container \"c\" { } } } }")

	rep := Run(dir)
	var found bool
	for _, c := range rep.Checks {
		if !strings.Contains(strings.ToLower(c.Name), "hub") {
			continue
		}
		found = true
		if c.Name != "org hub" {
			t.Errorf("unexpected hub-related check %q", c.Name)
		}
		if !c.Advisory {
			t.Error("the org-hub check is not advisory — an absent sibling must never gate a pre-flight (ADR-0032 point 3)")
		}
		if c.OK {
			t.Error("an absent hub reported OK")
		}
	}
	if !found {
		t.Fatal("no `org hub` check in the doctor report")
	}
	// Nothing GATING may fail because of the hub: strip the advisory checks and
	// assert the remaining failures are all about this temp dir not being a real
	// checkout (no git, no model), never about federation.
	for _, c := range rep.Checks {
		if c.Advisory || c.OK {
			continue
		}
		if strings.Contains(strings.ToLower(c.Name+c.Detail), "hub") {
			t.Errorf("a GATING check failed because of the hub: %q — %s", c.Name, c.Detail)
		}
	}
}

func TestHealthyHubReportsWhatItHolds(t *testing.T) {
	root := t.TempDir()
	me, hub := filepath.Join(root, "me"), filepath.Join(root, "hub")
	writeAt(t, me, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: me\n  hub: platform\npeers:\n  - name: platform\n    path: ../hub\n")
	writeAt(t, hub, ".nugit/lessons/a.md",
		"---\nschema_version: 1\nid: LESSON-a\ntype: lesson\nscope: global\nstatus: active\n"+
			"created: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# Lesson — a\n\nbody\n")
	writeAt(t, hub, ".nugit/lessons/b.md",
		"---\nschema_version: 1\nid: LESSON-b\ntype: lesson\nscope: global\nstatus: proposed\n"+
			"created: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# Lesson — b\n\nbody\n")

	ok, detail := orgHub(me, loadCfg(t, me))
	if !ok {
		t.Errorf("healthy hub reported unhealthy: %q", detail)
	}
	for _, want := range []string{`hub "platform"`, "2 object(s)", "1 global+ratified", "1 proposed awaiting"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q missing %q", detail, want)
		}
	}
}

// Without an identity nothing can be promoted, and doctor says so rather than
// reporting a green hub that refuses every write.
func TestHubWithoutOrgRepoSaysPromoteWillRefuse(t *testing.T) {
	root := t.TempDir()
	me, hub := filepath.Join(root, "me"), filepath.Join(root, "hub")
	writeAt(t, me, ".nugit/config.yml",
		"schema_version: 1\norg:\n  hub: platform\npeers:\n  - name: platform\n    path: ../hub\n")
	writeAt(t, hub, ".nugit/config.yml", "schema_version: 1\n")

	ok, detail := orgHub(me, loadCfg(t, me))
	if ok || !strings.Contains(detail, "org.repo") {
		t.Errorf("ok=%v detail=%q", ok, detail)
	}
}
