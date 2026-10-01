package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
	"github.com/falcoer/nexus-toolbox/internal/secrets"
	"github.com/spf13/cobra"
)

func newInit() *cobra.Command {
	var user string
	c := &cobra.Command{
		Use:   "init <alias> <url-du-repository>",
		Short: "Enregistre un repository Nexus sous un alias",
		Long: `Enregistre un repository sous un alias, demande les identifiants (stockés dans le
trousseau du système) et récupère le format, le type et la politique du repository.

Le mot de passe peut être fourni par NEXUS_<ALIAS>_PASSWORD (alias en majuscules, - → _).`,
		Example: `  nexus init rdsf-qp-maven-snapshots https://nexus.example.com/nexus/repository/rdsf-qp-maven-snapshots/
  NEXUS_REL_PASSWORD=... nexus init rel https://nexus.example.com/repository/maven-releases/ --user ci`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias, rawURL := args[0], args[1]
			base, name, err := config.ParseRepoURL(rawURL)
			if err != nil {
				return err
			}
			if user == "" {
				user = os.Getenv(strings.NewReplacer("-", "_", ".", "_").Replace("NEXUS_" + strings.ToUpper(alias) + "_USER"))
			}
			if user == "" {
				if user, err = env.Line("Login :"); err != nil {
					return err
				}
			}
			pw := os.Getenv(strings.NewReplacer("-", "_", ".", "_").Replace("NEXUS_" + strings.ToUpper(alias) + "_PASSWORD"))
			if pw == "" {
				if pw, err = env.Secret("Mot de passe :"); err != nil {
					return err
				}
			}
			cl := nexus.New(base, user, pw)
			cl.Debug = func(f string, a ...any) { env.Verbosef(2, f, a...) }
			info, err := cl.Repository(cmd.Context(), name)
			if err != nil {
				if nexus.IsAuth(err) {
					return err
				}
				return fmt.Errorf("connexion à %s impossible : %w", base, err)
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			cfg.Repos[alias] = config.Repo{URL: rawURL, Base: base, Name: name, Format: info.Format, Type: info.Type,
				Policy: strings.ToUpper(info.Attributes.Maven.VersionPolicy), User: user}
			if err := cfg.Save(); err != nil {
				return err
			}
			insecure, err := secrets.Set(alias, pw)
			if err != nil {
				return err
			}
			env.Successf("%s enregistré : %s", env.Accent(alias), env.Muted(fmt.Sprintf("%s · %s · %s", info.Format, info.Type, orDash(strings.ToLower(info.Attributes.Maven.VersionPolicy)))))
			if insecure {
				env.Warnf("trousseau indisponible : mot de passe stocké dans ~/.nexus/credentials (droits 0600)")
			}
			return nil
		},
	}
	c.Flags().StringVarP(&user, "user", "u", "", "login (sinon demandé)")
	return c
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
