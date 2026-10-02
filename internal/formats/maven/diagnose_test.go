package maven

import (
	"context"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

const parentSnapPom = "<project>\n  <artifactId>par</artifactId>\n  <version>03.27.10-0-SNAPSHOT</version>\n  <properties>\n    <mw.version>2.10.10-0-SNAPSHOT</mw.version>\n    <frm.version>2.10.9-0-SNAPSHOT</frm.version>\n  </properties>\n</project>\n"

// the pom as CI released it: version fixed, properties fixed.
const parentReleasedPom = "<project>\n  <artifactId>par</artifactId>\n  <version>03.27.10-0</version>\n  <properties>\n    <mw.version>2.10.10-0</mw.version>\n    <frm.version>2.10.9-0</frm.version>\n  </properties>\n</project>\n"

func existingReleaseFake(t *testing.T) *fake {
	f := newFake(t, "ALLOW_ONCE")
	f.src, f.snap = map[string]string{}, nil
	f.addBuilds("par", parentSnapPom, false, "20260914.060000-2")
	f.dst["com/acme/par/03.27.10-0/par-03.27.10-0.pom"] = parentReleasedPom
	return f
}

func parIn() module.PromoteInput {
	return module.PromoteInput{Group: "com.acme", Artifact: "par", Version: "03.27.10-0-SNAPSHOT", NoMarker: true}
}

func TestConflictDiagnosticsOnPom(t *testing.T) {
	f := existingReleaseFake(t)
	src, dst := f.targets()
	i := parIn()
	i.AllowSnapshotRefs = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil {
		t.Fatal(err)
	}
	it := plan.Items[0]
	diff := strings.Join(it.DiffLines, "|")
	if it.Action != "conflict" || !strings.Contains(diff, "- <mw.version>2.10.10-0</mw.version>") ||
		!strings.Contains(diff, "+ <mw.version>2.10.10-0-SNAPSHOT</mw.version>") {
		t.Fatalf("%+v", it)
	}
	if it.RemoteSize != int64(len(parentReleasedPom)) || it.RemoteModified.IsZero() {
		t.Errorf("remote facts: size=%d mod=%v", it.RemoteSize, it.RemoteModified)
	}
	if len(plan.Notes) != 1 || !strings.Contains(plan.Notes[0], "pas été produite par nexus") {
		t.Errorf("notes: %v", plan.Notes)
	}
}

func TestExistingReleasePromotedByNexusIsReported(t *testing.T) {
	f := existingReleaseFake(t)
	f.dst["com/acme/par/03.27.10-0/par-03.27.10-0-promoted-from-03.27.10-0-20260914.050000-1.txt"] = "source-version: 03.27.10-0-SNAPSHOT\nsource-build: 20260914.050000-1\npromoted-at: 2026-09-14T14:00:00Z\nsecret: no\n"
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, parIn())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Notes) != 1 || !strings.Contains(plan.Notes[0], "déjà promue par nexus") || !strings.Contains(plan.Notes[0], "source-build: 20260914.050000-1") ||
		strings.Contains(plan.Notes[0], "secret") {
		t.Errorf("notes: %v", plan.Notes)
	}
}

func TestAlignPropertiesWithExistingRelease(t *testing.T) {
	f := existingReleaseFake(t)
	src, dst := f.targets()
	i := parIn()
	i.AlignProperties = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil {
		t.Fatal(err)
	}
	it := plan.Items[0]
	if len(plan.SnapshotRefs) != 0 || it.Action != "skip" || len(plan.Notes) != 0 {
		t.Fatalf("aligned pom must equal the release: action=%s refs=%v diff=%v\n%s", it.Action, plan.SnapshotRefs, it.Diff, it.Content)
	}
	// without --align-properties the SNAPSHOT properties block
	plan, _ = New().PlanPromote(context.Background(), src, dst, parIn())
	if len(plan.BlockingRefs()) != 2 {
		t.Errorf("refs: %v", plan.SnapshotRefs)
	}
	// a property missing from the release stays blocking
	f.dst["com/acme/par/03.27.10-0/par-03.27.10-0.pom"] = strings.Replace(parentReleasedPom, "    <frm.version>2.10.9-0</frm.version>\n", "", 1)
	i.AllowSnapshotRefs = false
	plan, _ = New().PlanPromote(context.Background(), src, dst, i)
	if len(plan.SnapshotRefs) != 1 || !strings.Contains(plan.SnapshotRefs[0], "frm.version") || plan.Items[0].Action != "conflict" {
		t.Errorf("refs=%v action=%s", plan.SnapshotRefs, plan.Items[0].Action)
	}
}

func TestAlignPropertiesAppliesToParentsAndIsWritten(t *testing.T) {
	f := existingReleaseFake(t)
	delete(f.dst, "com/acme/par/03.27.10-0/par-03.27.10-0.pom")
	f.dst["com/acme/par/03.27.10-0/par-03.27.10-0.pom"] = parentReleasedPom
	// child inherits from par; par is already released identically once aligned
	f.addBuilds("ghc-web", childPom, true, "20260914.091709-45")
	src, dst := f.targets()
	i := withParent()
	i.AlignProperties = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || len(plan.Parents) != 0 || len(plan.BlockingRefs()) != 0 || len(plan.Blockers) != 0 {
		t.Fatalf("%v %+v", err, plan)
	}
	if len(plan.Modules) != 1 || plan.Modules[0].Artifact != "par" || plan.Modules[0].Status != "released" {
		t.Errorf("the parent already released at the target version needs no promotion: %+v", plan.Modules)
	}
}
