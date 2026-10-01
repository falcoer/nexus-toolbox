package maven

import (
	"context"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

func TestSplitSnapshotVersion(t *testing.T) {
	b, ts, n, ok := SplitSnapshotVersion("03.27.10-0-20260914.070210-43")
	if !ok || b != "03.27.10-0" || ts != "20260914.070210" || n != 43 {
		t.Errorf("%q %q %d %v", b, ts, n, ok)
	}
	if _, _, _, ok := SplitSnapshotVersion("03.27.10-0-SNAPSHOT"); ok {
		t.Error("SNAPSHOT is not timestamped")
	}
}

const snapPom = "<project>\n  <groupId>com.acme</groupId>\n  <artifactId>ghc-web</artifactId>\n  <version>03.27.10-0-SNAPSHOT</version>\n  <packaging>war</packaging>\n</project>\n"

// snapshot fixture modelled on the Nexus UI: three builds, each with .pom and .war.
func snapFake(t *testing.T, policy string, pom string) *fake {
	f := newFake(t, policy)
	f.src = map[string]string{}
	base := "com/acme/ghc-web/03.27.10-0-SNAPSHOT/ghc-web-03.27.10-0-"
	for _, b := range []string{"20260914.070210-43", "20260914.084843-44", "20260914.091709-45"} {
		f.src[base+b+".pom"] = pom
		f.src[base+b+".war"] = "WAR-" + b
		var assets []nexus.Asset
		for _, ext := range []string{".pom", ".war"} {
			c := f.src[base+b+ext]
			assets = append(assets, nexus.Asset{Path: base + b + ext, DownloadURL: f.srv.URL + "/repository/src/" + base + b + ext,
				FileSize: int64(len(c)), Checksum: map[string]string{"sha1": sum(c)}})
		}
		f.snap = append(f.snap, nexus.Component{ID: "id-" + b, Group: "com.acme", Name: "ghc-web", Version: "03.27.10-0-" + b, Assets: assets})
	}
	return f
}

var snapIn = module.PromoteInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT"}

func TestPromoteSnapshotLatestBuild(t *testing.T) {
	f := snapFake(t, "ALLOW_ONCE", snapPom)
	src, dst := f.targets()
	i := snapIn
	i.Tool, i.DeleteSource = "nexus-toolbox test", true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceBuild != "20260914.091709-45" || plan.TargetVersion != "03.27.10-0" || len(plan.Builds) != 3 || plan.SourceID != "id-20260914.091709-45" {
		t.Fatalf("%+v", plan)
	}
	paths := []string{}
	for _, it := range plan.Items {
		paths = append(paths, it.Path)
	}
	want := "com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.war,com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.pom,com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0-promoted-from.txt"
	if strings.Join(paths, ",") != want {
		t.Fatalf("paths %v", paths)
	}
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.Copied != 2 || !res.MarkerWritten || !res.SourceDeleted {
		t.Fatalf("%+v %v", res, err)
	}
	d := "com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0"
	if f.dst[d+".war"] != "WAR-20260914.091709-45" {
		t.Errorf("war must be byte identical: %q", f.dst[d+".war"])
	}
	if !strings.Contains(f.dst[d+".pom"], "<version>03.27.10-0</version>") || strings.Contains(f.dst[d+".pom"], "SNAPSHOT") {
		t.Errorf("pom not rewritten: %q", f.dst[d+".pom"])
	}
	mk := f.dst[d+"-promoted-from.txt"]
	for _, s := range []string{"source-version: 03.27.10-0-SNAPSHOT", "source-build: 20260914.091709-45", "target-version: 03.27.10-0", "promoted-by: u", "binaire inchangé", "nexus-toolbox test"} {
		if !strings.Contains(mk, s) {
			t.Errorf("marker lacks %q:\n%s", s, mk)
		}
	}
	for p := range f.dst {
		if strings.HasSuffix(p, ".sha1") || strings.HasSuffix(p, ".md5") {
			t.Errorf("checksum file uploaded: %s", p)
		}
	}
	if len(f.dst) != 3 || f.del[0] != "id-20260914.091709-45" {
		t.Errorf("dst=%v del=%v", f.dst, f.del)
	}
}

func TestPromoteSnapshotBuildChoiceAndAsVersion(t *testing.T) {
	f := snapFake(t, "ALLOW", snapPom)
	src, dst := f.targets()
	i := snapIn
	i.Build, i.AsVersion, i.NoMarker = "43", "03.27.10-1", true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || plan.SourceBuild != "20260914.070210-43" || plan.TargetVersion != "03.27.10-1" {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.dst["com/acme/ghc-web/03.27.10-1/ghc-web-03.27.10-1.pom"], "<version>03.27.10-1</version>") || len(f.dst) != 2 {
		t.Errorf("%v", f.dst)
	}
	// explicit timestamped version
	e := snapIn
	e.Version = "03.27.10-0-20260914.084843-44"
	e.NoMarker = true
	plan, err = New().PlanPromote(context.Background(), src, dst, e)
	if err != nil || plan.SourceBuild != "20260914.084843-44" || plan.TargetVersion != "03.27.10-0" {
		t.Fatalf("%+v %v", plan, err)
	}
	e.Build = "45"
	if _, err := New().PlanPromote(context.Background(), src, dst, e); err == nil {
		t.Error("--build contradicting the version must fail")
	}
	e.Version, e.Build = "03.27.10-0-SNAPSHOT", "99"
	if _, err := New().PlanPromote(context.Background(), src, dst, e); err == nil || !strings.Contains(err.Error(), "disponibles") {
		t.Errorf("unknown build: %v", err)
	}
	a := snapIn
	a.AsVersion = "1.0-SNAPSHOT"
	if _, err := New().PlanPromote(context.Background(), src, dst, a); err == nil {
		t.Error("snapshot target must be refused")
	}
}

const snapRefPom = "<project>\n  <parent><groupId>com.acme</groupId><artifactId>par</artifactId><version>1.0-SNAPSHOT</version></parent>\n" +
	"  <artifactId>ghc-web</artifactId>\n  <version>03.27.10-0-SNAPSHOT</version>\n</project>\n"

func TestPromoteSnapshotRefs(t *testing.T) {
	f := snapFake(t, "ALLOW", snapRefPom)
	src, dst := f.targets()
	i := snapIn
	i.NoMarker = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || len(plan.SnapshotRefs) != 1 {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err == nil || len(f.dst) != 0 {
		t.Fatalf("must block, wrote %v (%v)", f.dst, err)
	}
	i.Pins = []module.Pin{{Group: "com.acme", Artifact: "par", Version: "1.0"}}
	plan, _ = New().PlanPromote(context.Background(), src, dst, i)
	if len(plan.SnapshotRefs) != 0 {
		t.Fatalf("pin should resolve: %v", plan.SnapshotRefs)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.dst["com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.pom"], "<version>1.0</version>") {
		t.Error("parent not pinned")
	}
	// --allow-snapshot-refs
	f2 := snapFake(t, "ALLOW", snapRefPom)
	s2, d2 := f2.targets()
	i2 := snapIn
	i2.NoMarker, i2.AllowSnapshotRefs = true, true
	plan, _ = New().PlanPromote(context.Background(), s2, d2, i2)
	if _, err := New().ExecutePromote(context.Background(), s2, d2, plan, noRep{}); err != nil {
		t.Fatalf("allowed refs: %v", err)
	}
}

func TestPromoteMarkerResumeAndConflict(t *testing.T) {
	f := snapFake(t, "ALLOW_ONCE", snapPom)
	src, dst := f.targets()
	plan, _ := New().PlanPromote(context.Background(), src, dst, snapIn)
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	mk := "com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0-promoted-from.txt"
	first := f.dst[mk]
	// second run: everything identical → nothing rewritten, marker kept
	plan, _ = New().PlanPromote(context.Background(), src, dst, snapIn)
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.Copied != 0 || res.Skipped != 2 || res.MarkerWritten || f.dst[mk] != first {
		t.Fatalf("%+v %v", res, err)
	}
	// different build already promoted under the same target → conflict
	i := snapIn
	i.Build = "43"
	plan, _ = New().PlanPromote(context.Background(), src, dst, i)
	if len(plan.Blocking()) != 1 || !strings.HasSuffix(plan.Blocking()[0].Path, ".war") {
		t.Fatalf("expected a war conflict: %+v", plan.Items)
	}
}

func TestMetadataWarning(t *testing.T) {
	f := snapFake(t, "ALLOW", snapPom)
	src, dst := f.targets()
	i := snapIn
	i.NoMarker = true
	plan, _ := New().PlanPromote(context.Background(), src, dst, i)
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.MetadataOK || len(res.Warnings) != 1 {
		t.Fatalf("fake serves no maven-metadata.xml: %+v %v", res, err)
	}
}
