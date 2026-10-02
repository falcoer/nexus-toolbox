package module

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

func sha(s string) string { h := sha1.Sum([]byte(s)); return hex.EncodeToString(h[:]) }

type rec struct {
	started []string
	bytes   int64
	errs    map[string]error
}

func (r *rec) AssetStart(p string, _ int64) { r.started = append(r.started, p) }
func (r *rec) AssetBytes(n int64)           { r.bytes += n }
func (r *rec) AssetDone(p string, err error) {
	if r.errs == nil {
		r.errs = map[string]error{}
	}
	r.errs[p] = err
}

func target(t *testing.T, files map[string]string) (Target, func(name string) FileDetail) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, ok := files[strings.TrimPrefix(r.URL.Path, "/repository/rel/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(c))
	}))
	t.Cleanup(srv.Close)
	tg := Target{Client: nexus.New(srv.URL, "", "")}
	return tg, func(name string) FileDetail {
		return FileDetail{Name: name, URL: srv.URL + "/repository/rel/" + name, SHA1: sha(files[name]), Size: int64(len(files[name]))}
	}
}

func TestDownloadVerifiesAndLeavesNoPartFile(t *testing.T) {
	tg, f := target(t, map[string]string{"a.zip": "ZIPDATA", "b.pom": "<pom/>"})
	dir := filepath.Join(t.TempDir(), "out", "nested") // created on demand
	r := &rec{}
	res, err := Download(context.Background(), tg, []FileDetail{f("a.zip"), f("b.pom")}, DownloadOptions{Dir: dir}, r)
	if err != nil || res.Downloaded != 2 || res.Bytes != int64(len("ZIPDATA")+len("<pom/>")) || r.bytes != res.Bytes {
		t.Fatalf("%+v %v progress=%d", res, err, r.bytes)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.zip")); string(b) != "ZIPDATA" {
		t.Errorf("content: %q", b)
	}
	if m, _ := filepath.Glob(filepath.Join(dir, "*.part")); len(m) != 0 {
		t.Errorf("part files left: %v", m)
	}
}

func TestDownloadRefusesCorruptedTransfer(t *testing.T) {
	tg, f := target(t, map[string]string{"a.zip": "ZIPDATA"})
	bad := f("a.zip")
	bad.SHA1 = sha("something else") // Nexus announced another checksum
	dir := t.TempDir()
	res, err := Download(context.Background(), tg, []FileDetail{bad}, DownloadOptions{Dir: dir}, &rec{})
	if err != ErrPartial || res.Failed != 1 || !strings.Contains(res.Files[0].Error, "sha1 reçu") {
		t.Fatalf("%+v %v", res, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("a corrupted file must leave nothing behind: %v", ents)
	}
}

func TestDownloadSkipsIdenticalAndProtectsDifferent(t *testing.T) {
	tg, f := target(t, map[string]string{"a.zip": "ZIPDATA"})
	dir := t.TempDir()
	if _, err := Download(context.Background(), tg, []FileDetail{f("a.zip")}, DownloadOptions{Dir: dir}, &rec{}); err != nil {
		t.Fatal(err)
	}
	r := &rec{}
	res, err := Download(context.Background(), tg, []FileDetail{f("a.zip")}, DownloadOptions{Dir: dir}, r)
	if err != nil || res.Skipped != 1 || len(r.started) != 0 {
		t.Fatalf("identical file must be skipped without any transfer: %+v %v started=%v", res, err, r.started)
	}
	os.WriteFile(filepath.Join(dir, "a.zip"), []byte("LOCAL"), 0o644)
	res, err = Download(context.Background(), tg, []FileDetail{f("a.zip")}, DownloadOptions{Dir: dir}, &rec{})
	if err != ErrPartial || !strings.Contains(res.Files[0].Error, "--force") {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.zip")); string(b) != "LOCAL" {
		t.Error("local file must be untouched without --force")
	}
	if _, err = Download(context.Background(), tg, []FileDetail{f("a.zip")}, DownloadOptions{Dir: dir, Force: true}, &rec{}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.zip")); string(b) != "ZIPDATA" {
		t.Error("--force must replace the file")
	}
}

func TestDownloadRejectsUnsafeNamesAndContinues(t *testing.T) {
	tg, f := target(t, map[string]string{"ok.txt": "OK"})
	dir := t.TempDir()
	evil := FileDetail{Name: "../evil.txt", URL: f("ok.txt").URL}
	res, err := Download(context.Background(), tg, []FileDetail{evil, {Name: "..", URL: evil.URL}, f("ok.txt")}, DownloadOptions{Dir: dir}, &rec{})
	if err != ErrPartial || res.Failed != 2 || res.Downloaded != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, serr := os.Stat(filepath.Join(filepath.Dir(dir), "evil.txt")); serr == nil {
		t.Error("path traversal: file written outside the destination")
	}
}

func TestDownloadReportsHTTPFailure(t *testing.T) {
	tg, _ := target(t, map[string]string{})
	missing := FileDetail{Name: "gone.zip", URL: tg.Client.Base + "/repository/rel/gone.zip", SHA1: sha("x")}
	res, err := Download(context.Background(), tg, []FileDetail{missing}, DownloadOptions{Dir: t.TempDir()}, &rec{})
	if err != ErrPartial || !strings.Contains(res.Files[0].Error, "404") {
		t.Fatalf("%+v %v", res, err)
	}
}
