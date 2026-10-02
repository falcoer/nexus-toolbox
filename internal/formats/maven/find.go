package maven

import (
	"context"
	"net/url"
	"sort"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

var _ module.Finder = (*Module)(nil)

// FindArtifacts returns the distinct groupId:artifactId pairs of the repository whose artifactId
// is exactly name.
func (m *Module) FindArtifacts(ctx context.Context, t module.Target, name string) ([]module.ArtifactRef, error) {
	q := url.Values{"repository": {t.Repo.Name}, "name": {name}}
	seen := map[module.ArtifactRef]bool{}
	token := ""
	for page := 0; page < 20; page++ {
		res, err := t.Client.SearchComponents(ctx, q, token)
		if err != nil {
			return nil, err
		}
		for _, c := range res.Items {
			if c.Name == name {
				seen[module.ArtifactRef{Group: c.Group, Artifact: c.Name}] = true
			}
		}
		if token = res.ContinuationToken; token == "" {
			break
		}
	}
	out := make([]module.ArtifactRef, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Group < out[j].Group })
	return out, nil
}
