package config

import (
	"strings"
	"testing"
)

func repo(policy string) Repo { return Repo{Format: "maven2", Type: "hosted", Policy: policy} }

func TestPromotionPairInferredFromPolicies(t *testing.T) {
	c := &Config{Repos: map[string]Repo{"snap": repo("SNAPSHOT"), "rel": repo("RELEASE"), "docker": {Format: "docker", Type: "hosted"}, "proxy": {Format: "maven2", Type: "proxy", Policy: "RELEASE"}}}
	from, to, err := c.PromotionPair()
	if err != nil || from != "snap" || to != "rel" {
		t.Fatalf("%q %q %v", from, to, err)
	}
}

func TestPromotionPairAmbiguousOrSaved(t *testing.T) {
	c := &Config{Repos: map[string]Repo{"s1": repo("SNAPSHOT"), "s2": repo("SNAPSHOT"), "rel": repo("RELEASE")}}
	if _, _, err := c.PromotionPair(); err == nil || !strings.Contains(err.Error(), "s1, s2") || !strings.Contains(err.Error(), "repos link") {
		t.Fatalf("ambiguity must list the candidates: %v", err)
	}
	c.Promotion = Promotion{From: "s2", To: "rel"}
	if from, to, err := c.PromotionPair(); err != nil || from != "s2" || to != "rel" {
		t.Fatalf("saved pair wins: %q %q %v", from, to, err)
	}
	c.Promotion.To = "gone"
	if _, _, err := c.PromotionPair(); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("stale pair: %v", err)
	}
	if _, _, err := (&Config{Repos: map[string]Repo{}}).PromotionPair(); err == nil {
		t.Fatal("no repository must fail")
	}
}
