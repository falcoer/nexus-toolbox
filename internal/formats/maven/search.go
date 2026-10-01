// Package maven implements the maven2 module: search and promote.
package maven

import (
	"context"
	"net/url"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

type Module struct{}

func New() *Module { return &Module{} }

func (*Module) Format() string { return "maven2" }

var (
	_ module.Searcher = (*Module)(nil)
	_ module.Promoter = (*Module)(nil)
)

type searchIter struct {
	t     module.Target
	in    module.SearchInput
	q     url.Values
	token string
	done  bool
	n     int
}

func (m *Module) Search(_ context.Context, t module.Target, in module.SearchInput) (module.Iterator[module.Hit], error) {
	q := url.Values{"repository": {t.Repo.Name}}
	if in.Query != "" {
		q.Set("q", in.Query)
	}
	if in.Group != "" {
		q.Set("group", in.Group)
	}
	if in.Artifact != "" {
		q.Set("name", in.Artifact)
	}
	if in.Version != "" {
		q.Set("version", in.Version)
	}
	switch in.Sort {
	case "group", "name", "version":
		q.Set("sort", in.Sort)
	case "artifact":
		q.Set("sort", "name")
	}
	return &searchIter{t: t, in: in, q: q}, nil
}

func (it *searchIter) Next(ctx context.Context) ([]module.Hit, bool, error) {
	if it.done {
		return nil, true, nil
	}
	page, err := it.t.Client.SearchComponents(ctx, it.q, it.token)
	if err != nil {
		return nil, false, err
	}
	it.token = page.ContinuationToken
	it.done = it.token == ""
	var out []module.Hit
	for _, c := range page.Items {
		if it.in.FromVersion != "" && CompareVersions(c.Version, it.in.FromVersion) < 0 {
			continue
		}
		if it.in.ToVersion != "" && CompareVersions(c.Version, it.in.ToVersion) > 0 {
			continue
		}
		if it.in.Limit > 0 && it.n >= it.in.Limit {
			it.done = true
			break
		}
		out = append(out, hit(c))
		it.n++
	}
	return out, it.done, nil
}

func hit(c nexus.Component) module.Hit {
	h := module.Hit{ID: c.ID, Repository: c.Repository, Group: c.Group, Artifact: c.Name, Version: c.Version, Assets: len(c.Assets)}
	for _, a := range c.Assets {
		h.Size += a.FileSize
		if a.LastModified.After(h.Modified) {
			h.Modified = a.LastModified
		}
	}
	return h
}
