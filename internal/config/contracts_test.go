package config

import "testing"

// The identity knob is inert-by-default and fails closed to inert, never to a
// guess: binding this repo to the WRONG party's obligations is strictly worse
// than checking nothing (ADR-0033 point 3).
func TestOrgRepoIdentity(t *testing.T) {
	cases := []struct {
		yaml string
		want string
	}{
		{"schema_version: 1\n", ""},
		{"schema_version: 1\norg:\n  repo: consumer-gateway\n", "consumer-gateway"},
		{"schema_version: 1\norg:\n  repo: '  consumer-gateway  '\n", "consumer-gateway"},
		{"schema_version: 1\norg:\n  repo: myorg/consumer-gateway\n", "myorg/consumer-gateway"},
		{"schema_version: 1\norg:\n  repo: Consumer Gateway\n", ""}, // spaces: two repos cannot reliably agree
		{"schema_version: 1\norg:\n  repo: '-leading-dash'\n", ""},  // malformed ⇒ inert
		{"schema_version: 1\norg:\n  repo: ''\n", ""},               // explicit empty ⇒ inert
	}
	for _, tc := range cases {
		c, err := LoadBytes([]byte(tc.yaml))
		if err != nil {
			t.Fatalf("%q: %v", tc.yaml, err)
		}
		if c.Org.Repo != tc.want {
			t.Errorf("%q: org.repo = %q, want %q", tc.yaml, c.Org.Repo, tc.want)
		}
	}
}

// contracts.mode falls back to the DEFAULT (warn) on an unknown value — never
// to fail. A typo must not hand another repo the power to redden this build.
func TestContractsModeFailsClosedToWarn(t *testing.T) {
	cases := []struct {
		yaml     string
		wantMode string
		wantFail bool
	}{
		{"schema_version: 1\n", "warn", false},
		{"schema_version: 1\ncontracts:\n  mode: warn\n", "warn", false},
		{"schema_version: 1\ncontracts:\n  mode: FAIL\n", "fail", true},
		{"schema_version: 1\ncontracts:\n  mode: off\n", "off", false},
		{"schema_version: 1\ncontracts:\n  mode: strict\n", "warn", false},
		{"schema_version: 1\ncontracts:\n  mode: ''\n", "warn", false},
	}
	for _, tc := range cases {
		c, err := LoadBytes([]byte(tc.yaml))
		if err != nil {
			t.Fatalf("%q: %v", tc.yaml, err)
		}
		if c.Contracts.Mode != tc.wantMode || c.ContractsFail() != tc.wantFail {
			t.Errorf("%q: mode = %q (fail=%v), want %q (fail=%v)",
				tc.yaml, c.Contracts.Mode, c.ContractsFail(), tc.wantMode, tc.wantFail)
		}
	}
}

// ContractsOn needs BOTH an identity and a mode that isn't off. Either missing
// means the whole feature is inert — no peer store is even read.
func TestContractsOnNeedsIdentityAndMode(t *testing.T) {
	cases := []struct {
		yaml string
		want bool
	}{
		{"schema_version: 1\n", false},
		{"schema_version: 1\norg:\n  repo: gw\n", true},
		{"schema_version: 1\norg:\n  repo: gw\ncontracts:\n  mode: off\n", false},
		{"schema_version: 1\ncontracts:\n  mode: fail\n", false}, // fail without identity is still inert
	}
	for _, tc := range cases {
		c, err := LoadBytes([]byte(tc.yaml))
		if err != nil {
			t.Fatalf("%q: %v", tc.yaml, err)
		}
		if c.ContractsOn() != tc.want {
			t.Errorf("%q: ContractsOn() = %v, want %v", tc.yaml, c.ContractsOn(), tc.want)
		}
	}
}
