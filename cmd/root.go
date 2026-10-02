// Package cmd wires the cobra commands to the modules and the terminal layer.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/falcoer/nexus-toolbox/internal/formats/maven"
	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
	"github.com/falcoer/nexus-toolbox/internal/secrets"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

// Build information, set with -ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Exit codes (docs/CLI-UX-GUIDELINES.md §10).
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitPartial     = 3
	exitAuth        = 4
	exitPlanNeeded  = 5
	exitInterrupted = 130
)

var (
	flags    ui.Flags
	env      *ui.Env
	registry = module.NewRegistry(maven.New())

	outOverride, errOverride io.Writer // set by tests only
	inOverride               *os.File  // set by tests only: scripted answers for the prompts
)

type usageError struct{ error }

func newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "nexus",
		Short:         "Client en ligne de commande pour Nexus Repository",
		Long:          "nexus : recherche, promotion, analyse et nettoyage de repositories Nexus 3.\nLes dépôts sont enregistrés avec `nexus init <alias> <url>` puis désignés par leur alias.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       fmt.Sprintf("%s (commit %s, %s)", version, commit, date),
		PersistentPreRun: func(*cobra.Command, []string) {
			env = ui.Detect(flags)
			if outOverride != nil { // tests
				env.Out, env.Err, env.OutTTY, env.InTTY = outOverride, errOverride, false, false
			}
			if inOverride != nil {
				env.In, env.InTTY = inOverride, true
			}
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageError{err} })
	pf := root.PersistentFlags()
	pf.StringVarP(&flags.Output, "output", "o", "", "format de sortie : table|plain|json|ndjson (défaut : table sur TTY, plain sinon)")
	pf.BoolVar(&flags.NoColor, "no-color", false, "désactive les couleurs")
	pf.BoolVar(&flags.ASCII, "ascii", false, "icônes et barres en ASCII")
	pf.BoolVar(&flags.NoPager, "no-pager", false, "n'utilise pas le pager interactif")
	pf.BoolVar(&flags.Stream, "stream", false, "mode flux : une ligne par résultat, au fil de l'eau")
	pf.BoolVarP(&flags.Yes, "yes", "y", false, "répond oui aux confirmations")
	pf.CountVarP(&flags.Verbose, "verbose", "v", "détails (-vv : requêtes HTTP)")
	root.AddCommand(newInit(), newRepos(), newSearch(), newInfo(), newDownload(), newPromote())
	return root
}

// Execute runs the CLI and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := newRoot().ExecuteContext(ctx)
	if err == nil {
		return exitOK
	}
	if env == nil {
		env = ui.Detect(flags)
	}
	return report(ctx, err)
}

func report(ctx context.Context, err error) int {
	var uerr usageError
	var planNeeded planRequiredError
	switch {
	case errors.Is(err, context.Canceled) || ctx.Err() != nil:
		fmt.Fprintln(env.Err, "\ninterrompu")
		return exitInterrupted
	case errors.As(err, &uerr):
		env.Failure("Utilisation incorrecte", uerr.Error(), "voir `nexus --help`")
		return exitUsage
	case errors.As(err, &planNeeded):
		env.Failure("Plan de promotion à valider", planNeeded.why, planNeeded.hint)
		return exitPlanNeeded
	case errors.Is(err, module.ErrPartial):
		return exitPartial
	case nexus.IsAuth(err):
		env.Failure("Accès refusé par Nexus", err.Error(), "vérifiez vos identifiants (`nexus init`) et vos droits sur le repository")
		return exitAuth
	case strings.HasPrefix(err.Error(), "unknown command"), strings.HasPrefix(err.Error(), "accepts "), strings.HasPrefix(err.Error(), "requires "):
		env.Failure("Utilisation incorrecte", err.Error(), "voir `nexus --help`")
		return exitUsage
	}
	env.Failure(err.Error(), "", "")
	return exitError
}

// resolve returns the module and an authenticated client for a configured alias.
func resolve(alias string) (module.Module, module.Target, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, module.Target{}, err
	}
	repo, err := cfg.Get(alias)
	if err != nil {
		return nil, module.Target{}, err
	}
	creds, err := secrets.Get(alias, repo.User)
	if err != nil {
		return nil, module.Target{}, err
	}
	c := nexus.New(repo.Base, creds.User, creds.Password)
	c.Debug = func(f string, a ...any) { env.Verbosef(2, f, a...) }
	m, err := registry.For(repo.Format)
	if err != nil {
		return nil, module.Target{}, err
	}
	return m, module.Target{Repo: repo, Client: c}, nil
}

// aliasCompletion completes configured repository aliases.
func aliasCompletion(max int) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) >= max {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		cfg, err := config.Load()
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return cfg.Aliases(), cobra.ShellCompDirectiveNoFileComp
	}
}
