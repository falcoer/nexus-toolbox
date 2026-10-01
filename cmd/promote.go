package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

func newPromote() *cobra.Command {
	var in module.PromoteInput
	var dry bool
	c := &cobra.Command{
		Use:   "promote <repo-source> <repo-destination> <groupId:artifactId:version>",
		Short: "Promeut un artifact d'un repository vers un autre (ex. snapshot → release)",
		Long: `Copie tous les fichiers d'un artifact vers le repository de destination, vérifie les
sha1 côté destination, puis (avec --delete-source) supprime la source.

Nexus OSS n'a pas d'API de promotion : la copie est vérifiée fichier par fichier, et la
commande est reprenable (les fichiers déjà identiques sont ignorés).
Seule une version figée (non -SNAPSHOT) peut être promue.`,
		Example: `  nexus promote rdsf-qp-snapshots rdsf-qp-releases com.acme:quality-core:1.4.2 --dry-run
  nexus promote rdsf-qp-snapshots rdsf-qp-releases com.acme:quality-core:1.4.2 --delete-source`,
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: aliasCompletion(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[2], ":")
			if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
				return usageError{fmt.Errorf("coordonnées attendues sous la forme groupId:artifactId:version, reçu %q", args[2])}
			}
			in.Group, in.Artifact, in.Version = parts[0], parts[1], parts[2]
			sm, src, err := resolve(args[0])
			if err != nil {
				return err
			}
			_, dst, err := resolve(args[1])
			if err != nil {
				return err
			}
			p, ok := sm.(module.Promoter)
			if !ok {
				return module.Unsupported("promote", src.Repo.Format)
			}
			ctx := cmd.Context()
			plan, err := p.PlanPromote(ctx, src, dst, in)
			if err != nil {
				return err
			}
			printPlan(plan)
			if blocked := plan.Blocking(); len(blocked) > 0 {
				return fmt.Errorf("%d fichier(s) existent déjà dans %s avec un contenu différent (--force si la write policy le permet)", len(blocked), dst.Repo.Alias)
			}
			if dry {
				env.Infof("dry-run : rien n'a été modifié")
				return emitJSON(map[string]any{"schema": 1, "plan": plan})
			}
			ok, err = env.Confirm(confirmText(plan))
			if err != nil {
				return err
			}
			if !ok {
				env.Infof("abandon, rien n'a été modifié")
				return nil
			}
			res, err := p.ExecutePromote(ctx, src, dst, plan, &barReporter{})
			if res != nil {
				printResult(res)
				_ = emitJSON(map[string]any{"schema": 1, "plan": plan, "result": res})
			}
			if errors.Is(err, module.ErrPartial) {
				env.Failure("Promotion partielle", "certains fichiers n'ont pas pu être promus ou vérifiés",
					"relancez la même commande : les fichiers déjà identiques seront ignorés")
			}
			return err
		},
	}
	f := c.Flags()
	f.BoolVarP(&dry, "dry-run", "n", false, "affiche le plan sans rien modifier")
	f.BoolVar(&in.Force, "force", false, "écrase les fichiers existants (si la write policy l'autorise)")
	f.BoolVar(&in.DeleteSource, "delete-source", false, "supprime la source après copie vérifiée")
	return c
}

func confirmText(p *module.PromotePlan) string {
	s := fmt.Sprintf("Promouvoir %s:%s:%s vers %s", p.Group, p.Artifact, p.Version, p.Destination)
	if p.DeleteSource {
		s += " puis SUPPRIMER la source de " + p.Source
	}
	return s + " ?"
}

func emitJSON(v any) error {
	if env.Mode() != "json" {
		return nil
	}
	enc := json.NewEncoder(env.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printPlan(p *module.PromotePlan) {
	e := env
	fmt.Fprintf(e.Err, "%s %s:%s:%s  %s %s %s\n", e.IconInfo(), e.Accent(p.Group), e.Accent(p.Artifact), e.Accent(p.Version),
		p.Source, e.Arrow(), p.Destination)
	for _, w := range p.Warnings {
		fmt.Fprintf(e.Err, "  %s %s\n", e.IconWarn(), w)
	}
	var total int64
	for _, it := range p.Items {
		mark, name := e.Success("copier  "), it.Path
		switch it.Action {
		case "skip":
			mark = e.Muted("ignoré  ")
		case "conflict":
			mark = e.Error("conflit ")
		}
		line := fmt.Sprintf("  %s %s  %s", mark, name, e.Muted(ui.HumanSize(it.Size)))
		if it.Reason != "" {
			line += e.Muted("  (" + it.Reason + ")")
		}
		fmt.Fprintln(e.Err, line)
		if it.Action == "copy" {
			total += it.Size
		}
	}
	fmt.Fprintf(e.Err, "  %s\n", e.Muted(fmt.Sprintf("%d fichier(s), %s à transférer", len(p.Items), ui.HumanSize(total))))
}

func printResult(r *module.PromoteResult) {
	e := env
	for _, f := range r.Failed {
		fmt.Fprintf(e.Err, "  %s %s\n", e.IconErr(), f)
	}
	if len(r.Failed) == 0 {
		e.Successf("%d copié(s), %d ignoré(s), sha1 vérifiés%s", r.Copied, r.Skipped, map[bool]string{true: ", source supprimée"}[r.SourceDeleted])
	}
}

type barReporter struct{ bar *ui.Bar }

func (b *barReporter) AssetStart(p string, size int64) { b.bar = env.NewBar(p, size) }
func (b *barReporter) AssetBytes(n int64)              { b.bar.Add(n) }
func (b *barReporter) AssetDone(_ string, err error)   { b.bar.Done(err) }
