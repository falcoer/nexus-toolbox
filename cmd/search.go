package cmd

import (
	"context"
	"fmt"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

type hitSource struct {
	it module.Iterator[module.Hit]
}

func (hitSource) Columns() []ui.Column {
	return []ui.Column{
		{Title: "artifact", Style: (*ui.Env).Accent},
		{Title: "version"},
		{Title: "files", Right: true, Priority: 3},
		{Title: "size", Right: true, Priority: 2},
		{Title: "modifié", Priority: 1, Style: (*ui.Env).Muted},
	}
}

func (s hitSource) Next(ctx context.Context) ([]ui.Row, bool, error) {
	hits, done, err := s.it.Next(ctx)
	rows := make([]ui.Row, len(hits))
	for i, h := range hits {
		size := "-" // not every Nexus version reports file sizes in search results
		if h.Size > 0 {
			size = ui.HumanSize(h.Size)
		}
		rows[i] = ui.Row{
			Cells: []string{h.Group + ":" + h.Artifact, h.Version, fmt.Sprint(h.Assets), size, ui.HumanAgo(h.Modified)},
			Raw:   h,
		}
	}
	return rows, done, err
}

func newSearch() *cobra.Command {
	var in module.SearchInput
	c := &cobra.Command{
		Use:   "search <repo> [terme]",
		Short: "Recherche des artifacts dans un repository",
		Example: `  nexus search rdsf-qp-snapshots qualitycontrol
  nexus search rdsf-qp-snapshots --group com.acme --artifact quality-core --from-version 1.2 --to-version 1.9
  nexus search rdsf-qp-snapshots --stream | grep 1.4`,
		Args:              cobra.RangeArgs(1, 2),
		ValidArgsFunction: aliasCompletion(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			m, t, err := resolve(args[0])
			if err != nil {
				if len(args) == 1 {
					err = fmt.Errorf("%w\n  → syntaxe : nexus search <repo> <terme> (ex. nexus search snap %s)", err, args[0])
				}
				return err
			}
			s, ok := m.(module.Searcher)
			if !ok {
				return module.Unsupported("search", t.Repo.Format)
			}
			if len(args) == 2 {
				in.Query = args[1]
			}
			it, err := s.Search(cmd.Context(), t, in)
			if err != nil {
				return err
			}
			return env.List(cmd.Context(), hitSource{it})
		},
	}
	f := c.Flags()
	f.StringVar(&in.Group, "group", "", "groupId (jokers * acceptés)")
	f.StringVar(&in.Artifact, "artifact", "", "artifactId (jokers * acceptés)")
	f.StringVar(&in.Version, "version", "", "version exacte ou avec jokers")
	f.StringVar(&in.FromVersion, "from-version", "", "version minimale (incluse)")
	f.StringVar(&in.ToVersion, "to-version", "", "version maximale (incluse)")
	f.StringVar(&in.Sort, "sort", "", "tri serveur : group|artifact|version")
	f.IntVar(&in.Limit, "limit", 0, "nombre maximal de résultats")
	return c
}
