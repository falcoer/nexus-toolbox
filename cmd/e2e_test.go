package cmd

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/ui"
)

const snapPom = "<project><artifactId>ghc</artifactId><version>1.0-SNAPSHOT</version></project>"

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
		if r.URL.Query().Get("maven.baseVersion") != "" {
			var items []string
			for _, b := range []string{"20260914.070210-43", "20260914.091709-45"} {
				items = append(items, fmt.Sprintf(`{"id":"c-%s","group":"com.acme","name":"ghc","version":"1.0-%s","assets":[
				{"path":"com/acme/ghc/1.0-SNAPSHOT/ghc-1.0-%s.pom","downloadUrl":"%s/nexus/repository/snap/pom","fileSize":%d,"checksum":{"sha1":"%s"}},
				{"path":"com/acme/ghc/1.0-SNAPSHOT/ghc-1.0-%s.war","downloadUrl":"%s/nexus/repository/snap/war","fileSize":3,"checksum":{"sha1":"%s"}}]}`,
					b, b, b, srv.URL, len(snapPom), sha(snapPom), b, srv.URL, sha(jar)))
			}
			fmt.Fprintf(w, `{"items":[%s]}`, strings.Join(items, ","))
			return
		}
		fmt.Fprintf(w, `{"items":[{"id":"c1","repository":"snap","group":"com.acme","name":"lib","version":"1.0",
		"assets":[{"path":"com/acme/lib/1.0/lib-1.0.jar","downloadUrl":"%s/nexus/repository/snap/com/acme/lib/1.0/lib-1.0.jar","fileSize":3,"checksum":{"sha1":"%s"}}]}],"continuationToken":null}`, srv.URL, sha(jar))
	})
	mux.HandleFunc("/nexus/repository/snap/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/pom") || strings.HasSuffix(r.URL.Path, ".pom") {
			io.WriteString(w, snapPom)
			return
		}
		io.WriteString(w, jar)
	})
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
		if c, ok := dst[p]; ok && !strings.HasSuffix(p, ".sha1") {
			w.Header().Set("Last-Modified", "Mon, 14 Sep 2026 14:20:00 GMT")
			w.Header().Set("Content-Length", fmt.Sprint(len(c)))
			if r.Method != http.MethodHead {
				io.WriteString(w, c)
			}
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
	// snapshot → release with re-versioning
	_, e, err := run(t, "promote", "snap", "rel", "com.acme:ghc:1.0-SNAPSHOT", "--dry-run")
	t.Log("\n" + e)
	if err != nil || !strings.Contains(e, "ghc-1.0-20260914.091709-45.war") || !strings.Contains(e, "1.0-SNAPSHOT → 1.0") {
		t.Fatalf("snapshot dry-run: %v", err)
	}
	if dst["com/acme/ghc/1.0/ghc-1.0.war"] != "" {
		t.Fatal("dry-run wrote")
	}
	out, e, err = run(t, "promote", "snap", "rel", "com.acme:ghc:1.0-SNAPSHOT", "--yes", "-o", "json")
	t.Log("\n" + e)
	if err != nil || dst["com/acme/ghc/1.0/ghc-1.0.war"] != "JAR" || !strings.Contains(dst["com/acme/ghc/1.0/ghc-1.0.pom"], "<version>1.0</version>") ||
		!strings.Contains(out, `"target_version": "1.0"`) || !strings.Contains(dst["com/acme/ghc/1.0/ghc-1.0-promoted-from-1.0-20260914.091709-45.txt"], "source-build: 20260914.091709-45") {
		t.Fatalf("snapshot promote: %v\n%s\n%v", err, out, dst)
	}
	// an existing release with different content is explained, and every blocker is reported
	dst["com/acme/ghc/1.0/ghc-1.0.war"] = "OTHER-WAR"
	_, e, err = run(t, "promote", "snap", "rel", "com.acme:ghc:1.0-SNAPSHOT", "--dry-run")
	t.Log("\n" + e)
	if err == nil || !strings.Contains(err.Error(), "existent déjà") || !strings.Contains(err.Error(), "ALLOW_ONCE") ||
		!strings.Contains(e, "destination : 9 B, publié le 2026-09-14") || !strings.Contains(e, "la version 1.0 existe déjà") ||
		!strings.Contains(e, "déjà promue par nexus") {
		t.Fatalf("conflict diagnostics: %v", err)
	}
	// info: details and direct links
	out, _, err = run(t, "info", "snap", "com.acme:ghc:1.0-SNAPSHOT", "--links")
	wantLink := srv.URL + "/nexus/repository/snap/com/acme/ghc/1.0-SNAPSHOT/ghc-1.0-20260914.091709-45.war"
	if err != nil || !strings.Contains(out, wantLink+"\n") || strings.Contains(out, "Téléchargement") {
		t.Fatalf("info --links: %q %v", out, err)
	}
	out, _, err = run(t, "info", "snap", "com.acme:ghc:1.0-SNAPSHOT", "-o", "table")
	if err != nil || !strings.Contains(out, "Téléchargement direct") || !strings.Contains(out, "  "+wantLink) ||
		!strings.Contains(out, "snapshot · build 20260914.091709-45") || !strings.Contains(out, "Répertoire") {
		t.Fatalf("info table: %q %v", out, err)
	}
	out, _, err = run(t, "info", "snap", "com.acme:ghc:1.0-SNAPSHOT", "-o", "json")
	if err != nil || !strings.Contains(out, `"url": "`+wantLink+`"`) || !strings.Contains(out, `"schema": 1`) {
		t.Fatalf("info json: %q %v", out, err)
	}
	if _, _, err := run(t, "info", "snap", "bad"); err == nil {
		t.Fatal("bad coordinates must fail")
	}
	// download: files + sha1 check, idempotent re-run, conflict without --force
	dl := t.TempDir()
	out, e, err = run(t, "download", "snap", "com.acme:ghc:1.0-SNAPSHOT", "--dir", dl, "-o", "plain")
	if err != nil || !strings.Contains(e, "2 téléchargé(s)") || strings.Count(out, "ghc-1.0-20260914.091709-45.") != 2 {
		t.Fatalf("download: %q %q %v", out, e, err)
	}
	if b, rerr := os.ReadFile(filepath.Join(dl, "ghc-1.0-20260914.091709-45.war")); rerr != nil || string(b) != "JAR" {
		t.Fatalf("downloaded war: %q %v", b, rerr)
	}
	if _, e, err = run(t, "download", "snap", "com.acme:ghc:1.0-SNAPSHOT", "--dir", dl); err != nil || !strings.Contains(e, "0 téléchargé(s)") || !strings.Contains(e, "2 déjà présent(s)") {
		t.Fatalf("re-run must skip identical files: %q %v", e, err)
	}
	os.WriteFile(filepath.Join(dl, "ghc-1.0-20260914.091709-45.war"), []byte("LOCAL EDIT"), 0o644)
	if _, _, err = run(t, "download", "snap", "com.acme:ghc:1.0-SNAPSHOT", "--dir", dl); err == nil {
		t.Fatal("a different local file must be refused without --force")
	}
	if _, _, err = run(t, "download", "snap", "com.acme:ghc:1.0-SNAPSHOT", "--dir", dl, "--force", "--include", "*.war"); err != nil {
		t.Fatalf("--force: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dl, "ghc-1.0-20260914.091709-45.war")); string(b) != "JAR" {
		t.Fatalf("--force must restore the published content: %q", b)
	}
	if _, _, err = run(t, "download", "snap", "com.acme:ghc:1.0-SNAPSHOT", "--dir", dl, "--include", "*.nope"); err == nil {
		t.Fatal("no match must fail")
	}
	if _, _, err = run(t, "download", "snap", "com.acme:ghc"); err == nil {
		t.Fatal("version required")
	}
}
