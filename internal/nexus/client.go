// Package nexus is a minimal client for the Nexus Repository 3 REST API.
package nexus

import (
	"context"
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	Base     string // https://host/nexus (no trailing slash)
	User     string
	Password string
	HTTP     *http.Client
	Retries  int           // extra attempts for idempotent requests (default 2)
	Backoff  time.Duration // base backoff (default 500ms)
	Debug    func(format string, a ...any)
}

func New(base, user, password string) *Client {
	return &Client{Base: strings.TrimRight(base, "/"), User: user, Password: password,
		HTTP: &http.Client{Timeout: 0}, Retries: 2, Backoff: 500 * time.Millisecond}
}

// HTTPError is returned for non-2xx responses.
type HTTPError struct {
	Status int
	Method string
	URL    string
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s %s → HTTP %d %s", e.Method, e.URL, e.Status, strings.TrimSpace(truncate(e.Body, 200)))
}

// IsAuth reports an authentication/authorization failure (exit code 4).
func IsAuth(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.Status == 401 || he.Status == 403)
}

func IsNotFound(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && he.Status == 404
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func (c *Client) debugf(f string, a ...any) {
	if c.Debug != nil {
		c.Debug(f, a...)
	}
}

// do sends a request. body must be re-creatable for retries: pass bodyFn (nil for none).
func (c *Client) do(ctx context.Context, method, u string, bodyFn func() (io.Reader, error), size int64, ctype string, idempotent bool) (*http.Response, error) {
	attempts := 1
	if idempotent {
		attempts += c.Retries
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.Backoff << (i - 1)):
			}
		}
		var body io.Reader
		if bodyFn != nil {
			var err error
			if body, err = bodyFn(); err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, u, body)
		if err != nil {
			return nil, err
		}
		if size > 0 {
			req.ContentLength = size
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.Header.Set("Accept", "application/json, */*")
		req.Header.Set("User-Agent", "nexus-toolbox")
		if method == http.MethodGet || method == http.MethodHead {
			// reads (plan, verification) must never be answered by an intermediate cache
			req.Header.Set("Cache-Control", "no-cache")
			req.Header.Set("Pragma", "no-cache")
		}
		if c.User != "" {
			req.SetBasicAuth(c.User, c.Password)
		}
		c.debugf("%s %s", method, u)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		lastErr = &HTTPError{resp.StatusCode, method, u, string(b)}
		if resp.StatusCode < 500 && resp.StatusCode != 429 {
			return nil, lastErr
		}
	}
	return nil, lastErr
}

func (c *Client) getJSON(ctx context.Context, u string, out any) error {
	resp, err := c.do(ctx, http.MethodGet, u, nil, 0, "", true)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// RepoInfo is an entry of GET /service/rest/v1/repositories.
type RepoInfo struct {
	Name       string `json:"name"`
	Format     string `json:"format"`
	Type       string `json:"type"`
	URL        string `json:"url"`
	Attributes struct {
		Maven struct {
			VersionPolicy string `json:"versionPolicy"`
			LayoutPolicy  string `json:"layoutPolicy"`
		} `json:"maven"`
		Storage struct {
			WritePolicy string `json:"writePolicy"`
		} `json:"storage"`
	} `json:"attributes"`
}

func (c *Client) Repositories(ctx context.Context) ([]RepoInfo, error) {
	var out []RepoInfo
	return out, c.getJSON(ctx, c.Base+"/service/rest/v1/repositories", &out)
}

// Repository looks up a single repository by name.
func (c *Client) Repository(ctx context.Context, name string) (RepoInfo, error) {
	all, err := c.Repositories(ctx)
	if err != nil {
		return RepoInfo{}, err
	}
	for _, r := range all {
		if r.Name == name {
			return r, nil
		}
	}
	return RepoInfo{}, fmt.Errorf("repository %q introuvable sur %s (ou non visible avec ce compte)", name, c.Base)
}

type Asset struct {
	ID           string            `json:"id"`
	Path         string            `json:"path"`
	DownloadURL  string            `json:"downloadUrl"`
	Repository   string            `json:"repository"`
	ContentType  string            `json:"contentType"`
	FileSize     int64             `json:"fileSize"`
	LastModified time.Time         `json:"lastModified"`
	Checksum     map[string]string `json:"checksum"`
}

type Component struct {
	ID         string  `json:"id"`
	Repository string  `json:"repository"`
	Format     string  `json:"format"`
	Group      string  `json:"group"`
	Name       string  `json:"name"`
	Version    string  `json:"version"`
	Assets     []Asset `json:"assets"`
}

type ComponentPage struct {
	Items             []Component `json:"items"`
	ContinuationToken string      `json:"continuationToken"`
}

// SearchComponents fetches one page of GET /service/rest/v1/search.
func (c *Client) SearchComponents(ctx context.Context, q url.Values, token string) (ComponentPage, error) {
	q = cloneValues(q)
	if token != "" {
		q.Set("continuationToken", token)
	}
	var p ComponentPage
	return p, c.getJSON(ctx, c.Base+"/service/rest/v1/search?"+q.Encode(), &p)
}

func cloneValues(v url.Values) url.Values {
	o := url.Values{}
	for k, vs := range v {
		o[k] = append([]string(nil), vs...)
	}
	return o
}

// Download streams an asset into w.
func (c *Client) Download(ctx context.Context, downloadURL string, w io.Writer) (int64, error) {
	resp, err := c.do(ctx, http.MethodGet, downloadURL, nil, 0, "", true)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return io.Copy(w, resp.Body)
}

// Put uploads a file to a hosted repository path (maven layout).
// open must return a fresh reader each call.
func (c *Client) Put(ctx context.Context, repo, path string, open func() (io.Reader, error), size int64, ctype string) error {
	u := c.Base + "/repository/" + repo + "/" + strings.TrimLeft(path, "/")
	resp, err := c.do(ctx, http.MethodPut, u, open, size, ctype, false)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// DeleteComponent removes a component and all its assets.
func (c *Client) DeleteComponent(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, c.Base+"/service/rest/v1/components/"+url.PathEscape(id), nil, 0, "", false)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// GetText fetches a small text resource (e.g. a .sha1 file) and returns its trimmed first field.
func (c *Client) GetText(ctx context.Context, u string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, u, nil, 0, "", true)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if err != nil {
		return "", err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return "", nil
	}
	return f[0], nil
}

// RepoURL builds the URL of a path inside a repository.
func (c *Client) RepoURL(repo, path string) string {
	return c.Base + "/repository/" + repo + "/" + strings.TrimLeft(path, "/")
}

// GetBytes downloads a small resource (max 8 MiB) into memory.
func (c *Client) GetBytes(ctx context.Context, u string) ([]byte, error) {
	resp, err := c.do(ctx, http.MethodGet, u, nil, 0, "", true)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err == nil && len(b) > 8<<20 {
		err = fmt.Errorf("%s : ressource trop volumineuse", u)
	}
	return b, err
}

// Head returns the Content-Length of a resource (-1 when unknown).
func (c *Client) Head(ctx context.Context, u string) (int64, error) {
	resp, err := c.do(ctx, http.MethodHead, u, nil, 0, "", true)
	if err != nil {
		return -1, err
	}
	resp.Body.Close()
	return resp.ContentLength, nil
}

// HeadInfo returns the size (-1 when unknown) and Last-Modified (zero when unknown) of a resource.
func (c *Client) HeadInfo(ctx context.Context, u string) (int64, time.Time, error) {
	resp, err := c.do(ctx, http.MethodHead, u, nil, 0, "", true)
	if err != nil {
		return -1, time.Time{}, err
	}
	resp.Body.Close()
	mod, _ := http.ParseTime(resp.Header.Get("Last-Modified"))
	return resp.ContentLength, mod, nil
}

// HashFile streams a resource into a SHA-1 hash and returns the hex digest and byte count.
func (c *Client) HashFile(ctx context.Context, u string) (string, int64, error) {
	resp, err := c.do(ctx, http.MethodGet, u, nil, 0, "", true)
	if err != nil {
		return "", 0, err
	}
	defer resp.Body.Close()
	h := sha1.New()
	n, err := io.Copy(h, resp.Body)
	if err != nil {
		return "", n, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// HashFileSums streams a resource and returns its SHA-1 and MD5 (hex) and byte count.
func (c *Client) HashFileSums(ctx context.Context, u string) (sha, md string, n int64, err error) {
	resp, err := c.do(ctx, http.MethodGet, u, nil, 0, "", true)
	if err != nil {
		return "", "", 0, err
	}
	defer resp.Body.Close()
	h1, h2 := sha1.New(), md5.New()
	n, err = io.Copy(io.MultiWriter(h1, h2), resp.Body)
	if err != nil {
		return "", "", n, err
	}
	return hex.EncodeToString(h1.Sum(nil)), hex.EncodeToString(h2.Sum(nil)), n, nil
}
