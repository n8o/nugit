package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func peerCheck(t *testing.T, r Report) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == "peer stores reachable" {
			return c
		}
	}
	t.Fatal("the peer-stores check is missing from the doctor report")
	return Check{}
}

func writeAt(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func peerRecord(id, status, scope string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: decision\nscope: " + scope +
		"\nstatus: " + status + "\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n---\n\n# " + id + "\n\nbody\n"
}

func TestDoctorReportsNoPeers(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\n")
	c := peerCheck(t, Run(dir))
	if !c.Advisory {
		t.Error("the peer check must be advisory — federation never gates a pre-flight")
	}
	if !c.OK || c.Detail != "no peers configured" {
		t.Errorf("check = %+v", c)
	}
}

// An unreachable peer is a WARNING, never a failure: CI routinely has only the
// repo under review checked out.
func TestDoctorUnreachablePeerIsAdvisoryNotGating(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml",
		"schema_version: 1\npeers:\n  - name: platform\n    path: "+filepath.Join(t.TempDir(), "absent")+"\n")
	r := Run(dir)
	c := peerCheck(t, r)
	if c.OK {
		t.Error("an absent peer must report not-OK so it is visible")
	}
	if !c.Advisory {
		t.Fatal("an absent peer must be ADVISORY — it may never gate doctor")
	}
	if !strings.Contains(c.Detail, "unreachable") || !strings.Contains(c.Detail, "platform") {
		t.Errorf("detail must name the peer and its state: %q", c.Detail)
	}
	// The gating verdict must be unaffected by the peer.
	for _, chk := range r.Checks {
		if chk.Name == "peer stores reachable" && !chk.Advisory {
			t.Fatal("peer check leaked into the gating set")
		}
	}
}

// A reachable peer reports its total AND the honest federatable count: total
// overstates reach, because component-scoped foreign knowledge names nothing here.
func TestDoctorReachablePeerReportsCounts(t *testing.T) {
	dir := t.TempDir()
	peer := t.TempDir()
	writeAt(t, peer, ".nugit/decisions/a.md", peerRecord("ADR-0001", "accepted", "global"))
	writeAt(t, peer, ".nugit/decisions/b.md", peerRecord("ADR-0002", "proposed", "global"))
	writeAt(t, peer, ".nugit/decisions/c.md", peerRecord("ADR-0003", "accepted", "transport"))
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\npeers:\n  - name: platform\n    path: "+peer+"\n")

	c := peerCheck(t, Run(dir))
	if !c.OK {
		t.Errorf("a reachable peer must report OK: %q", c.Detail)
	}
	if !strings.Contains(c.Detail, "3 object(s)") || !strings.Contains(c.Detail, "1 global+ratified") {
		t.Errorf("detail = %q, want 3 objects with 1 global+ratified", c.Detail)
	}
}
