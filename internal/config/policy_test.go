package config

import "testing"

func TestEffectivePolicyFallsBackToNames(t *testing.T) {
	cases := []struct {
		alias string
		r     Repo
		want  string
	}{
		{"snap", Repo{Policy: "release"}, "RELEASE"}, // the recorded policy wins
		{"snap", Repo{Name: "rdsf-qualitycontrol-maven-snapshots"}, "SNAPSHOT"},
		{"rel", Repo{URL: "https://h/nexus/repository/rdsf-qualitycontrol-maven-releases/"}, "RELEASE"},
		{"x", Repo{Name: "maven-central"}, ""},
		{"both", Repo{Name: "snapshots-and-releases"}, ""}, // ambiguous: no guess
	}
	for _, c := range cases {
		if got := c.r.EffectivePolicy(c.alias); got != c.want {
			t.Errorf("%s %+v: %q want %q", c.alias, c.r, got, c.want)
		}
	}
}

func TestPromotionPairFromConventionalNames(t *testing.T) {
	c := &Config{Repos: map[string]Repo{
		"a": {Format: "maven2", Type: "hosted", Name: "rdsf-qualitycontrol-maven-snapshots"},
		"b": {Format: "maven2", Type: "hosted", Name: "rdsf-qualitycontrol-maven-releases"},
	}}
	from, to, err := c.PromotionPair()
	if err != nil || from != "a" || to != "b" {
		t.Fatalf("%q %q %v", from, to, err)
	}
}
