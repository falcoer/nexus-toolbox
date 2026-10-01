package maven

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

func noWait(t *testing.T) {
	old := verifyBackoff
	verifyBackoff = []time.Duration{0, 0, 0, 0}
	t.Cleanup(func() { verifyBackoff = old })
}

// planned returns a fake destination with a one-artifact plan (pom + war), no marker.
func planned(t *testing.T) (*fake, module.Target, module.Target, *module.PromotePlan) {
	noWait(t)
	f := snapFake(t, "ALLOW", snapPom)
	src, dst := f.targets()
	i := snapIn
	i.NoMarker = true
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil {
		t.Fatal(err)
	}
	return f, src, dst, plan
}

func TestVerifyRetriesWhenChecksumLags(t *testing.T) {
	f, src, dst, plan := planned(t)
	f.lagAfterPut = 2 // the first two .sha1 reads after each upload answer 404
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || !res.Verified {
		t.Fatalf("%+v %v", res, err)
	}
	for _, w := range res.Warnings {
		if strings.Contains(w, "contenu vérifié directement") {
			t.Errorf("a short lag must be solved by retrying, not by re-downloading: %v", res.Warnings)
		}
	}
	if len(res.Published) != 2 || f.cacheHdrMissed {
		t.Errorf("published=%v cacheHeaderMissed=%v", res.Published, f.cacheHdrMissed)
	}
}

func TestVerifyFallsBackToContentWhenChecksumUnavailable(t *testing.T) {
	f, src, dst, plan := planned(t)
	for _, mode := range []string{"404", "wrong"} {
		f.dst = map[string]string{}
		f.sha1Mode = ""
		// plan again on an empty destination, then break the checksum endpoint
		plan, _ = New().PlanPromote(context.Background(), src, dst, module.PromoteInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT", NoMarker: true})
		f.sha1Mode = mode
		res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
		if err != nil || !res.Verified || strings.Count(strings.Join(res.Warnings, "|"), "contenu vérifié directement") != 2 {
			t.Fatalf("%s: %+v %v", mode, res, err)
		}
		w := strings.Join(res.Warnings, "|")
		if !strings.Contains(w, "contenu vérifié directement") || (mode == "404" && !strings.Contains(w, "HTTP 404")) {
			t.Errorf("%s: warnings %v", mode, res.Warnings)
		}
	}
}

func TestVerifyFailsWithAllValuesWhenContentIsWrong(t *testing.T) {
	f, src, dst, plan := planned(t)
	f.sha1Mode, f.tamperGET = "wrong", true
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != module.ErrPartial || res.Verified || len(res.Failed) != 2 || len(res.Published) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	m := res.Failed[0]
	if !strings.Contains(m, "sha1 attendu") || !strings.Contains(m, strings.Repeat("0", 40)) || !strings.Contains(m, "sha1 du contenu publié") {
		t.Errorf("failure must carry expected, served and actual values: %s", m)
	}
}

func TestVerifyReportsHTTPStatus(t *testing.T) {
	f, src, dst, plan := planned(t)
	f.sha1Mode = "403"
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != module.ErrPartial || len(res.Failed) != 2 || !strings.Contains(res.Failed[0], "HTTP 403") || !strings.Contains(res.Failed[0], "contenu illisible") {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestVerifyStopsOnCancelledContext(t *testing.T) {
	old := verifyBackoff
	verifyBackoff = []time.Duration{0, time.Hour}
	defer func() { verifyBackoff = old }()
	f := snapFake(t, "ALLOW", snapPom)
	_, dst := f.targets()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := verifyRemote(ctx, dst, "com/acme/x/1/x-1.pom", "abc"); err == nil || time.Since(start) > 5*time.Second {
		t.Fatalf("must return promptly with an error: %v", err)
	}
}

func TestPublishedListsAlreadyIdenticalFilesToo(t *testing.T) {
	f, src, dst, plan := planned(t)
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err != nil {
		t.Fatal(err)
	}
	f.puts = nil
	plan, _ = New().PlanPromote(context.Background(), src, dst, module.PromoteInput{Group: "com.acme", Artifact: "ghc-web", Version: "03.27.10-0-SNAPSHOT", NoMarker: true})
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.Copied != 0 || res.Skipped != 2 || len(res.Published) != 2 || len(f.puts) != 0 {
		t.Fatalf("%+v %v puts=%v", res, err, f.puts)
	}
}
