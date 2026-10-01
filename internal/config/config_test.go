package config

import "testing"

func TestParseRepoURL(t *testing.T) {
	cases := []struct{ in, base, name string }{
		{"https://h.example/nexus/repository/snap/", "https://h.example/nexus", "snap"},
		{"https://h.example/repository/rel", "https://h.example", "rel"},
		{"https://h.example/nexus/repository/snap/com/acme/", "https://h.example/nexus", "snap"},
	}
	for _, c := range cases {
		b, n, err := ParseRepoURL(c.in)
		if err != nil || b != c.base || n != c.name {
			t.Errorf("%s → %q %q %v", c.in, b, n, err)
		}
	}
	if _, _, err := ParseRepoURL("https://h.example/nexus/"); err == nil {
		t.Error("expected error")
	}
}
