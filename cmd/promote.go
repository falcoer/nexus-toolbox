package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

func newPromote() *cobra.Command {
	var in module.PromoteInput
	var dry bool
	var pins []string
	c := &cobra.Command{
		Use:   "promote <repo-source> <repo-destination> <groupId:artifactId:version>",
		Short: "Promeut un artifact d'un repository vers un autre (ex. snapshot → release)",
		Long: `Copie tous les fichiers d'un artifact vers le repository de destination, vérifie les
sha1 côté destination, puis (avec --delete-source) supprime la source.

Nexus OSS n'a pas d'API de promotion : la copie est vérifiée fichier par fichier, et la
commande est reprenable (les fichiers déjà identiques sont ignorés).

Pour une version -SNAPSHOT, le build le plus récent est promu (ou celui désigné par --build) :
les fichiers sont renommés (ghc-web-1.2.3-20260914.070210-43.war → ghc-web-1.2.3.war) et le
<version> du pom est réécrit (1.2.3-SNAPSHOT → 1.2.3, ou --as-version). Les binaires restent
inchangés ; Nexus recalcule les empreintes. Les références -SNAPSHOT du pom (parent,
dépendances) bloquent la promotion : --pin groupId:artifactId=version les réécrit.
Un fichier ghc-web-1.2.3-promoted-from.txt trace la provenance (--no-marker pour le désactiver).`,
		Example: `  nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --dry-run
  nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --build 43 --as-version 03.27.10-1
  nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --pin com.acme:parent=1.0
  nexus promote snap rel com.acme:quality-core:1.4.2 --delete-source`,
		Args:              cobra.ExactArgs(3),
		ValidArgsFunction: aliasCompletion(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[2], ":")
			if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
				return usageError{fmt.Errorf("coordonnées attendues sous la forme groupId:artifactId:version, reçu %q", args[2])}
			}
			in.Group, in.Artifact, in.Version = parts[0], parts[1], parts[2]
			in.Tool = "nexus-toolbox " + version
			for _, p := range pins {
				ga, v, ok := strings.Cut(p, "=")
				g, a, ok2 := strings.Cut(ga, ":")
				if !ok || !ok2 || g == "" || a == "" || v == "" {
					return usageError{fmt.Errorf("--pin attend groupId:artifactId=version, reçu %q", p)}
				}
				in.Pins = append(in.Pins, module.Pin{Group: g, Artifact: a, Version: v})
			}
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
			if refs := plan.BlockingRefs(); len(refs) > 0 {
				return fmt.Errorf("le pom référence %d version(s) -SNAPSHOT (voir ci-dessus) : utilisez --pin groupId:artifactId=version pour les figer, ou --allow-snapshot-refs", len(refs))
			}
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
	f.StringVar(&in.AsVersion, "as-version", "", "version cible (défaut : version source sans -SNAPSHOT)")
	f.StringVar(&in.Build, "build", "", "build du snapshot à promouvoir : 43 ou 20260914.070210-43 (défaut : le plus récent)")
	f.StringArrayVar(&pins, "pin", nil, "réécrit une référence -SNAPSHOT du pom : groupId:artifactId=version (répétable)")
	f.BoolVar(&in.AllowSnapshotRefs, "allow-snapshot-refs", false, "autorise les références -SNAPSHOT restantes dans le pom")
	f.BoolVar(&in.NoMarker, "no-marker", false, "n'ajoute pas le fichier -promoted-from.txt")
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
	fmt.Fprintf(e.Err, "%s %s:%s  %s %s %s\n", e.IconInfo(), e.Accent(p.Group), e.Accent(p.Artifact), p.Source, e.Arrow(), p.Destination)
	ver := p.SourceVersion
	if p.SourceBuild != "" {
		ver += " (build " + p.SourceBuild + ")"
	}
	fmt.Fprintf(e.Err, "  version : %s %s %s\n", ver, e.Arrow(), e.Accent(p.TargetVersion))
	if len(p.Builds) > 1 {
		var bs []string
		for _, b := range p.Builds {
			if b == p.SourceBuild {
				b = e.Success(b + " ✔")
			}
			bs = append(bs, b)
		}
		fmt.Fprintf(e.Err, "  builds  : %s\n", strings.Join(bs, ", "))
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(e.Err, "  %s %s\n", e.IconWarn(), w)
	}
	var total int64
	for _, it := range p.Items {
		mark := e.Success("copier  ")
		switch it.Action {
		case "skip":
			mark = e.Muted("ignoré  ")
		case "conflict":
			mark = e.Error("conflit ")
		}
		name := it.Path
		if it.Kind == module.KindFile && it.SourcePath != "" && path.Base(it.SourcePath) != path.Base(it.Path) {
			name = path.Base(it.SourcePath) + " " + e.Arrow() + " " + path.Base(it.Path)
		} else {
			name = path.Base(it.Path)
		}
		if it.Kind == module.KindMarker {
			name += e.Muted(" (traçabilité)")
		}
		line := fmt.Sprintf("  %s %s", mark, name)
		if it.Size > 0 {
			line += "  " + e.Muted(ui.HumanSize(it.Size))
		}
		if it.Reason != "" {
			line += e.Muted("  (" + it.Reason + ")")
		}
		fmt.Fprintln(e.Err, line)
		for _, d := range it.Diff {
			fmt.Fprintf(e.Err, "             %s %s\n", e.Info("pom"), d)
		}
		if it.Action == "copy" {
			total += it.Size
		}
	}
	for _, r := range p.SnapshotRefs {
		icon := e.IconErr()
		if p.AllowSnapshotRefs {
			icon = e.IconWarn()
		}
		fmt.Fprintf(e.Err, "  %s référence SNAPSHOT restante : %s\n", icon, r)
	}
	fmt.Fprintf(e.Err, "  %s\n", e.Muted(fmt.Sprintf("%d fichier(s), %s à transférer", len(p.Items), ui.HumanSize(total))))
}

func printResult(r *module.PromoteResult) {
	e := env
	for _, f := range r.Failed {
		fmt.Fprintf(e.Err, "  %s %s\n", e.IconErr(), f)
	}
	for _, w := range r.Warnings {
		fmt.Fprintf(e.Err, "  %s %s\n", e.IconWarn(), w)
	}
	if len(r.Failed) == 0 {
		extra := ""
		if r.MarkerWritten {
			extra += ", traçabilité ajoutée"
		}
		if r.SourceDeleted {
			extra += ", source supprimée"
		}
		e.Successf("%d copié(s), %d ignoré(s), sha1 vérifiés%s", r.Copied, r.Skipped, extra)
	}
}

type barReporter struct{ bar *ui.Bar }

func (b *barReporter) AssetStart(p string, size int64) { b.bar = env.NewBar(p, size) }
func (b *barReporter) AssetBytes(n int64)              { b.bar.Add(n) }
func (b *barReporter) AssetDone(_ string, err error)   { b.bar.Done(err) }
