package doctor

import (
	"strings"
	"testing"
)

func landscapeCheck(t *testing.T, r Report) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == "org landscape" {
			return c
		}
	}
	t.Fatal("the org-landscape check is missing from the doctor report")
	return Check{}
}

const doctorLandscape = `workspace {
  model {
    gateway = softwareSystem "Consumer Gateway" {
      properties { "nugit_repo" "consumer-gateway" }
    }
    registry = softwareSystem "Shared artifact registry" {
      properties {
        "nugit_owner" "producer-service"
        "nugit_paths" "platform/registry/**"
      }
    }
  }
}
`

func TestDoctorReportsNoLandscape(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\n")
	c := landscapeCheck(t, Run(dir))
	if !c.Advisory {
		t.Error("the landscape check must be advisory — a landscape is optional and never gates setup")
	}
	if !c.OK || !strings.Contains(c.Detail, "no landscape.dsl found") {
		t.Errorf("check = %+v", c)
	}
}

func TestDoctorReportsLocalLandscapeAndWhere(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: consumer-gateway\n")
	writeAt(t, dir, ".nugit/architecture/landscape.dsl", doctorLandscape)
	// The peer supplies the identity that legitimises `producer-service`.
	peer := t.TempDir()
	writeAt(t, peer, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: producer-service\n")
	writeAt(t, dir, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: consumer-gateway\npeers:\n  - name: producer\n    path: "+peer+"\n")

	c := landscapeCheck(t, Run(dir))
	if !c.OK {
		t.Errorf("a healthy landscape must pass: %+v", c)
	}
	for _, want := range []string{"local .nugit/architecture/landscape.dsl", "2 system(s)", "1 shared", "1 declaring nugit_repo"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail must contain %q, got %q", want, c.Detail)
		}
	}
}

// An invalid glob matches nothing, so the ownership check can NEVER fire for
// that system — exactly the kind of silent uselessness doctor exists to name.
func TestDoctorReportsInvalidLandscapeGlobs(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: consumer-gateway\n")
	writeAt(t, dir, ".nugit/architecture/landscape.dsl", `workspace { model {
	  s = softwareSystem "S" { properties { "nugit_owner" "consumer-gateway" "nugit_paths" "[bad" } }
	}}`)
	c := landscapeCheck(t, Run(dir))
	if c.OK {
		t.Error("an invalid glob must be reported as not-OK (advisory)")
	}
	if !strings.Contains(c.Detail, "invalid nugit_paths glob \"[bad\"") {
		t.Errorf("detail = %q", c.Detail)
	}
	if c.Advisory != true {
		t.Error("still advisory — a bad glob is modelling debt, not a setup failure")
	}
}

// A `nugit_owner` naming a repo id nothing here declares is a typo nothing else
// can catch: the ownership check would silently never fire.
func TestDoctorReportsDanglingRepoIDs(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: consumer-gateway\n")
	writeAt(t, dir, ".nugit/architecture/landscape.dsl", `workspace { model {
	  a = softwareSystem "A" { properties { "nugit_repo" "consumer-gateway" } }
	  b = softwareSystem "B" { properties { "nugit_owner" "prodcuer-service" "nugit_paths" "x/**" } }
	}}`)
	c := landscapeCheck(t, Run(dir))
	if c.OK {
		t.Error("a dangling owner id must be reported")
	}
	if !strings.Contains(c.Detail, "prodcuer-service") {
		t.Errorf("detail must name the dangling id: %q", c.Detail)
	}
	// A repo id the landscape itself declares is NOT dangling.
	if strings.Contains(c.Detail, "consumer-gateway —") {
		t.Errorf("a declared repo id must not be flagged: %q", c.Detail)
	}
}

// The ambiguous case: two peers each declaring a landscape. nugit uses NOTHING
// and says which repos are claiming it (ADR-0011).
func TestDoctorReportsAmbiguousLandscape(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := t.TempDir(), t.TempDir()
	writeAt(t, p1, ".nugit/architecture/landscape.dsl", doctorLandscape)
	writeAt(t, p2, ".nugit/architecture/landscape.dsl", doctorLandscape)
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\npeers:\n"+
		"  - name: zeta\n    path: "+p1+"\n  - name: alpha\n    path: "+p2+"\n")

	c := landscapeCheck(t, Run(dir))
	if c.OK {
		t.Error("ambiguity must be reported as not-OK")
	}
	if !strings.Contains(c.Detail, "alpha, zeta") || !strings.Contains(c.Detail, "NOTHING is used") {
		t.Errorf("detail must name every claimant and say nothing was used: %q", c.Detail)
	}
	if !c.Advisory {
		t.Error("still advisory — never a pre-flight failure")
	}
}

// A local landscape ends the ambiguity outright: it always wins and no peer
// landscape is read.
func TestDoctorLocalLandscapeWinsOverPeers(t *testing.T) {
	dir := t.TempDir()
	p1, p2 := t.TempDir(), t.TempDir()
	writeAt(t, p1, ".nugit/architecture/landscape.dsl", doctorLandscape)
	writeAt(t, p2, ".nugit/architecture/landscape.dsl", doctorLandscape)
	writeAt(t, dir, ".nugit/architecture/landscape.dsl", doctorLandscape)
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: producer-service\npeers:\n"+
		"  - name: zeta\n    path: "+p1+"\n  - name: alpha\n    path: "+p2+"\n")

	c := landscapeCheck(t, Run(dir))
	if !strings.Contains(c.Detail, "landscape from local") {
		t.Errorf("local must win: %q", c.Detail)
	}
}
