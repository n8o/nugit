package config

import "testing"

func TestHubMarksTheNamedPeer(t *testing.T) {
	c, err := LoadBytes([]byte("schema_version: 1\norg:\n  repo: member-repo\n  hub: platform\n" +
		"peers:\n  - name: platform\n    path: ../platform\n  - name: gateway\n    path: ../gateway\n"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Org.Hub != "platform" {
		t.Fatalf("Org.Hub = %q", c.Org.Hub)
	}
	hub, ok := c.HubPeer()
	if !ok || hub.Name != "platform" || !hub.Hub {
		t.Fatalf("HubPeer = %+v ok=%v", hub, ok)
	}
	for _, p := range c.Peers {
		if p.Name == "gateway" && p.Hub {
			t.Error("a non-hub peer was marked as the hub")
		}
	}
}

// A hub naming no configured peer is KEPT, not blanked: doctor's job is to say
// "org.hub names X, which is not in peers:", which it cannot do if Load erases
// the distinction between that and "no hub at all" (ADR-0035 point 3).
func TestHubNamingAnUnconfiguredPeerIsVisibleButUnresolved(t *testing.T) {
	c, _ := LoadBytes([]byte("schema_version: 1\norg:\n  hub: ghost\npeers:\n  - name: platform\n    path: ../p\n"))
	if c.Org.Hub != "ghost" {
		t.Errorf("Org.Hub = %q, want the declared-but-unresolved name preserved", c.Org.Hub)
	}
	if _, ok := c.HubPeer(); ok {
		t.Error("HubPeer resolved a name that matches no peer")
	}
	for _, p := range c.Peers {
		if p.Hub {
			t.Errorf("peer %q marked as hub", p.Name)
		}
	}
}

// A peer entry must not be able to declare itself canonical: the designation
// lives in the reader's config, in one place (ADR-0035 point 1).
func TestAPeerCannotDeclareItselfTheHub(t *testing.T) {
	c, _ := LoadBytes([]byte("schema_version: 1\npeers:\n  - name: platform\n    path: ../p\n    hub: true\n"))
	if c.Org.Hub != "" {
		t.Errorf("Org.Hub = %q from a peer entry", c.Org.Hub)
	}
	for _, p := range c.Peers {
		if p.Hub {
			t.Fatalf("peer %q declared ITSELF the org hub — the designation must come from org.hub only", p.Name)
		}
	}
}

func TestMalformedHubNameIsInert(t *testing.T) {
	c, _ := LoadBytes([]byte("schema_version: 1\norg:\n  hub: \"Not A Name!\"\npeers:\n  - name: p\n    path: ../p\n"))
	if c.Org.Hub != "" {
		t.Errorf("Org.Hub = %q, want \"\" for a name outside the peer grammar", c.Org.Hub)
	}
}

func TestNoHubIsTheDefault(t *testing.T) {
	c := Default()
	if c.Org.Hub != "" {
		t.Errorf("Default has a hub: %q", c.Org.Hub)
	}
	if _, ok := c.HubPeer(); ok {
		t.Error("Default resolved a hub peer")
	}
}
