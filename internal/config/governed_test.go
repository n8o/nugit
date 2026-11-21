package config

import (
	"strings"
	"testing"
)

const targetYML = `schema_version: 1
c4:
  dsl: .nugit/architecture/workspace.dsl
  mode: enforce
pr_render:
  fail_on: fail
plan:
  mode: warn
  scope: touched
context_gate: block
`

// The failure this is for: a PR that writes `fail_on: none` and `c4.mode:
// warn` into its own config.yml, and is then judged by them.
func TestGovernKeepsTheTargetsGatesAndTheHeadsTree(t *testing.T) {
	head := strings.NewReplacer(
		"fail_on: fail", "fail_on: none",
		"mode: enforce", "mode: warn",
		"dsl: .nugit/architecture/workspace.dsl", "dsl: model/workspace.dsl",
		"scope: touched", "scope: all",
		"context_gate: block", "context_gate: off",
	).Replace(targetYML)
	g, err := Resolve([]byte(head), []byte(targetYML), true)
	if err != nil {
		t.Fatal(err)
	}
	if !g.FromTarget || g.Note != "" {
		t.Fatalf("the target's config must govern: %+v", g)
	}
	c := g.Config
	if c.PRRender.FailOn != "fail" || c.C4.Mode != "enforce" || c.ContextGate != "block" {
		t.Errorf("governed keys must come from the target, got fail_on=%s c4.mode=%s context_gate=%s",
			c.PRRender.FailOn, c.C4.Mode, c.ContextGate)
	}
	if c.C4.DSL != "model/workspace.dsl" || c.PlanScope() != "all" {
		t.Errorf("head keys must come from head, got dsl=%s scope=%s", c.C4.DSL, c.PlanScope())
	}

	got := map[string]Change{}
	for _, ch := range g.Changes {
		got[ch.Key] = ch
	}
	for key, want := range map[string][2]string{
		"pr_render.fail_on": {"fail", "none"},
		"c4.mode":           {"enforce", "warn"},
		"context_gate":      {"block", "off"},
	} {
		ch, ok := got[key]
		if !ok || ch.Target != want[0] || ch.Head != want[1] {
			t.Errorf("%s: want %s → %s, got %+v (all: %+v)", key, want[0], want[1], ch, g.Changes)
		}
	}
	for _, k := range []string{"c4.dsl", "plan.scope"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s is read at head and is never a governed change: %+v", k, g.Changes)
		}
	}
	if len(g.Changes) != 3 {
		t.Errorf("want exactly the three governed changes, got %+v", g.Changes)
	}
}

// A knob nobody listed is governed: adding a peer or an org id in a PR is a
// change the reviewer sees, because those decide which obligations apply.
func TestAnUnlistedKeyIsGoverned(t *testing.T) {
	head := targetYML + "org:\n  repo: acme/api\npeers:\n  - name: platform\n    path: ../platform\n"
	g, err := Resolve([]byte(head), []byte(targetYML), true)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, ch := range g.Changes {
		keys = append(keys, ch.Key)
	}
	if strings.Join(keys, ",") != "org.repo,peers" {
		t.Fatalf("want org.repo and peers as governed changes, got %v", keys)
	}
	if g.Config.Org.Repo != "" || len(g.Config.Peers) != 0 {
		t.Errorf("the target's identity and peers govern until merge: %+v", g.Config.Org)
	}
}

func TestUnchangedConfigHasNoChanges(t *testing.T) {
	g, err := Resolve([]byte(targetYML), []byte(targetYML), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Changes) != 0 {
		t.Errorf("same config both sides: %+v", g.Changes)
	}
	// A typo that normalizes to the same mode is the same policy.
	typo := strings.Replace(targetYML, "mode: enforce", "mode: enfroce", 1)
	if g, _ := Resolve([]byte(typo), []byte(targetYML), true); len(g.Changes) != 0 {
		t.Errorf("an unknown c4.mode normalizes to enforce, so it is no change: %+v", g.Changes)
	}
}

// The adoption PR: the target has no config, so there is no merged policy to
// evade. Head governs and the finding is measured against the defaults.
func TestNoTargetConfigHeadGoverns(t *testing.T) {
	head := "c4:\n  mode: warn\n"
	g, err := Resolve([]byte(head), nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if g.FromTarget || g.Config.C4.Mode != "warn" || g.Note == "" {
		t.Fatalf("head governs with a note when the target has none: %+v", g)
	}
	if len(g.Changes) != 1 || g.Changes[0].Key != "c4.mode" || g.Changes[0].Target != "enforce" {
		t.Errorf("want c4.mode measured against the default enforce, got %+v", g.Changes)
	}

	// A target config that does not parse must not fail every PR until main
	// is fixed, including the PR that fixes it.
	g, err = Resolve([]byte(head), []byte("c4: [unclosed\n"), true)
	if err != nil || g.FromTarget || !strings.Contains(g.Note, "does not parse") {
		t.Fatalf("a broken target config falls back to head: %+v, %v", g, err)
	}

	// An unreadable target is the pre-ADR-0055 behaviour: head, and nothing
	// to compare against.
	g, err = Resolve([]byte(head), nil, false)
	if err != nil || g.FromTarget || len(g.Changes) != 0 || g.Note != "" {
		t.Fatalf("unreadable target: head governs silently, got %+v", g)
	}
}

func TestMalformedHeadIsStillAnError(t *testing.T) {
	if _, err := Resolve([]byte("c4: [unclosed\n"), []byte(targetYML), true); err == nil {
		t.Fatal("a head config that does not parse is an error, as before")
	}
}
