package cmd

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func setupRepos(t *testing.T) (map[string]string, string) {
	srv, dst := fakeNexus(t)
	t.Setenv("NEXUS_HOME", t.TempDir())
	for _, a := range []string{"SNAP", "REL"} {
		t.Setenv("NEXUS_"+a+"_PASSWORD", "pw")
		t.Setenv("NEXUS_"+a+"_USER", "me")
	}
	base := srv.URL + "/nexus/repository/"
	if _, _, err := run(t, "init", "snap", base+"snap/", "--user", "me"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "init", "rel", base+"rel/", "--user", "me"); err != nil {
		t.Fatal(err)
	}
	return dst, srv.URL
}

func TestShortFormUsesDefaultsAndPrintsTheEquivalentCommand(t *testing.T) {
	dst, _ := setupRepos(t)
	// the fake reports policy MIXED for snap: the pair cannot be inferred until it is linked
	if _, _, err := run(t, "promote", "ghc", "2.0", "--dry-run"); err == nil || !strings.Contains(err.Error(), "repos link") {
		t.Fatalf("an undeterminable pair must say how to fix it: %v", err)
	}
	if _, e, err := run(t, "repos", "link", "snap", "rel"); err != nil || !strings.Contains(e, "promotion par défaut") {
		t.Fatalf("link: %q %v", e, err)
	}
	_, e, err := run(t, "promote", "ghc", "2.0", "--dry-run")
	if err != nil || len(dst) != 0 {
		t.Fatalf("dry-run must write nothing: %v %v", err, dst)
	}
	for _, want := range []string{"1.0-SNAPSHOT (build 20260914.091709-45) → 2.0", "dry-run", "Commande équivalente", "nexus promote ghc 2.0"} {
		if !strings.Contains(e, want) {
			t.Errorf("missing %q in:\n%s", want, e)
		}
	}
	_, e, err = run(t, "promote", "ghc", "2.0", "--yes")
	if err != nil || dst["com/acme/ghc/2.0/ghc-2.0.war"] != "JAR" || !strings.Contains(dst["com/acme/ghc/2.0/ghc-2.0.pom"], "<version>2.0</version>") {
		t.Fatalf("promotion: %v\n%s\n%v", err, e, dst)
	}
	if !strings.Contains(e, "nexus promote ghc 2.0") {
		t.Errorf("recap expected after the run:\n%s", e)
	}
	// target defaults to the source version without -SNAPSHOT
	if _, _, err = run(t, "promote", "ghc", "--yes", "--build", "43"); err != nil || dst["com/acme/ghc/1.0/ghc-1.0.war"] != "JAR" {
		t.Fatalf("default target: %v %v", err, dst)
	}
	if _, _, err = run(t, "promote", "nope", "1.0"); err == nil || !strings.Contains(err.Error(), "introuvable") {
		t.Fatalf("unknown artifact: %v", err)
	}
}

func TestPlanWithExtraModulesNeedsAnExplicitDecision(t *testing.T) {
	dst, _ := setupRepos(t)
	run(t, "repos", "link", "snap", "rel")
	// app inherits from base: publishing app alone would leave an inconsistent release
	_, e, err := run(t, "promote", "app", "3.0", "--yes")
	var pr planRequiredError
	if !errors.As(err, &pr) || len(dst) != 0 {
		t.Fatalf("exit code 5 expected, nothing written: %v %v", err, dst)
	}
	for _, want := range []string{"Plan de promotion", "à promouvoir", "base", "3.0", "--with-deps"} {
		if !strings.Contains(e+pr.why+pr.hint, want) {
			t.Errorf("missing %q in:\n%s\n%s %s", want, e, pr.why, pr.hint)
		}
	}
	if !strings.Contains(e, "nexus promote app 3.0 --with-deps") {
		t.Errorf("the rerun command must carry --with-deps:\n%s", e)
	}
	// the module follows the root's version: no --pin needed
	_, e, err = run(t, "promote", "app", "3.0", "--yes", "--with-deps")
	if err != nil {
		t.Fatalf("%v\n%s", err, e)
	}
	if dst["com/acme/base/3.0/base-3.0.pom"] == "" || !strings.Contains(dst["com/acme/app/3.0/app-3.0.pom"], "<version>3.0</version></parent>") {
		t.Fatalf("base and app must be published at 3.0: %v", dst)
	}
}

func TestWizardWalksThroughTheChoicesAndPrintsTheCommand(t *testing.T) {
	dst, _ := setupRepos(t)
	run(t, "repos", "link", "snap", "rel")
	r, w, _ := os.Pipe()
	w.WriteString("ghc\n\n2.1\no\n") // artifact, newest build (default), target version, confirmation
	w.Close()
	inOverride = r
	t.Cleanup(func() { inOverride = nil; r.Close() })
	_, e, err := run(t, "promote")
	if err != nil {
		t.Fatalf("%v\n%s", err, e)
	}
	if dst["com/acme/ghc/2.1/ghc-2.1.war"] != "JAR" {
		t.Fatalf("wizard promotion missing: %v\n%s", dst, e)
	}
	for _, want := range []string{"Assistant de promotion", "Build :", "20260914.091709-45", "nexus promote ghc 2.1"} {
		if !strings.Contains(e, want) {
			t.Errorf("missing %q in:\n%s", want, e)
		}
	}
}

func TestWizardNeedsATerminalAndALegacyFormStillWorks(t *testing.T) {
	setupRepos(t)
	if _, _, err := run(t, "promote"); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("no tty: %v", err)
	}
	// the full form is untouched: no inference, no recap
	_, e, err := run(t, "promote", "snap", "rel", "com.acme:ghc:1.0-SNAPSHOT", "--dry-run")
	if err != nil || strings.Contains(e, "Commande équivalente") {
		t.Fatalf("legacy form: %v\n%s", err, e)
	}
}

func TestConflictSuggestsTheForceCommand(t *testing.T) {
	relWritePolicy = "ALLOW" // redeploy allowed: --force can replace published files
	t.Cleanup(func() { relWritePolicy = "ALLOW_ONCE" })
	dst, _ := setupRepos(t)
	run(t, "repos", "link", "snap", "rel")
	dst["com/acme/ghc/2.0/ghc-2.0.war"] = "OTHER" // already published with another content
	_, e, err := run(t, "promote", "ghc", "2.0", "--yes")
	if err == nil || !strings.Contains(e, "Pour remplacer") || !strings.Contains(e, "nexus promote ghc 2.0 --force") {
		t.Fatalf("the exact --force command must be offered:\n%s\n%v", e, err)
	}
}

func TestConflictWithAllowOnceExplainsInsteadOfSuggestingForce(t *testing.T) {
	dst, _ := setupRepos(t)
	run(t, "repos", "link", "snap", "rel")
	dst["com/acme/ghc/2.0/ghc-2.0.war"] = "OTHER"
	_, e, err := run(t, "promote", "ghc", "2.0", "--yes")
	if err == nil || strings.Contains(e, "Pour remplacer") || !strings.Contains(err.Error(), "ALLOW_ONCE") {
		t.Fatalf("with ALLOW_ONCE --force cannot help: %v\n%s", err, e)
	}
}
