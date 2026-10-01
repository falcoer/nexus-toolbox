package maven

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

// verifyBackoff are the waits before each attempt to read the destination's .sha1
// (the checksum may lag behind the upload, or a cache may still answer with a stale 404).
// Tests replace it with zeros.
var verifyBackoff = []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second}

// verifyResult carries a non-fatal remark about a successful verification.
type verifyResult struct{ Warning string }

// verifyRemote checks that the destination holds expected (SHA-1 hex) at path:
//  1. read <path>.sha1 (no-cache), retrying with a back-off;
//  2. if that stays wrong or unreadable, hash the published file itself;
//  3. fail with every value involved (expected, served, actual content).
//
// A broken .sha1 therefore never blocks a correct upload, and a wrong content is never accepted.
func verifyRemote(ctx context.Context, dst module.Target, path, expected string) (verifyResult, error) {
	var served string
	var lastErr error
	for _, wait := range verifyBackoff {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return verifyResult{}, ctx.Err()
			case <-time.After(wait):
			}
		}
		served, lastErr = dst.Client.GetText(ctx, dst.Client.RepoURL(dst.Repo.Name, path+".sha1"))
		if lastErr == nil && strings.EqualFold(served, expected) {
			return verifyResult{}, nil
		}
		if errors.Is(lastErr, context.Canceled) || errors.Is(lastErr, context.DeadlineExceeded) {
			return verifyResult{}, lastErr
		}
	}
	servedDesc := served
	if lastErr != nil {
		servedDesc = describeErr(lastErr)
	} else if servedDesc == "" {
		servedDesc = "(vide)"
	}
	got, n, err := dst.Client.HashFile(ctx, dst.Client.RepoURL(dst.Repo.Name, path))
	if err != nil {
		if ctx.Err() != nil {
			return verifyResult{}, ctx.Err()
		}
		return verifyResult{}, fmt.Errorf("sha1 attendu %s ; sha1 servi par Nexus : %s ; contenu illisible : %s", expected, servedDesc, describeErr(err))
	}
	if strings.EqualFold(got, expected) {
		return verifyResult{Warning: fmt.Sprintf("%s : le .sha1 servi par Nexus (%s) ne correspond pas au contenu publié ; contenu vérifié directement (%.1f MiB relus, sha1 %s)", path, servedDesc, float64(n)/(1<<20), got)}, nil
	}
	return verifyResult{}, fmt.Errorf("sha1 attendu %s ; sha1 servi par Nexus : %s ; sha1 du contenu publié : %s", expected, servedDesc, got)
}

func describeErr(err error) string {
	var he *nexus.HTTPError
	if errors.As(err, &he) {
		return fmt.Sprintf("HTTP %d", he.Status)
	}
	return err.Error()
}
