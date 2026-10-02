package maven

import (
	"context"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

// addVersionBuilds registers snapshot builds of artifact at an arbitrary base version
// (addBuilds is fixed to 03.27.10-0).
func (f *fake) addVersionBuilds(artifact, base, pom string, builds ...string) {
	dir := "com/acme/" + artifact + "/" + base + "-SNAPSHOT/" + artifact + "-" + base + "-"
	for _, b := range builds {
		p := dir + b + ".pom"
		f.src[p] = pom
		f.snap = append(f.snap, nexus.Component{ID: "id-" + artifact + "-" + b, Group: "com.acme", Name: artifact, Version: base + "-" + b,
			Assets: []nexus.Asset{{Path: p, DownloadURL: f.srv.URL + "/repository/src/" + p, Checksum: map[string]string{"sha1": sum(pom)}}}})
	}
}

// A reactor like _flux-manager-parent: the parent shares the root's version and carries SNAPSHOT
// properties designating libraries (one of them used by two artifacts) plus one unused property.
const reactorParentPom = "<project>\n  <artifactId>par</artifactId>\n  <version>03.27.10-0-SNAPSHOT</version>\n  <properties>\n" +
	"    <fox.version>18.0.0-0-SNAPSHOT</fox.version>\n    <swing.version>18.0.0-0-SNAPSHOT</swing.version>\n    <lonely.version>1.0-SNAPSHOT</lonely.version>\n  </properties>\n" +
	"  <dependencyManagement><dependencies>\n" +
	"    <dependency><groupId>com.acme</groupId><artifactId>fox</artifactId><version>${fox.version}</version></dependency>\n" +
	"    <dependency><groupId>com.acme</groupId><artifactId>swing-a</artifactId><version>${swing.version}</version></dependency>\n" +
	"    <dependency><groupId>${project.groupId}</groupId><artifactId>swing-b</artifactId><version>${swing.version}</version></dependency>\n" +
	"  </dependencies></dependencyManagement>\n</project>\n"

const libPom = "<project><artifactId>lib</artifactId><version>18.0.0-0-SNAPSHOT</version></project>"

func reactorFake(t *testing.T) *fake {
	noWait(t)
	f := newFake(t, "ALLOW")
	f.src, f.snap = map[string]string{}, nil
	f.addBuilds("ghc-web", childPom, true, "20260914.091709-45")
	f.addBuilds("par", reactorParentPom, false, "20260914.060000-2")
	for _, a := range []string{"fox", "swing-a", "swing-b"} {
		f.addVersionBuilds(a, "18.0.0-0", libPom, "20260914.010000-1")
	}
	return f
}

func rootIn() module.PromoteInput {
	return module.PromoteInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT", WithParent: true, NoMarker: true}
}

func statusOf(plan *module.PromotePlan, artifact string) string {
	for _, m := range plan.Modules {
		if m.Artifact == artifact {
			return m.Status + ":" + m.Version
		}
	}
	return ""
}

func TestClosureFollowsRootVersionAndResolvesLibrariesFromProperties(t *testing.T) {
	f := reactorFake(t)
	src, dst := f.targets()
	in := rootIn()
	in.AsVersion = "03.27.10-beta1" // no --pin anywhere
	plan, err := New().PlanPromote(context.Background(), src, dst, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 || len(plan.BlockingRefs()) != 0 {
		t.Fatalf("nothing should block: %v %v", plan.Blockers, plan.BlockingRefs())
	}
	// the parent shares the root's base version → same target; libraries keep their own base version
	for artifact, want := range map[string]string{"par": "promote:03.27.10-beta1", "fox": "promote:18.0.0-0", "swing-a": "promote:18.0.0-0", "swing-b": "promote:18.0.0-0"} {
		if got := statusOf(plan, artifact); got != want {
			t.Errorf("%s: %q want %q", artifact, got, want)
		}
	}
	// modules come in dependency order: libraries, then the parent, then the root
	var order []string
	for _, p := range plan.Parents {
		order = append(order, p.Artifact)
	}
	if strings.Join(order, ",") != "fox,swing-a,swing-b,par" {
		t.Fatalf("order: %v", order)
	}
	par := string(plan.Parents[3].Items[0].Content)
	for _, want := range []string{"<fox.version>18.0.0-0</fox.version>", "<swing.version>18.0.0-0</swing.version>", "<lonely.version>1.0</lonely.version>", "<version>03.27.10-beta1</version>"} {
		if !strings.Contains(par, want) {
			t.Errorf("parent pom lacks %s:\n%s", want, par)
		}
	}
	if !strings.Contains(string(plan.Items[len(plan.Items)-1].Content)+string(plan.Items[0].Content), "<version>03.27.10-beta1</version></parent>") {
		t.Error("the root must reference the parent at the root's target version")
	}
	// executes in that order
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	var first []string
	for _, p := range f.puts {
		first = append(first, strings.Split(p, "/")[2])
	}
	if strings.Join(first[:4], ",") != "fox,swing-a,swing-b,par" || first[len(first)-1] != "ghc-web" {
		t.Errorf("put order: %v", first)
	}
}

func TestClosureLibraryAlreadyReleasedIsNotPromoted(t *testing.T) {
	f := reactorFake(t)
	f.dst["com/acme/fox/18.0.0-0/fox-18.0.0-0.pom"] = "x"
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, rootIn())
	if err != nil {
		t.Fatal(err)
	}
	if got := statusOf(plan, "fox"); got != "released:18.0.0-0" {
		t.Errorf("fox: %q", got)
	}
	for _, p := range plan.Parents {
		if p.Artifact == "fox" {
			t.Error("a released library must not be planned again")
		}
	}
	if !strings.Contains(string(plan.Parents[len(plan.Parents)-1].Items[0].Content), "<fox.version>18.0.0-0</fox.version>") {
		t.Error("the property must still point at the released version")
	}
}

func TestClosureMissingLibraryBlocksEverything(t *testing.T) {
	f := reactorFake(t)
	var kept []nexus.Component
	for _, c := range f.snap {
		if c.Name != "swing-b" {
			kept = append(kept, c)
		}
	}
	f.snap = kept
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, rootIn())
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 1 || !strings.Contains(plan.Blockers[0], "com.acme:swing-b:18.0.0-0") ||
		!strings.Contains(plan.Blockers[0], "--set-property swing.version=<version>") {
		t.Fatalf("blockers: %v", plan.Blockers)
	}
	if got := statusOf(plan, "swing-b"); got != "blocked:18.0.0-0" {
		t.Errorf("swing-b: %q", got)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err == nil || len(f.puts) != 0 {
		t.Fatalf("an incomplete plan must write nothing (no inconsistent release): %v %v", err, f.puts)
	}
}

func TestClosureExplicitSetPropertyReplacesThePlan(t *testing.T) {
	f := reactorFake(t)
	src, dst := f.targets()
	in := rootIn()
	in.SetProperties = map[string]string{"fox.version": "18.0.0-3"}
	plan, err := New().PlanPromote(context.Background(), src, dst, in)
	if err != nil {
		t.Fatal(err)
	}
	if statusOf(plan, "fox") != "" {
		t.Errorf("an explicit value must not trigger the promotion of fox: %+v", plan.Modules)
	}
	if !strings.Contains(string(plan.Parents[len(plan.Parents)-1].Items[0].Content), "<fox.version>18.0.0-3</fox.version>") {
		t.Error("explicit value expected in the parent pom")
	}
	par := plan.Parents[len(plan.Parents)-1] // warnings belong to the plan whose pom carries the property
	if !strings.Contains(strings.Join(par.Warnings, "|"), "com.acme:fox:18.0.0-3 est absent") {
		t.Errorf("the explicit version is not in the release: %v", par.Warnings)
	}
}

func TestClosureNeededTwiceIsPlannedOnce(t *testing.T) {
	f := reactorFake(t)
	f.addBuilds("ghc-web", "<project><parent><groupId>com.acme</groupId><artifactId>par</artifactId><version>03.27.10-0-SNAPSHOT</version></parent><artifactId>ghc-web</artifactId>"+
		"<dependencies><dependency><groupId>com.acme</groupId><artifactId>fox</artifactId><version>18.0.0-0-SNAPSHOT</version></dependency></dependencies></project>", true, "20260914.099999-99")
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, rootIn())
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range plan.Parents {
		if p.Artifact == "fox" {
			n++
		}
	}
	if n != 1 {
		t.Errorf("fox planned %d times", n)
	}
	if !strings.Contains(string(plan.Items[len(plan.Items)-1].Content)+string(plan.Items[0].Content), "<artifactId>fox</artifactId><version>18.0.0-0</version>") {
		t.Error("a literal SNAPSHOT dependency must be fixed to the planned version")
	}
}

// A real reactor nests parents far deeper than a handful of levels: the plan must still be complete.
func TestClosureDeepParentChainIsPlanned(t *testing.T) {
	noWait(t)
	f := newFake(t, "ALLOW")
	f.src, f.snap = map[string]string{}, nil
	const depth = 9
	name := func(i int) string { return "m" + string(rune('a'+i)) }
	for i := 0; i < depth; i++ {
		pom := "<project><artifactId>" + name(i) + "</artifactId><version>1.0-SNAPSHOT</version></project>"
		if i+1 < depth {
			pom = "<project><parent><groupId>com.acme</groupId><artifactId>" + name(i+1) + "</artifactId><version>1.0-SNAPSHOT</version></parent>" +
				"<artifactId>" + name(i) + "</artifactId><version>1.0-SNAPSHOT</version></project>"
		}
		f.addVersionBuilds(name(i), "1.0", pom, "20260914.010000-1")
	}
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, module.PromoteInput{Group: "com.acme", Artifact: name(0), Version: "1.0-SNAPSHOT", WithParent: true, NoMarker: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 || len(plan.Modules) != depth-1 { // the root is not listed
		t.Fatalf("modules=%d blockers=%v", len(plan.Modules), plan.Blockers)
	}
}

func TestClosureAllowMissingTurnsABlockerIntoAWarning(t *testing.T) {
	f := reactorFake(t)
	var kept []nexus.Component
	for _, c := range f.snap {
		if c.Name != "swing-b" {
			kept = append(kept, c)
		}
	}
	f.snap = kept
	src, dst := f.targets()
	in := rootIn()
	in.AllowMissing = []string{"com.acme:swing-b"}
	plan, err := New().PlanPromote(context.Background(), src, dst, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) != 0 {
		t.Fatalf("no blocker expected: %v", plan.Blockers)
	}
	if got := statusOf(plan, "swing-b"); got != "ignored:18.0.0-0" {
		t.Errorf("swing-b: %q", got)
	}
	// the ignored module is never published; the others still are
	for _, p := range plan.Parents {
		if p.Artifact == "swing-b" {
			t.Error("an ignored module must not be planned for publication")
		}
	}
	// another missing module that is NOT listed still blocks
	in.AllowMissing = []string{"com.acme:other"}
	plan, _ = New().PlanPromote(context.Background(), src, dst, in)
	if len(plan.Blockers) != 1 {
		t.Fatalf("unlisted module must still block: %v", plan.Blockers)
	}
}
