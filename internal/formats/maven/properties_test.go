package maven

import (
	"context"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

// a parent like _flux-manager-parent: SNAPSHOT version properties used by BOM imports.
const propsParentPom = "<project>\n  <artifactId>par</artifactId>\n  <version>03.27.10-0-SNAPSHOT</version>\n  <properties>\n    <qc-bom.version>4.1.0-0-SNAPSHOT</qc-bom.version>\n    <fox.version>18.0.0-0-SNAPSHOT</fox.version>\n  </properties>\n" +
	"  <dependencyManagement><dependencies>\n    <dependency><groupId>com.acme</groupId><artifactId>qc-bom</artifactId><version>${qc-bom.version}</version><type>pom</type><scope>import</scope></dependency>\n" +
	"    <dependency><groupId>com.acme</groupId><artifactId>fox</artifactId><version>${fox.version}</version></dependency>\n  </dependencies></dependencyManagement>\n</project>\n"

func propsFake(t *testing.T) *fake {
	f := newFake(t, "ALLOW")
	f.src, f.snap = map[string]string{}, nil
	f.addBuilds("par", propsParentPom, false, "20260914.060000-2")
	return f
}

func parOnly() module.PromoteInput {
	return module.PromoteInput{Group: "com.acme", Artifact: "par", Version: "03.27.10-0-SNAPSHOT", NoMarker: true}
}

func TestSnapshotPropertiesBlockUnlessFixed(t *testing.T) {
	f := propsFake(t)
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, parOnly())
	if err != nil || len(plan.BlockingRefs()) != 2 {
		t.Fatalf("%v %v", err, plan.SnapshotRefs)
	}
	// --release-properties
	i := parOnly()
	i.ReleaseProperties = true
	plan, err = New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || len(plan.BlockingRefs()) != 0 {
		t.Fatalf("%v %v", err, plan.SnapshotRefs)
	}
	got := string(plan.Items[0].Content)
	if !strings.Contains(got, "<qc-bom.version>4.1.0-0</qc-bom.version>") || !strings.Contains(got, "<fox.version>18.0.0-0</fox.version>") {
		t.Errorf("properties not stripped:\n%s", got)
	}
	// --set-property wins over the strip
	i.SetProperties = map[string]string{"fox.version": "18.0.0-3"}
	plan, _ = New().PlanPromote(context.Background(), src, dst, i)
	if !strings.Contains(string(plan.Items[0].Content), "<fox.version>18.0.0-3</fox.version>") {
		t.Errorf("explicit value must win:\n%s", plan.Items[0].Content)
	}
	if len(plan.Warnings) == 0 {
		t.Error("absent artifacts must be reported")
	}
}

func TestPropertyArtifactsMustExistInDestination(t *testing.T) {
	f := propsFake(t)
	f.dst["com/acme/qc-bom/4.1.0-0/qc-bom-4.1.0-0.pom"] = "x" // released; fox 18.0.0-0 is not
	src, dst := f.targets()
	i := parOnly()
	i.ReleaseProperties = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil {
		t.Fatal(err)
	}
	w := strings.Join(plan.Warnings, "|")
	if strings.Contains(w, "qc-bom") || !strings.Contains(w, "propriété fox.version → 18.0.0-0 : com.acme:fox:18.0.0-0 est absent de dst") {
		t.Errorf("warnings: %v", plan.Warnings)
	}
	f.dst["com/acme/fox/18.0.0-0/fox-18.0.0-0.pom"] = "x"
	plan, _ = New().PlanPromote(context.Background(), src, dst, i)
	if len(plan.Warnings) != 0 {
		t.Errorf("no warning expected once both are released: %v", plan.Warnings)
	}
}

func TestPropertyFlagsApplyToWholeChainAndWarnOnce(t *testing.T) {
	f := propsFake(t)
	f.addBuilds("ghc-web", childPom, true, "20260914.091709-45")
	src, dst := f.targets()
	i := withParent()
	i.ReleaseProperties = true
	i.SetProperties = map[string]string{"qc-bom.version": "4.1.0-9", "unknown.version": "1"}
	i.Pins = []module.Pin{{Group: "com.acme", Artifact: "par", Version: "03.27.10-0"}, {Group: "x", Artifact: "y", Version: "1"}}
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || len(plan.Parents) != 1 || len(plan.BlockingRefs()) != 0 {
		t.Fatalf("%v %+v", err, plan)
	}
	if !strings.Contains(string(plan.Parents[0].Items[0].Content), "<qc-bom.version>4.1.0-9</qc-bom.version>") {
		t.Errorf("set-property must reach the parent pom")
	}
	var ws []string
	ws = append(ws, plan.Warnings...)
	all := strings.Join(ws, "|")
	if strings.Count(all, "--pin sans effet") != 1 || !strings.Contains(all, "x:y=1") || strings.Contains(all, "par=03.27.10-0") {
		t.Errorf("pin warnings: %v", ws)
	}
	if strings.Count(all, "--set-property sans effet") != 1 || !strings.Contains(all, "unknown.version") || strings.Contains(all, "qc-bom.version\n") {
		t.Errorf("set-property warnings: %v", ws)
	}
}

func TestNoBinaryWarningForPomOnlyPlans(t *testing.T) {
	f := propsFake(t)
	src, dst := f.targets()
	i := parOnly()
	i.ReleaseProperties = true
	plan, _ := New().PlanPromote(context.Background(), src, dst, i)
	for _, w := range plan.Warnings {
		if strings.Contains(w, "binaires") {
			t.Errorf("pom-only plan must not warn about binaries: %v", plan.Warnings)
		}
	}
}
