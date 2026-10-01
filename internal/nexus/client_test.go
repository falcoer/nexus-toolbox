package nexus

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestSearchPaginationAndAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); !ok || u != "me" || p != "pw" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Query().Get("continuationToken") == "" {
			w.Write([]byte(`{"items":[{"name":"a"}],"continuationToken":"t2"}`))
			return
		}
		w.Write([]byte(`{"items":[{"name":"b"}],"continuationToken":null}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "me", "pw")
	p1, err := c.SearchComponents(context.Background(), url.Values{"repository": {"r"}}, "")
	if err != nil || p1.ContinuationToken != "t2" {
		t.Fatalf("%+v %v", p1, err)
	}
	p2, _ := c.SearchComponents(context.Background(), url.Values{}, p1.ContinuationToken)
	if p2.ContinuationToken != "" || p2.Items[0].Name != "b" {
		t.Fatalf("%+v", p2)
	}
	bad := New(srv.URL, "me", "nope")
	if _, err := bad.Repositories(context.Background()); !IsAuth(err) {
		t.Fatalf("want auth error, got %v", err)
	}
}

func TestRetryOn5xx(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) < 3 {
			w.WriteHeader(503)
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := New(srv.URL, "", "")
	c.Backoff = time.Millisecond
	if _, err := c.Repositories(context.Background()); err != nil || n != 3 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
