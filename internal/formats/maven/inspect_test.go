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
