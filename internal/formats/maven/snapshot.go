package maven

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

// ErrNoBuild means the snapshot repository holds no build of the requested version.
var ErrNoBuild = errors.New("aucun build")

var tsVersionRe = regexp.MustCompile(`^(.+)-(\d{8}\.\d{6})-(\d+)$`)

// SplitSnapshotVersion parses a timestamped snapshot version
// (03.27.10-0-20260914.070210-43 → base 03.27.10-0, ts, 43).
func SplitSnapshotVersion(v string) (base, ts string, n int, ok bool) {
	m := tsVersionRe.FindStringSubmatch(v)
	if m == nil {
		return "", "", 0, false
	}
	n, _ = strconv.Atoi(m[3])
	return m[1], m[2], n, true
}

// buildAsset is one file of a snapshot build, with the part of its name after
// "<artifactId>-<fileVersion>" (e.g. ".war", "-sources.jar").
type buildAsset struct {
	nexus.Asset
	Rest string
}

// Build groups the files of one deployment of a -SNAPSHOT version.
type Build struct {
	Key         string // "20260914.070210-43", or "SNAPSHOT" for a non-unique snapshot
	TS          string
	N           int
	FileVersion string // version string embedded in the file names
	Assets      []buildAsset
	Components  map[string]bool // Nexus component ids holding these assets
}

// listBuilds returns the builds of base-SNAPSHOT found in the repository, oldest first.
func listBuilds(ctx context.Context, c *nexus.Client, repo, group, artifact, base string) ([]*Build, error) {
	snap := base + "-SNAPSHOT"
	queries := []url.Values{
		{"repository": {repo}, "group": {group}, "name": {artifact}, "maven.baseVersion": {snap}},
		{"repository": {repo}, "group": {group}, "name": {artifact}, "version": {base + "-*"}},
	}
	nameRe := regexp.MustCompile("^" + regexp.QuoteMeta(artifact+"-"+base) + `-(?:(\d{8}\.\d{6})-(\d+)|SNAPSHOT)(.*)$`)
	for _, q := range queries {
		byKey := map[string]*Build{}
		token := ""
		for {
			page, err := c.SearchComponents(ctx, q, token)
			if err != nil {
				return nil, err
			}
			for _, comp := range page.Items {
				if comp.Group != group || comp.Name != artifact {
					continue
				}
				for _, a := range comp.Assets {
					if !strings.Contains(a.Path, "/"+snap+"/") || isGenerated(a.Path) {
						continue
					}
					m := nameRe.FindStringSubmatch(path.Base(a.Path))
					if m == nil {
						continue
					}
					key, fileVer := "SNAPSHOT", snap
					b := &Build{}
					if m[1] != "" {
						key, fileVer = m[1]+"-"+m[2], base+"-"+m[1]+"-"+m[2]
					}
					if got, ok := byKey[key]; ok {
						b = got
					} else {
						b.Key, b.FileVersion, b.Components = key, fileVer, map[string]bool{}
						b.TS = m[1]
						b.N, _ = strconv.Atoi(m[2])
						byKey[key] = b
					}
					b.Assets = append(b.Assets, buildAsset{Asset: a, Rest: m[3]})
					b.Components[comp.ID] = true
				}
			}
			if token = page.ContinuationToken; token == "" {
				break
			}
		}
		if len(byKey) > 0 {
			out := make([]*Build, 0, len(byKey))
			for _, b := range byKey {
				out = append(out, b)
			}
			sort.Slice(out, func(i, j int) bool {
				if out[i].N != out[j].N {
					return out[i].N < out[j].N
				}
				return out[i].TS < out[j].TS
			})
			return out, nil
		}
	}
	return nil, fmt.Errorf("%w de %s:%s:%s-SNAPSHOT dans %s", ErrNoBuild, group, artifact, base, repo)
}

// selectBuild picks a build: wanted may be "", "43" or "20260914.070210-43".
func selectBuild(builds []*Build, wanted string) (*Build, error) {
	if wanted == "" {
		return builds[len(builds)-1], nil // latest
	}
	for _, b := range builds {
		if b.Key == wanted || strconv.Itoa(b.N) == wanted {
			return b, nil
		}
	}
	keys := make([]string, len(builds))
	for i, b := range builds {
		keys[i] = b.Key
	}
	return nil, fmt.Errorf("build %q introuvable (disponibles : %s)", wanted, strings.Join(keys, ", "))
}
