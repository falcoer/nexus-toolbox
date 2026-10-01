package maven

import (
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

const pom = "<?xml version=\"1.0\"?>\r\n<project>\r\n  <!-- <version>9.9.9</version> -->\r\n" +
	"  <parent>\r\n    <groupId>com.acme</groupId>\r\n    <artifactId>parent</artifactId>\r\n    <version>1.0-SNAPSHOT</version>\r\n  </parent>\r\n" +
	"  <artifactId>ghc-web</artifactId>\r\n  <version>03.27.10-0-SNAPSHOT</version>\r\n  <packaging>war</packaging>\r\n" +
	"  <properties><lib.version>2.0-SNAPSHOT</lib.version><java>17</java></properties>\r\n" +
	"  <dependencies>\r\n    <dependency><groupId>com.acme</groupId><artifactId>core</artifactId><version>3.1</version></dependency>\r\n" +
	"    <dependency><groupId>com.acme</groupId><artifactId>snap</artifactId><version>4.0-SNAPSHOT</version></dependency>\r\n  </dependencies>\r\n" +
	"  <build><plugins><plugin><artifactId>p</artifactId><version>1.1</version></plugin></plugins></build>\r\n</project>\r\n"

func TestRewritePomOnlyOwnVersion(t *testing.T) {
	r, err := RewritePom([]byte(pom), "03.27.10-0", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(pom, "<version>03.27.10-0-SNAPSHOT</version>", "<version>03.27.10-0</version>", 1)
	if string(r.Out) != want {
		t.Fatalf("pom altered beyond own version:\n%s", r.Out)
	}
	if len(r.Refs) != 3 || !r.HasVersion || len(r.Diff) != 1 {
		t.Fatalf("refs=%v diff=%v", r.Refs, r.Diff)
	}
	if !strings.Contains(strings.Join(r.Refs, "|"), "parent com.acme:parent : 1.0-SNAPSHOT") {
		t.Errorf("refs: %v", r.Refs)
	}
}

func TestRewritePomPins(t *testing.T) {
	pins := []module.Pin{{Group: "com.acme", Artifact: "parent", Version: "1.0"}, {Group: "x", Artifact: "y", Version: "1"}}
	r, err := RewritePom([]byte(pom), "03.27.10-0", pins)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(r.Out), "<artifactId>parent</artifactId>\r\n    <version>1.0</version>") {
		t.Errorf("parent not pinned:\n%s", r.Out)
	}
	if len(r.Refs) != 2 || len(r.UnusedPins) != 1 || r.UnusedPins[0] != "x:y=1" {
		t.Errorf("refs=%v unused=%v", r.Refs, r.UnusedPins)
	}
}

func TestRewritePomEdgeCases(t *testing.T) {
	r, err := RewritePom([]byte("<project><parent><groupId>g</groupId><artifactId>a</artifactId><version>1</version></parent><artifactId>x</artifactId></project>"), "2", nil)
	if err != nil || r.HasVersion || len(r.Diff) != 0 {
		t.Errorf("inherited version: %+v %v", r, err)
	}
	if _, err := RewritePom([]byte("<project><version>${revision}</version></project>"), "2", nil); err == nil {
		t.Error("${revision} must be refused")
	}
	if _, err := RewritePom([]byte("<html/>"), "2", nil); err == nil {
		t.Error("non-pom must be refused")
	}
	same, _ := RewritePom([]byte("<project><version>2</version></project>"), "2", nil)
	if string(same.Out) != "<project><version>2</version></project>" || len(same.Diff) != 0 {
		t.Error("same version must be a no-op")
	}
}

func TestRewritePomProperties(t *testing.T) {
	src := "<project>\r\n <properties>\r\n  <mw.version>2.10.10-0-SNAPSHOT</mw.version>\r\n  <frm.version>2.10.9-0-SNAPSHOT</frm.version>\r\n  <java>17</java>\r\n </properties>\r\n</project>\r\n"
	r, err := RewritePomOpts([]byte(src), "", nil, PomOpts{Aligned: map[string]string{"mw.version": "2.10.10-0", "other": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(src, "<mw.version>2.10.10-0-SNAPSHOT", "<mw.version>2.10.10-0", 1)
	if string(r.Out) != want {
		t.Errorf("only the aligned property may change:\n%s", r.Out)
	}
	if len(r.Refs) != 1 || !strings.Contains(r.Refs[0], "frm.version") || len(r.Diff) != 1 || !strings.Contains(r.Diff[0], "valeur de la release existante") {
		t.Errorf("refs=%v diff=%v", r.Refs, r.Diff)
	}
	if r.Props["java"] != "17" || r.Props["frm.version"] != "2.10.9-0-SNAPSHOT" {
		t.Errorf("props=%v", r.Props)
	}
	// a SNAPSHOT value in the release is not an alignment
	r, _ = RewritePomOpts([]byte(src), "", nil, PomOpts{Aligned: map[string]string{"mw.version": "9-SNAPSHOT"}})
	if len(r.Refs) != 2 {
		t.Errorf("refs=%v", r.Refs)
	}
}

const bomPom = "<project>\r\n <properties>\r\n  <!-- libs -->\r\n  <qc-bom.version>4.1.0-0-SNAPSHOT</qc-bom.version>\r\n  <fox.version>18.0.0-0-SNAPSHOT</fox.version>\r\n  <keep.version>1.2</keep.version>\r\n </properties>\r\n" +
	" <dependencyManagement><dependencies>\r\n  <dependency><groupId>com.acme</groupId><artifactId>qc-bom</artifactId><version>${qc-bom.version}</version><type>pom</type><scope>import</scope></dependency>\r\n" +
	"  <dependency><groupId>${project.groupId}</groupId><artifactId>fox</artifactId><version>${fox.version}</version></dependency>\r\n" +
	" </dependencies></dependencyManagement>\r\n</project>\r\n"

func TestPropertiesPrecedenceAndStrip(t *testing.T) {
	// strip only
	r, err := RewritePomOpts([]byte(bomPom), "", nil, PomOpts{StripSnapshot: true})
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(strings.Replace(bomPom, "4.1.0-0-SNAPSHOT", "4.1.0-0", 1), "18.0.0-0-SNAPSHOT", "18.0.0-0", 1)
	if string(r.Out) != want || len(r.Refs) != 0 || len(r.Changed) != 2 {
		t.Fatalf("strip: refs=%v changed=%v\n%s", r.Refs, r.Changed, r.Out)
	}
	// precedence: set > aligned > strip
	r, _ = RewritePomOpts([]byte(bomPom), "", nil, PomOpts{
		Set:           map[string]string{"qc-bom.version": "4.1.0-9", "keep.version": "1.3", "nope": "1"},
		Aligned:       map[string]string{"qc-bom.version": "4.1.0-5", "fox.version": "18.0.0-7"},
		StripSnapshot: true})
	if r.Changed["qc-bom.version"] != "4.1.0-9" || r.Changed["fox.version"] != "18.0.0-7" || r.Changed["keep.version"] != "1.3" {
		t.Errorf("changed=%v", r.Changed)
	}
	if len(r.SetUsed) != 2 {
		t.Errorf("setUsed=%v (unknown names must not be reported as used)", r.SetUsed)
	}
	if !strings.Contains(string(r.Out), "<!-- libs -->") || !strings.Contains(string(r.Out), "\r\n  <keep.version>1.3</keep.version>") {
		t.Errorf("layout altered:\n%s", r.Out)
	}
	// users of properties
	u := r.PropUsers["qc-bom.version"]
	if len(u) != 1 || u[0].Group != "com.acme" || u[0].Artifact != "qc-bom" {
		t.Errorf("users: %v", r.PropUsers)
	}
	if f := r.PropUsers["fox.version"]; len(f) != 1 || f[0].Group != "${project.groupId}" {
		t.Errorf("fox users: %v", f)
	}
	// setting a SNAPSHOT value keeps it blocking
	r, _ = RewritePomOpts([]byte(bomPom), "", nil, PomOpts{Set: map[string]string{"fox.version": "9-SNAPSHOT"}, StripSnapshot: true})
	if len(r.Refs) != 1 || !strings.Contains(r.Refs[0], "fox.version = 9-SNAPSHOT") {
		t.Errorf("refs=%v", r.Refs)
	}
}
