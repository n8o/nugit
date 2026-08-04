package config

import (
	"path/filepath"
	"testing"
)

func peersOf(t *testing.T, yaml string) PeerList {
	t.Helper()
	c, err := LoadBytes([]byte(yaml))
	if err != nil {
		t.Fatalf("peers must never error the whole config: %v", err)
	}
	return c.Peers
}

func TestPeersParse(t *testing.T) {
	p := peersOf(t, "schema_version: 1\npeers:\n  - name: platform\n    path: ../platform-repo\n")
	if len(p) != 1 || p[0].Name != "platform" || p[0].Path != "../platform-repo" {
		t.Fatalf("peers = %+v", p)
	}
}

func TestNoPeersByDefault(t *testing.T) {
	if p := peersOf(t, "schema_version: 1\n"); len(p) != 0 {
		t.Errorf("peers must default to none, got %+v", p)
	}
	if p := Default().Peers; len(p) != 0 {
		t.Errorf("Default() must carry no peers, got %+v", p)
	}
}

// Malformed peers fail CLOSED to "no peers" and never take the rest of
// config.yml down with them — c4.mode, fail_on and the capture mode must keep
// parsing. Federation is additive context; it may not become a way to disable
// enforcement by typo.
func TestMalformedPeersFailClosedWithoutBreakingConfig(t *testing.T) {
	cases := map[string]string{
		"scalar where a list belongs":  "peers: nope\n",
		"mapping where a list belongs": "peers:\n  platform: ../platform-repo\n",
		"entry is a bare scalar":       "peers:\n  - ../platform-repo\n",
	}
	for name, block := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := LoadBytes([]byte("schema_version: 1\nc4:\n  mode: enforce\npr_render:\n  fail_on: fail\n" + block))
			if err != nil {
				t.Fatalf("a malformed peers block must not error the config: %v", err)
			}
			if len(c.Peers) != 0 {
				t.Errorf("want no peers, got %+v", c.Peers)
			}
			if c.C4.Mode != "enforce" || c.PRRender.FailOn != "fail" {
				t.Errorf("the rest of the config must be intact: mode=%q fail_on=%q", c.C4.Mode, c.PRRender.FailOn)
			}
		})
	}
}

// One bad entry never voids the good ones.
func TestOneMalformedEntryDoesNotVoidTheList(t *testing.T) {
	p := peersOf(t, "peers:\n  - ../oops\n  - name: platform\n    path: ../platform-repo\n")
	if len(p) != 1 || p[0].Name != "platform" {
		t.Fatalf("peers = %+v, want just platform", p)
	}
}

// Name shape and uniqueness are validated; an unusable entry is dropped, never
// promoted to a config error.
func TestInvalidPeerEntriesAreDropped(t *testing.T) {
	cases := map[string]string{
		"uppercase":       "  - name: Platform\n    path: ../p\n",
		"underscore":      "  - name: plat_form\n    path: ../p\n",
		"leading dash":    "  - name: -platform\n    path: ../p\n",
		"colon in name":   "  - name: \"plat:form\"\n    path: ../p\n",
		"empty name":      "  - name: \"\"\n    path: ../p\n",
		"missing name":    "  - path: ../p\n",
		"missing path":    "  - name: platform\n",
		"whitespace path": "  - name: platform\n    path: \"   \"\n",
	}
	for name, entry := range cases {
		t.Run(name, func(t *testing.T) {
			if p := peersOf(t, "peers:\n"+entry); len(p) != 0 {
				t.Errorf("want the entry dropped, got %+v", p)
			}
		})
	}
}

func TestDuplicatePeerNamesKeepTheFirst(t *testing.T) {
	p := peersOf(t, "peers:\n  - name: platform\n    path: ../a\n  - name: platform\n    path: ../b\n")
	if len(p) != 1 || p[0].Path != "../a" {
		t.Fatalf("a duplicate name must be dropped, keeping the first: %+v", p)
	}
}

func TestPeerDirResolution(t *testing.T) {
	if got := (Peer{Path: "../platform-repo"}).Dir("/repos/app"); got != filepath.Clean("/repos/platform-repo") {
		t.Errorf("relative peer dir = %q", got)
	}
	if got := (Peer{Path: "/elsewhere/platform"}).Dir("/repos/app"); got != "/elsewhere/platform" {
		t.Errorf("absolute peer dir = %q", got)
	}
}
