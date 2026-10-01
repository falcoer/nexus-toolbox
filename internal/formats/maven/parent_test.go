package maven

import (
	"context"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

func pomWithParent(artifact, parent string) string {
	p := ""
	if parent != "" {
		p = "<parent><groupId>com.acme</groupId><artifactId>" + parent + "</artifactId><version>03.27.10-0-SNAPSHOT</version></parent>"
	}
	return "<project>" + p + "<groupId>com.acme</groupId><artifactId>" + artifact + "</artifactId><version>03.27.10-0-SNAPSHOT</version></project>"
}

// childPom inherits its version from the parent, like ghc-web.
const childPom = "<project><parent><groupId>com.acme</groupId><artifactId>par</artifactId><version>03.27.10-0-SNAPSHOT</version></parent><artifactId>ghc-web</artifactId></project>"

func parentFake(t *testing.T) *fake {
	f := newFake(t, "ALLOW")
	f.src = map[string]string{}
	f.addBuilds("ghc-web", childPom, true, "20260914.070210-43", "20260914.091709-45")
	f.addBuilds("par", pomWithParent("par", ""), false, "20260914.050000-1", "20260914.060000-2")
	return f
}

func withParent() module.PromoteInput {
	i := snapIn
	i.WithParent, i.NoMarker = true, true
	return i
}

func TestWithoutParentFlagBlocksOnSnapshotParent(t *testing.T) {
	f := parentFake(t)
	src, dst := f.targets()
	i := snapIn
	i.NoMarker = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || len(plan.Parents) != 0 || len(plan.BlockingRefs()) != 1 {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err == nil || len(f.puts) != 0 {
		t.Fatalf("must block without writing: %v %v", err, f.puts)
	}
}

func TestWithParentPromotesParentFirst(t *testing.T) {
	f := parentFake(t)
	src, dst := f.targets()
	i := withParent()
	i.DeleteSource = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Parents) != 1 || plan.Parents[0].Artifact != "par" || plan.Parents[0].TargetVersion != "03.27.10-0" ||
		plan.Parents[0].SourceBuild != "20260914.060000-2" || len(plan.BlockingRefs()) != 0 {
		t.Fatalf("%+v", plan.Parents)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	if len(f.puts) != 3 || !strings.HasPrefix(f.puts[0], "com/acme/par/") {
		t.Fatalf("parent must be uploaded first: %v", f.puts)
	}
	if !strings.Contains(f.dst["com/acme/par/03.27.10-0/par-03.27.10-0.pom"], "<version>03.27.10-0</version>") {
		t.Errorf("parent pom: %q", f.dst["com/acme/par/03.27.10-0/par-03.27.10-0.pom"])
	}
	child := f.dst["com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.pom"]
	if !strings.Contains(child, "<version>03.27.10-0</version></parent>") || strings.Contains(child, "SNAPSHOT") {
		t.Errorf("child parent ref not pinned: %q", child)
	}
	if len(f.del) != 1 || f.del[0] != "id-ghc-web-20260914.091709-45" {
		t.Errorf("--delete-source must never touch parents: %v", f.del)
	}
}

func TestWithParentChainOfTwoAndCycle(t *testing.T) {
	f := parentFake(t)
	f.addBuilds("root", pomWithParent("root", ""), false, "20260914.010000-1")
	f.src = map[string]string{}
	f.snap = nil
	f.addBuilds("ghc-web", childPom, true, "20260914.091709-45")
	f.addBuilds("par", pomWithParent("par", "root"), false, "20260914.060000-2")
	f.addBuilds("root", pomWithParent("root", ""), false, "20260914.010000-1")
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, withParent())
	if err != nil || len(plan.Parents) != 2 || plan.Parents[0].Artifact != "root" || plan.Parents[1].Artifact != "par" {
		t.Fatalf("want [root par]: %+v %v", plan, err)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.puts[0], "com/acme/root/") || !strings.HasPrefix(f.puts[1], "com/acme/par/") {
		t.Errorf("order: %v", f.puts)
	}
	// cycle: root → par
	f2 := parentFake(t)
	f2.src, f2.snap = map[string]string{}, nil
	f2.addBuilds("ghc-web", childPom, true, "20260914.091709-45")
	f2.addBuilds("par", pomWithParent("par", "root"), false, "20260914.060000-2")
	f2.addBuilds("root", pomWithParent("root", "par"), false, "20260914.010000-1")
	s2, d2 := f2.targets()
	if _, err := New().PlanPromote(context.Background(), s2, d2, withParent()); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle expected: %v", err)
	}
}

func TestWithParentExplicitPinWinsAndMissingParent(t *testing.T) {
	f := parentFake(t)
	src, dst := f.targets()
	i := withParent()
	i.Pins = []module.Pin{{Group: "com.acme", Artifact: "par", Version: "03.27.10-9"}}
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || plan.Parents[0].TargetVersion != "03.27.10-9" {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	if f.dst["com/acme/par/03.27.10-9/par-03.27.10-9.pom"] == "" ||
		!strings.Contains(f.dst["com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.pom"], "<version>03.27.10-9</version></parent>") {
		t.Errorf("pin must drive the parent version: %v", f.puts)
	}
	// parent absent from the source
	f2 := parentFake(t)
	f2.snap = f2.snap[:2] // keep only ghc-web builds
	s2, d2 := f2.targets()
	if _, err := New().PlanPromote(context.Background(), s2, d2, withParent()); err == nil || !strings.Contains(err.Error(), "promouvez-le séparément") {
		t.Fatalf("clear error expected: %v", err)
	}
}

func TestParentFailureNeverPromotesChild(t *testing.T) {
	f := parentFake(t)
	f.failOn = "com/acme/par/"
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, withParent())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err == nil {
		t.Fatal("expected failure")
	}
	for p := range f.dst {
		if strings.Contains(p, "ghc-web") {
			t.Errorf("child written although its parent failed: %s", p)
		}
	}
}

func TestMissingReleaseParentWarns(t *testing.T) {
	f := snapFake(t, "ALLOW", "<project><parent><groupId>g</groupId><artifactId>rp</artifactId><version>1.0</version></parent><artifactId>ghc-web</artifactId></project>")
	src, dst := f.targets()
	i := snapIn
	i.NoMarker = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || !strings.Contains(strings.Join(plan.Warnings, "|"), "g:rp:1.0 est absent") {
		t.Fatalf("%v %v", plan.Warnings, err)
	}
	f.dst["g/rp/1.0/rp-1.0.pom"] = "x"
	plan, _ = New().PlanPromote(context.Background(), src, dst, i)
	if strings.Contains(strings.Join(plan.Warnings, "|"), "absent") {
		t.Errorf("parent present: no warning expected: %v", plan.Warnings)
	}
}

func TestConflictDiagnosisAndSizeFromHead(t *testing.T) {
	f := snapFake(t, "ALLOW_ONCE", snapPom)
	f.dst["com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.war"] = "WAR-20260914.084843-44" // an older build already promoted
	src, dst := f.targets()
	i := snapIn
	i.NoMarker = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil {
		t.Fatal(err)
	}
	var war module.PlanItem
	for _, it := range plan.Items {
		if strings.HasSuffix(it.Path, ".war") {
			war = it
		}
	}
	if war.Action != "conflict" || war.MatchBuild != "20260914.084843-44" || war.RemoteSHA1 != sum("WAR-20260914.084843-44") ||
		!strings.Contains(war.Reason, "identique au build 20260914.084843-44") {
		t.Fatalf("%+v", war)
	}
	if war.Size != int64(len("WAR-20260914.091709-45")) || plan.WritePolicy != "ALLOW_ONCE" {
		t.Errorf("size from HEAD = %d, policy %q", war.Size, plan.WritePolicy)
	}
}
