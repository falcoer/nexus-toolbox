package maven

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

func TestInspectSnapshotLatestBuildWithDirectLinks(t *testing.T) {
	f := snapFake(t, "ALLOW", snapPom)
	src, _ := f.targets()
	d, err := New().Inspect(context.Background(), src, module.InspectInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "snapshot" || d.Build != "20260914.091709-45" || len(d.Builds) != 3 || d.Version != "03.27.10-0-SNAPSHOT" {
		t.Fatalf("%+v", d)
	}
	if len(d.Files) != 2 || d.Files[0].Name != "ghc-web-03.27.10-0-20260914.091709-45.pom" || d.Files[1].Name != "ghc-web-03.27.10-0-20260914.091709-45.war" {
		t.Fatalf("files: %+v", d.Files)
	}
	war := d.Files[1]
	wantURL := f.srv.URL + "/repository/src/com/acme/ghc-web/03.27.10-0-SNAPSHOT/ghc-web-03.27.10-0-20260914.091709-45.war"
	if war.URL != wantURL || war.Size != int64(len("WAR-20260914.091709-45")) || war.SHA1 != sum("WAR-20260914.091709-45") {
		t.Errorf("war: %+v want url %s", war, wantURL)
	}
	if d.TotalSize != d.Files[0].Size+d.Files[1].Size || d.TotalSize == 0 {
		t.Errorf("total size from HEAD: %d", d.TotalSize)
	}
	if !strings.HasSuffix(d.DirectoryURL, "/repository/src/com/acme/ghc-web/03.27.10-0-SNAPSHOT/") ||
		!strings.HasSuffix(d.BrowseURL, "/#browse/browse:src:com%2Facme%2Fghc-web%2F03.27.10-0-SNAPSHOT") {
		t.Errorf("dir=%s browse=%s", d.DirectoryURL, d.BrowseURL)
	}
	// other build, and the explicit timestamped form
	d, err = New().Inspect(context.Background(), src, module.InspectInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT", Build: "43"})
	if err != nil || d.Build != "20260914.070210-43" {
		t.Fatalf("%+v %v", d, err)
	}
	d, err = New().Inspect(context.Background(), src, module.InspectInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-20260914.084843-44"})
	if err != nil || d.Build != "20260914.084843-44" || d.Version != "03.27.10-0-SNAPSHOT" {
		t.Fatalf("%+v %v", d, err)
	}
	if _, err := New().Inspect(context.Background(), src, module.InspectInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT", Build: "99"}); err == nil || !strings.Contains(err.Error(), "disponibles") {
		t.Errorf("unknown build: %v", err)
	}
}

func TestInspectReleaseHidesChecksumFilesUnlessAll(t *testing.T) {
	f := newFake(t, "ALLOW") // release com.acme:lib:1.0 with jar, pom, .jar.sha1, maven-metadata.xml
	src, _ := f.targets()
	in := module.InspectInput{Group: "com.acme", Artifact: "lib", Version: "1.0"}
	d, err := New().Inspect(context.Background(), src, in)
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "release" || len(d.Files) != 2 || d.Build != "" {
		t.Fatalf("%+v", d)
	}
	for _, fl := range d.Files {
		if fl.Generated || strings.HasSuffix(fl.Name, ".sha1") {
			t.Errorf("generated file listed: %+v", fl)
		}
	}
	in.All = true
	d, err = New().Inspect(context.Background(), src, in)
	if err != nil || len(d.Files) != 4 {
		t.Fatalf("--all must list checksum and metadata files: %v %+v", err, d.Files)
	}
	if _, err := New().Inspect(context.Background(), src, module.InspectInput{Group: "com.acme", Artifact: "lib", Version: "9.9"}); err == nil {
		t.Error("unknown version must fail")
	}
}

func TestVersionsGroupsSnapshotBuilds(t *testing.T) {
	f := snapFake(t, "ALLOW", snapPom)
	old := time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
	f.snap = append(f.snap, nexus.Component{ID: "rel", Group: "com.acme", Name: "ghc-web", Version: "03.27.09-0", Assets: []nexus.Asset{
		{Path: "com/acme/ghc-web/03.27.09-0/ghc-web-03.27.09-0.war", LastModified: old},
		{Path: "com/acme/ghc-web/03.27.09-0/ghc-web-03.27.09-0.war.sha1"},
	}})
	src, _ := f.targets()
	s, err := New().Versions(context.Background(), src, "com.acme", "ghc-web")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Versions) != 2 || s.Versions[0].Version != "03.27.09-0" || s.Versions[1].Version != "03.27.10-0-SNAPSHOT" {
		t.Fatalf("order (oldest first): %+v", s.Versions)
	}
	rel, snap := s.Versions[0], s.Versions[1]
	if rel.Kind != "release" || rel.Files != 1 || !rel.Modified.Equal(old) || rel.Builds != 0 {
		t.Errorf("release: %+v", rel)
	}
	if snap.Kind != "snapshot" || snap.Builds != 3 || snap.Files != 6 {
		t.Errorf("snapshot: %+v", snap)
	}
	if !strings.HasSuffix(s.DirectoryURL, "/repository/src/com/acme/ghc-web/") || s.BrowseURL == "" {
		t.Errorf("links: %s %s", s.DirectoryURL, s.BrowseURL)
	}
	if _, err := New().Versions(context.Background(), src, "com.acme", "nope"); err == nil {
		t.Error("unknown artifact must fail")
	}
}

// The search API does not list checksum files or maven-metadata: --all must find them next to the files.
func TestInspectAllProbesChecksumAndMetadataFiles(t *testing.T) {
	f := snapFake(t, "ALLOW", snapPom)
	base := "com/acme/ghc-web/03.27.10-0-SNAPSHOT/ghc-web-03.27.10-0-20260914.091709-45"
	f.src[base+".war.sha1"] = sum("WAR-20260914.091709-45")
	f.src[base+".war.md5"] = "md5"
	f.src[base+".pom.sha1"] = "x" // no .pom.md5
	f.src["com/acme/ghc-web/03.27.10-0-SNAPSHOT/maven-metadata.xml"] = "<metadata/>"
	f.src["com/acme/ghc-web/03.27.10-0-SNAPSHOT/maven-metadata.xml.sha1"] = "y"
	f.src["com/acme/ghc-web/maven-metadata.xml"] = "<artifact-level/>" // must not be reported twice
	src, _ := f.targets()
	in := module.InspectInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT"}
	d, err := New().Inspect(context.Background(), src, in)
	if err != nil || len(d.Files) != 2 {
		t.Fatalf("without --all only the real files: %v %+v", err, d.Files)
	}
	in.All = true
	d, err = New().Inspect(context.Background(), src, in)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, fl := range d.Files {
		names = append(names, fl.Name)
		if strings.HasSuffix(fl.Name, ".sha1") || strings.HasSuffix(fl.Name, ".md5") || fl.Name == "maven-metadata.xml" {
			if !fl.Generated {
				t.Errorf("%s must be flagged generated", fl.Name)
			}
		}
	}
	want := "ghc-web-03.27.10-0-20260914.091709-45.pom,ghc-web-03.27.10-0-20260914.091709-45.pom.sha1,ghc-web-03.27.10-0-20260914.091709-45.war,ghc-web-03.27.10-0-20260914.091709-45.war.md5,ghc-web-03.27.10-0-20260914.091709-45.war.sha1,maven-metadata.xml,maven-metadata.xml.sha1"
	if strings.Join(names, ",") != want {
		t.Fatalf("got  %v\nwant %s", names, want)
	}
	for _, fl := range d.Files {
		if fl.Name == "maven-metadata.xml" && !strings.HasSuffix(fl.URL, "/03.27.10-0-SNAPSHOT/maven-metadata.xml") {
			t.Errorf("version-level metadata expected: %s", fl.URL)
		}
		if fl.Size == 0 {
			t.Errorf("size from HEAD missing for %s", fl.Name)
		}
	}
}

func TestInspectAllFallsBackToArtifactLevelMetadataForReleases(t *testing.T) {
	f := newFake(t, "ALLOW") // release com.acme:lib:1.0
	f.src["com/acme/lib/maven-metadata.xml"] = "<metadata/>"
	src, _ := f.targets()
	d, err := New().Inspect(context.Background(), src, module.InspectInput{Group: "com.acme", Artifact: "lib", Version: "1.0", All: true})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, fl := range d.Files {
		if fl.Name == "maven-metadata.xml" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("exactly one maven-metadata.xml expected, got %d in %+v", n, d.Files)
	}
}
