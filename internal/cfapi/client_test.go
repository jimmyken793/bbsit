package cfapi

import "testing"

func TestFindZoneForHostname(t *testing.T) {
	zones := []Zone{
		{ID: "z1", Name: "example.com"},
		{ID: "z2", Name: "jomican.com"},
		{ID: "z3", Name: "internal.example.com"}, // longer-suffix override
	}

	cases := []struct {
		host, wantID string
	}{
		// exact zone match
		{"example.com", "z1"},
		// subdomain → owning zone
		{"app.example.com", "z1"},
		// deepest suffix wins (internal.example.com is longer than example.com)
		{"foo.internal.example.com", "z3"},
		{"internal.example.com", "z3"},
		// different zone
		{"marketing.jomican.com", "z2"},
		// case-insensitive
		{"APP.Example.COM", "z1"},
		// trailing dot tolerated
		{"app.example.com.", "z1"},
		// no match
		{"unrelated.org", ""},
		{"example.org", ""},
		// must NOT match a zone whose name is a substring but not a domain suffix
		// (e.g. zone "example.com" must not match "notexample.com")
		{"notexample.com", ""},
	}

	for _, c := range cases {
		got := FindZoneForHostname(zones, c.host)
		var gotID string
		if got != nil {
			gotID = got.ID
		}
		if gotID != c.wantID {
			t.Errorf("FindZoneForHostname(%q) = %q, want %q", c.host, gotID, c.wantID)
		}
	}
}

func TestFindZoneForHostname_EmptyZones(t *testing.T) {
	if got := FindZoneForHostname(nil, "x.example.com"); got != nil {
		t.Errorf("expected nil for empty zones, got %+v", got)
	}
}
