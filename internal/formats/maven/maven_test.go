package maven

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

func TestCompareVersions(t *testing.T) {
	less := [][2]string{{"1.0", "1.1"}, {"1.2", "1.10"}, {"1.0-SNAPSHOT", "1.0"}, {"1.0-rc1", "1.0"},
		{"1.0-alpha", "1.0-beta"}, {"1.0", "1.0.1"}, {"1.0", "1.0-sp1"}}
	for _, p := range less {
		if CompareVersions(p[0], p[1]) >= 0 || CompareVersions(p[1], p[0]) <= 0 {
			t.Errorf("%s should be < %s", p[0], p[1])
		}
	}
	if CompareVersions("1.0", "1.0.0") != 0 {
		t.Error("1.0 == 1.0.0")
	}
}

type fake struct {
	mu     sync.Mutex
	src    map[string]string
	dst    map[string]string
	del    []string
	srv    *httptest.Server
	dstPW  string // write policy
	snap   []nexus.Component
	puts   []string
	failOn string // PUT paths containing this answer 500
}

func sum(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

func newFake(t *testing.T, policy string) *fake {
	f := &fake{src: map[string]string{
		"com/acme/lib/1.0/lib-1.0.jar": "JARDATA", "com/acme/lib/1.0/lib-1.0.pom": "<project><version>1.0</version></project>",
		"com/acme/lib/1.0/lib-1.0.jar.sha1": "x", "com/acme/lib/maven-metadata.xml": "m"}, dst: map[string]string{}, dstPW: policy}
	mux := http.NewServeMux()
	mux.HandleFunc("/service/rest/v1/repositories", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"name":"src","format":"maven2","type":"hosted","attributes":{"maven":{"versionPolicy":"MIXED"}}},
		{"name":"dst","format":"maven2","type":"hosted","attributes":{"maven":{"versionPolicy":"RELEASE"},"storage":{"writePolicy":%q}}}]`, f.dstPW)
	})
	mux.HandleFunc("/service/rest/v1/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("maven.baseVersion") != "" {
			var items []nexus.Component
			for _, c := range f.snap {
				if c.Name == r.URL.Query().Get("name") {
					items = append(items, c)
				}
			}
			json.NewEncoder(w).Encode(nexus.ComponentPage{Items: items})
			return
		}
		var assets []nexus.Asset
		for p, c := range f.src {
			assets = append(assets, nexus.Asset{Path: p, DownloadURL: f.srv.URL + "/repository/src/" + p, FileSize: int64(len(c)), Checksum: map[string]string{"sha1": sum(c)}})
		}
		json.NewEncoder(w).Encode(nexus.ComponentPage{Items: []nexus.Component{{ID: "cid", Repository: "src", Group: "com.acme", Name: "lib", Version: "1.0", Assets: assets}}})
	})
	mux.HandleFunc("/service/rest/v1/components/", func(w http.ResponseWriter, r *http.Request) {
		f.del = append(f.del, strings.TrimPrefix(r.URL.Path, "/service/rest/v1/components/"))
		w.WriteHeader(204)
	})
	mux.HandleFunc("/repository/src/", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, f.src[strings.TrimPrefix(r.URL.Path, "/repository/src/")])
	})
	mux.HandleFunc("/repository/dst/", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		p := strings.TrimPrefix(r.URL.Path, "/repository/dst/")
		if r.Method == http.MethodPut {
			if f.failOn != "" && strings.Contains(p, f.failOn) {
				w.WriteHeader(500)
				return
			}
			f.puts = append(f.puts, p)
			b, _ := io.ReadAll(r.Body)
			f.dst[p] = string(b)
			w.WriteHeader(201)
			return
		}
		if base, ok := strings.CutSuffix(p, ".sha1"); ok {
			if c, ok := f.dst[base]; ok {
				io.WriteString(w, sum(c))
				return
			}
		}
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

type noRep struct{}

func (noRep) AssetStart(string, int64) {}
func (noRep) AssetBytes(int64)         {}
func (noRep) AssetDone(string, error)  {}

func (f *fake) targets() (module.Target, module.Target) {
	c := nexus.New(f.srv.URL, "u", "p")
	mk := func(n string) module.Target {
		return module.Target{Client: c, Repo: config.Repo{Alias: n, Name: n, Base: f.srv.URL, Format: "maven2", Type: "hosted"}}
	}
	return mk("src"), mk("dst")
}

var in = module.PromoteInput{Group: "com.acme", Artifact: "lib", Version: "1.0", NoMarker: true}

func TestPromoteSuccessDeleteSource(t *testing.T) {
	f := newFake(t, "ALLOW")
	src, dst := f.targets()
	m := New()
	i := in
	i.DeleteSource = true
	plan, err := m.PlanPromote(context.Background(), src, dst, i)
	if err != nil || len(plan.Items) != 2 || !strings.HasSuffix(plan.Items[1].Path, ".pom") {
		t.Fatalf("%+v %v", plan, err)
	}
	res, err := m.ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.Copied != 2 || !res.Verified || !res.SourceDeleted {
		t.Fatalf("%+v %v", res, err)
	}
	if f.dst["com/acme/lib/1.0/lib-1.0.jar"] != "JARDATA" || len(f.dst) != 2 || f.del[0] != "cid" {
		t.Fatalf("dst=%v del=%v", f.dst, f.del)
	}
}

func TestPromoteResumeSkipsIdentical(t *testing.T) {
	f := newFake(t, "ALLOW_ONCE")
	f.dst["com/acme/lib/1.0/lib-1.0.jar"] = "JARDATA"
	src, dst := f.targets()
	plan, err := New().PlanPromote(context.Background(), src, dst, in)
	if err != nil {
		t.Fatal(err)
	}
	res, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{})
	if err != nil || res.Skipped != 1 || res.Copied != 1 || len(f.del) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestPromoteConflict(t *testing.T) {
	f := newFake(t, "ALLOW_ONCE")
	f.dst["com/acme/lib/1.0/lib-1.0.jar"] = "OTHER"
	src, dst := f.targets()
	i := in
	i.Force = true // ALLOW_ONCE : force must not help
	plan, err := New().PlanPromote(context.Background(), src, dst, i)
	if err != nil || len(plan.Blocking()) != 1 {
		t.Fatalf("%+v %v", plan, err)
	}
	if _, err := New().ExecutePromote(context.Background(), src, dst, plan, noRep{}); err == nil {
		t.Fatal("expected conflict error")
	}
	f2 := newFake(t, "ALLOW")
	f2.dst["com/acme/lib/1.0/lib-1.0.jar"] = "OTHER"
	s2, d2 := f2.targets()
	plan, _ = New().PlanPromote(context.Background(), s2, d2, i)
	if len(plan.Blocking()) != 0 {
		t.Fatalf("force with ALLOW should overwrite: %+v", plan.Items)
	}
}

func TestPromoteRefusals(t *testing.T) {
	f := newFake(t, "ALLOW")
	src, dst := f.targets()
	i := in
	i.Version = "1.0-SNAPSHOT"
	if _, err := New().PlanPromote(context.Background(), src, dst, i); err == nil {
		t.Error("snapshot must be refused")
	}
	if _, err := New().PlanPromote(context.Background(), src, src, in); err == nil {
		t.Error("same repo must be refused")
	}
	f.dstPW = "DENY"
	if _, err := New().PlanPromote(context.Background(), src, dst, in); err == nil {
		t.Error("read-only destination must be refused")
	}
}
