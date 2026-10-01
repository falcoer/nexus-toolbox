package cmd

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/ui"
)

func sha(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

func fakeNexus(t *testing.T) (*httptest.Server, map[string]string) {
	dst := map[string]string{}
	jar := "JAR"
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/nexus/service/rest/v1/repositories", func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "me" || p != "pw" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `[{"name":"snap","format":"maven2","type":"hosted","attributes":{"maven":{"versionPolicy":"MIXED"}}},
			{"name":"rel","format":"maven2","type":"hosted","attributes":{"maven":{"versionPolicy":"RELEASE"},"storage":{"writePolicy":"ALLOW_ONCE"}}}]`)
	})
	mux.HandleFunc("/nexus/service/rest/v1/search", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"items":[{"id":"c1","repository":"snap","group":"com.acme","name":"lib","version":"1.0",
		"assets":[{"path":"com/acme/lib/1.0/lib-1.0.jar","downloadUrl":"%s/nexus/repository/snap/com/acme/lib/1.0/lib-1.0.jar","fileSize":3,"checksum":{"sha1":"%s"}}]}],"continuationToken":null}`, srv.URL, sha(jar))
	})
	mux.HandleFunc("/nexus/repository/snap/", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, jar) })
	mux.HandleFunc("/nexus/repository/rel/", func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/nexus/repository/rel/")
		if r.Method == http.MethodPut {
			b, _ := io.ReadAll(r.Body)
			dst[p] = string(b)
			w.WriteHeader(201)
			return
		}
		if base, ok := strings.CutSuffix(p, ".sha1"); ok && dst[base] != "" {
			io.WriteString(w, sha(dst[base]))
			return
		}
		http.NotFound(w, r)
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, dst
}

func run(t *testing.T, args ...string) (string, string, error) {
	var o, e bytes.Buffer
	outOverride, errOverride = &o, &e
	t.Cleanup(func() { outOverride, errOverride = nil, nil })
	flags = ui.Flags{}
	root := newRoot()
	root.SetArgs(args)
	err := root.Execute()
	return o.String(), e.String(), err
}

func TestEndToEnd(t *testing.T) {
	srv, dst := fakeNexus(t)
	t.Setenv("NEXUS_HOME", t.TempDir())
	t.Setenv("NEXUS_SNAP_PASSWORD", "pw")
	t.Setenv("NEXUS_REL_PASSWORD", "pw")
	t.Setenv("NEXUS_SNAP_USER", "me")
	t.Setenv("NEXUS_REL_USER", "me")
	base := srv.URL + "/nexus/repository/"

	if _, _, err := run(t, "init", "snap", base+"snap/", "--user", "me"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, "init", "rel", base+"rel/", "--user", "me"); err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, "repos", "list", "-o", "plain")
	if err != nil || !strings.Contains(out, "snap\tmaven2\thosted\tmixed") && !strings.Contains(out, "snap\tmaven2\thosted\tMIXED") {
		t.Fatalf("repos: %q %v", out, err)
	}
	out, _, err = run(t, "search", "snap", "lib")
	if err != nil || !strings.HasPrefix(out, "com.acme:lib\t1.0\t1\t3 B") {
		t.Fatalf("search: %q %v", out, err)
	}
	out, _, err = run(t, "search", "snap", "-o", "json")
	if err != nil || !strings.Contains(out, `"artifact": "lib"`) {
		t.Fatalf("search json: %q %v", out, err)
	}
	if _, e, err := run(t, "promote", "snap", "rel", "com.acme:lib:1.0", "--dry-run"); err != nil || len(dst) != 0 || !strings.Contains(e, "dry-run") {
		t.Fatalf("dry-run: %q %v %v", e, err, dst)
	}
	if _, e, err := run(t, "promote", "snap", "rel", "com.acme:lib:1.0", "--yes"); err != nil || dst["com/acme/lib/1.0/lib-1.0.jar"] != "JAR" {
		t.Fatalf("promote: %q %v %v", e, err, dst)
	}
	// second run is a no-op resume
	if _, e, err := run(t, "promote", "snap", "rel", "com.acme:lib:1.0", "--yes"); err != nil || !strings.Contains(e, "1 ignoré") {
		t.Fatalf("resume: %q %v", e, err)
	}
	if _, _, err := run(t, "promote", "snap", "rel", "badcoords"); err == nil {
		t.Fatal("bad coords must fail")
	}
	if _, _, err := run(t, "search", "unknown"); err == nil {
		t.Fatal("unknown alias must fail")
	}
}
