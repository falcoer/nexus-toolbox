package cmd

import (
	"fmt"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/secrets"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

func newRepos() *cobra.Command {
	c := &cobra.Command{Use: "repos", Short: "Gère les repositories enregistrés"}
	c.AddCommand(
		&cobra.Command{
			Use: "list", Short: "Liste les repositories enregistrés", Args: cobra.NoArgs,
			RunE: func(cmd *cobra.Command, _ []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				src := &ui.SliceSource{Cols: []ui.Column{
					{Title: "alias", Style: (*ui.Env).Accent},
					{Title: "format"}, {Title: "type", Priority: 2}, {Title: "policy", Priority: 3},
					{Title: "actions", Priority: 4}, {Title: "user", Priority: 5, Style: (*ui.Env).Muted},
					{Title: "url", Priority: 6, Style: (*ui.Env).Muted},
				}}
				for _, a := range cfg.Aliases() {
					r := cfg.Repos[a]
					acts := ""
					if m, err := registry.For(r.Format); err == nil {
						acts = fmt.Sprint(module.Capabilities(m))
					}
					src.Rows = append(src.Rows, ui.Row{
						Cells: []string{a, r.Format, r.Type, orDash(r.Policy), acts, r.User, r.URL},
						Raw:   map[string]any{"alias": a, "format": r.Format, "type": r.Type, "policy": r.Policy, "user": r.User, "url": r.URL, "repository": r.Name},
					})
				}
				return env.List(cmd.Context(), src)
			},
		},
		&cobra.Command{
			Use: "link <repo-snapshot> <repo-release>", Short: "Définit la paire source/destination par défaut de `nexus promote`", Args: cobra.ExactArgs(2),
			ValidArgsFunction: aliasCompletion(2),
			RunE: func(_ *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				for _, a := range args {
					if _, err := cfg.Get(a); err != nil {
						return err
					}
				}
				cfg.Promotion = config.Promotion{From: args[0], To: args[1]}
				if err := cfg.Save(); err != nil {
					return err
				}
				env.Successf("promotion par défaut : %s %s %s", env.Accent(args[0]), env.Arrow(), env.Accent(args[1]))
				return nil
			},
		},
		&cobra.Command{
			Use: "remove <alias>", Aliases: []string{"rm"}, Short: "Oublie un repository et son mot de passe", Args: cobra.ExactArgs(1),
			ValidArgsFunction: aliasCompletion(1),
			RunE: func(_ *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				if _, err := cfg.Get(args[0]); err != nil {
					return err
				}
				ok, err := env.Confirm(fmt.Sprintf("Supprimer %s de la configuration ?", args[0]))
				if err != nil || !ok {
					return err
				}
				delete(cfg.Repos, args[0])
				secrets.Delete(args[0])
				if err := cfg.Save(); err != nil {
					return err
				}
				env.Successf("%s supprimé", env.Accent(args[0]))
				return nil
			},
		},
	)
	return c
}
