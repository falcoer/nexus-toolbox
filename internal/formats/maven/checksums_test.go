package maven

import (
	"context"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

func realSumsPlan(t *testing.T, in module.PromoteInput) (*fake, module.Target, module.Target, *module.PromotePlan) {
	noWait(t)
	f := snapFake(t, "ALLOW", snapPom)
	f.realSums = true
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, in)
	if err != nil {
		t.Fatal(err)
	}
	return f, src, dst, plan
}

// A Nexus that serves no .sha1 for bare PUTs (what the real server did): the tool publishes
// the checksum files itself, and verification is then a plain .sha1 read (no warnings).
func TestChecksumFilesArePublished(t *testing.T) {
	f, src, dst, plan := realSumsPlan(t, snapIn)
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || !res.Verified || res.Copied != 2 || !res.MarkerWritten {
		t.Fatalf("%+v %v", res, err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "contenu vérifié directement") {
			t.Errorf("checksums are served now, no content re-read expected: %v", res.Warnings)
		}
	}
	if len(f.dst) != 3 || len(f.sums) != 6 {
		t.Fatalf("3 files + marker need 6 checksum files: dst=%d sums=%d", len(f.dst), len(f.sums))
	}
	war := "com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.war"
	if f.sums[war+".sha1"] != sum("WAR-20260914.091709-45") || f.sums[war+".md5"] != md5Hex([]byte("WAR-20260914.091709-45")) {
		t.Errorf("war checksums: %q %q", f.sums[war+".sha1"], f.sums[war+".md5"])
	}
}

// Files published earlier by a bare PUT (no .sha1 served): same content → only the checksums are added.
func TestExistingFilesWithoutChecksumsAreCompleted(t *testing.T) {
	f, src, dst, plan := realSumsPlan(t, snapIn)
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	f.sums, f.puts = map[string]string{}, nil // simulate the earlier, checksum-less promotion
	plan, err := New().PlanPromote(context.Background(), src, dst, snapIn)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range plan.Items {
		if it.Kind == module.KindFile && (it.Action != "skip" || !it.MissingChecksums) {
			t.Fatalf("identical file without .sha1 must be completed, not re-uploaded or flagged: %+v", it)
		}
	}
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.Copied != 0 || res.ChecksumsAdded != 2 || len(f.puts) != 0 || len(f.sums) < 4 {
		t.Fatalf("%+v %v puts=%v sums=%d", res, err, f.puts, len(f.sums))
	}
	// and a second run is a no-op for the main files
	plan, _ = New().PlanPromote(context.Background(), src, dst, snapIn)
	res, err = New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.ChecksumsAdded != 0 || res.Copied != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDifferentFileWithoutChecksumIsAConflict(t *testing.T) {
	noWait(t)
	f := snapFake(t, "ALLOW_ONCE", snapPom)
	f.realSums = true
	f.dst["com/acme/ghc-web/03.27.10-0/ghc-web-03.27.10-0.war"] = "WAR-20260914.084843-44" // no .sha1 served
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
	if war.Action != "conflict" || war.RemoteSHA1 != sum("WAR-20260914.084843-44") || war.MatchBuild != "20260914.084843-44" {
		t.Fatalf("%+v", war)
	}
}

func TestReleasedInDestinationWithoutChecksum(t *testing.T) {
	f := snapFake(t, "ALLOW", snapPom)
	f.realSums = true
	f.dst["com/acme/par/1.0/par-1.0.pom"] = "x" // published by a bare PUT: no .sha1
	_, dst := f.targets()
	if !releasedInDestination(context.Background(), dst, "com.acme", "par", "1.0") {
		t.Error("a pom without .sha1 is still published")
	}
	if releasedInDestination(context.Background(), dst, "com.acme", "par", "2.0") {
		t.Error("absent version")
	}
}
