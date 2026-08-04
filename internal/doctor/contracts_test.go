package doctor

import (
	"strings"
	"testing"
)

func contractCheck(t *testing.T, r Report) Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == "cross-repo contract obligations" {
			return c
		}
	}
	t.Fatal("the cross-repo contract check is missing from the doctor report")
	return Check{}
}

func contractDoc(id, party, status, file string) string {
	return "---\nschema_version: 1\nid: " + id + "\ntype: contract\nscope: global\nstatus: " + status +
		"\ncreated: 2026-01-01T00:00:00Z\nprovenance:\n  commit: x\n" +
		"parties:\n  - repo: " + party + "\n    must:\n" +
		"      - name: mirror guard present\n        file: " + file + "\n        matches: 'useStandardProtocols'\n" +
		"---\n\n# " + id + "\n\nbody\n"
}

// No identity ⇒ the check states it is inert. "Not configured" and "nothing
// unmet" are different facts and must read differently.
func TestDoctorContractsInertWithoutIdentity(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\n")
	c := contractCheck(t, Run(dir))
	if !c.Advisory {
		t.Error("the contract check must be advisory — it may never gate a pre-flight")
	}
	if !c.OK || !strings.Contains(c.Detail, "org.repo is not configured") {
		t.Errorf("check = %+v", c)
	}
}

// The advisory answers exactly the two questions ADR-0033 asks of doctor: how
// many contracts name this repo, and how many obligations are unmet.
func TestDoctorCountsContractsAndUnmetObligations(t *testing.T) {
	dir := t.TempDir()
	peer := t.TempDir()
	writeAt(t, peer, ".nugit/contracts/a.md", contractDoc("CONTRACT-0001", "consumer-gateway", "accepted", "apps/gateway/server.cpp"))
	writeAt(t, peer, ".nugit/contracts/b.md", contractDoc("CONTRACT-0002", "consumer-gateway", "accepted", "apps/gateway/other.cpp"))
	writeAt(t, peer, ".nugit/contracts/c.md", contractDoc("CONTRACT-0003", "someone-else", "accepted", "apps/gateway/server.cpp"))
	writeAt(t, dir, ".nugit/config.yml",
		"schema_version: 1\norg:\n  repo: consumer-gateway\npeers:\n  - name: producer\n    path: "+peer+"\n")
	// One half honored, one not.
	writeAt(t, dir, "apps/gateway/server.cpp", "useStandardProtocols(f);\n")

	c := contractCheck(t, Run(dir))
	if !c.Advisory {
		t.Fatal("the contract check must be ADVISORY — an unmet obligation is backlog, not a blocked setup")
	}
	if c.OK {
		t.Error("an unmet obligation must report not-OK so it is visible")
	}
	for _, want := range []string{`2 contract(s) name "consumer-gateway"`, "2 obligation(s), 1 unmet",
		"producer:CONTRACT-0002", "mirror guard present"} {
		if !strings.Contains(c.Detail, want) {
			t.Errorf("detail must contain %q; got %q", want, c.Detail)
		}
	}
	// The third contract names another repo; it must not be counted here.
	if strings.Contains(c.Detail, "CONTRACT-0003") {
		t.Errorf("another party's contract leaked into this repo's count: %q", c.Detail)
	}
	// The check may never leak into the gating set, whatever its verdict.
	for _, chk := range Run(dir).Checks {
		if chk.Name == "cross-repo contract obligations" && !chk.Advisory {
			t.Fatal("the contract check leaked into doctor's gating set")
		}
	}
}

// contracts.mode: off is stated, not silently reported as "nothing unmet".
func TestDoctorContractsModeOffIsStated(t *testing.T) {
	dir := t.TempDir()
	writeAt(t, dir, ".nugit/config.yml", "schema_version: 1\norg:\n  repo: consumer-gateway\ncontracts:\n  mode: off\n")
	c := contractCheck(t, Run(dir))
	if !c.OK || !strings.Contains(c.Detail, "contracts.mode: off") {
		t.Errorf("check = %+v", c)
	}
}
