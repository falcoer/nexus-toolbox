package cmd

import (
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

func newPromote() *cobra.Command {
	var (
		in             module.PromoteInput
		dry, deps      bool
		pins, setProps []string
		from, to       string
		fromVersion    string
	)
	c := &cobra.Command{
		Use:   "promote [artifact [version-cible]]",
		Short: "Promeut un artifact snapshot vers la release, avec les modules dont il dépend",
		Long: `Publie dans le repository release un artifact (et les modules dont il dépend) à partir
des builds du repository snapshot, en vérifiant chaque fichier.

  nexus promote                              assistant interactif
  nexus promote <artifact> [version-cible]   forme courte

Règles appliquées automatiquement :
  1. dépôts      la paire snapshot → release est déduite des repos configurés
                 (--from / --to pour en choisir d'autres, ` + "`nexus repos link`" + ` pour la fixer)
  2. artifact    artifactId seul (ou groupId:artifactId) ; le groupe est retrouvé par recherche
  3. source      la version snapshot la plus récente et son build le plus récent
                 (--from-version X, --build N pour en choisir un autre)
  4. cible       le 2e argument ; à défaut la version source sans -SNAPSHOT

Modules requis (parent, dépendances, BOM, librairies désignées par des propriétés) :
  un module du même reactor suit la version cible ; les autres prennent leur version sans
  -SNAPSHOT. Chacun est « en release » (rien à faire), « à promouvoir » ou « bloquant »
  (introuvable : rien n'est publié, pour ne jamais laisser une release incohérente).
  S'il y a des modules à promouvoir, le plan est affiché puis soumis à confirmation ;
  sans terminal, la commande s'arrête avec le code 5 : relancez avec --with-deps.

Nexus OSS n'a pas d'API de promotion : la copie est vérifiée fichier par fichier et reprenable.`,
		Example: `  nexus promote flux-editor 18.00.00.beta1-0
  nexus promote flux-editor 18.00.00.beta1-0 --dry-run
  nexus promote flux-editor 18.00.00.beta1-0 --build 3 --with-deps
  nexus promote snap rel com.acme:ghc-web:03.27.10-0-SNAPSHOT --dry-run        (forme complète)`,
		Args:              cobra.MaximumNArgs(3),
		ValidArgsFunction: aliasCompletion(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := parseAdvanced(&in, pins, setProps); err != nil {
				return err
			}
			in.Tool = "nexus-toolbox " + version
			req := &promoteReq{in: in, deps: deps, dry: dry, from: from, to: to, fromVersion: fromVersion, pins: pins, setProps: setProps}
			switch {
			case len(args) == 0:
				return runWizard(cmd, req)
			case len(args) == 3 && strings.Count(args[2], ":") == 2:
				return runLegacy(cmd, req, args)
			case len(args) == 3:
				return usageError{fmt.Errorf("forme complète : promote <repo-source> <repo-destination> <groupId:artifactId:version> ; forme courte : promote <artifact> [version-cible]")}
			}
			return runShort(cmd, req, args)
		},
	}
	f := c.Flags()
	f.BoolVarP(&dry, "dry-run", "n", false, "affiche le plan sans rien modifier")
	f.StringVar(&from, "from", "", "repository source (alias) ; défaut : le repo snapshot configuré")
	f.StringVar(&to, "to", "", "repository destination (alias) ; défaut : le repo release configuré")
	f.StringVar(&fromVersion, "from-version", "", "version snapshot source (défaut : la plus récente)")
	f.StringVar(&in.Build, "build", "", "build du snapshot : 3 ou 20260914.070210-3 (défaut : le plus récent)")
	f.BoolVar(&deps, "with-deps", false, "accepte de publier aussi les modules requis (obligatoire sans terminal)")
	f.BoolVar(&deps, "with-parent", false, "ancien nom de --with-deps")
	f.BoolVar(&in.Force, "force", false, "écrase les fichiers existants (si la write policy l'autorise)")
	f.StringVar(&in.AsVersion, "as-version", "", "version cible (forme complète ; sinon 2e argument)")
	f.StringArrayVar(&pins, "pin", nil, "avancé : impose la version d'un parent/une dépendance, groupId:artifactId=version (répétable)")
	f.StringArrayVar(&setProps, "set-property", nil, "avancé : impose la valeur d'une propriété du pom, nom=valeur (répétable)")
	f.BoolVar(&in.AlignProperties, "align-properties", false, "avancé : reprend les propriétés du pom déjà publié en release à la version cible")
	f.BoolVar(&in.AllowSnapshotRefs, "allow-snapshot-refs", false, "avancé : autorise les références -SNAPSHOT restantes dans le pom")
	f.BoolVar(&in.ReleaseProperties, "release-properties", false, "ancienne option (les propriétés sont résolues par le plan)")
	f.BoolVar(&in.NoMarker, "no-marker", false, "n'ajoute pas le fichier de traçabilité -promoted-from-<version d'origine>.txt")
	f.BoolVar(&in.DeleteSource, "delete-source", false, "supprime la source après copie vérifiée (artifact racine seulement)")
	_ = f.MarkHidden("with-parent")
	_ = f.MarkHidden("release-properties")
	return c
}

func parseAdvanced(in *module.PromoteInput, pins, setProps []string) error {
	for _, sp := range setProps {
		name, val, ok := strings.Cut(sp, "=")
		if !ok || name == "" || val == "" {
			return usageError{fmt.Errorf("--set-property attend nom=valeur, reçu %q", sp)}
		}
		if in.SetProperties == nil {
			in.SetProperties = map[string]string{}
		}
		in.SetProperties[name] = val
	}
	for _, p := range pins {
		ga, v, ok := strings.Cut(p, "=")
		g, a, ok2 := strings.Cut(ga, ":")
		if !ok || !ok2 || g == "" || a == "" || v == "" {
			return usageError{fmt.Errorf("--pin attend groupId:artifactId=version, reçu %q", p)}
		}
		in.Pins = append(in.Pins, module.Pin{Group: g, Artifact: a, Version: v})
	}
	return nil
}

// blockingProblems lists every reason that forbids the promotion (conflicts, SNAPSHOT references).
func blockingProblems(p *module.PromotePlan, dst string) []string {
	var out []string
	if refs := p.BlockingRefs(); len(refs) > 0 {
		out = append(out, fmt.Sprintf("le pom référence %d version(s) -SNAPSHOT (voir ci-dessus)\n  %s --pin groupId:artifactId=version fige un parent/une dépendance ; --set-property nom=valeur ou --release-properties fixent les propriétés ; --align-properties reprend celles de la release existante ; --allow-snapshot-refs passe outre", len(refs), env.Arrow()))
	}
	if n := len(p.Blocking()); n > 0 {
		out = append(out, fmt.Sprintf("%d fichier(s) existent déjà dans %s avec un contenu différent\n  %s %s", n, dst, env.Arrow(), conflictHint(p, dst)))
	}
	return out
}

func conflictHint(p *module.PromotePlan, dst string) string {
	if p.WritePolicy == "ALLOW_ONCE" {
		return "la write policy ALLOW_ONCE interdit l'écrasement : supprimez cette version dans le repo " + dst + " (UI Nexus) ou choisissez --as-version"
	}
	return "--force écrasera les fichiers existants"
}

func confirmText(p *module.PromotePlan) string {
	s := fmt.Sprintf("Promouvoir %s:%s:%s vers %s", p.Group, p.Artifact, p.Version, p.Destination)
	if n := len(p.Parents); n > 0 {
		s += fmt.Sprintf(" (avec %d parent(s))", n)
	}
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
	if len(p.Parents) > 3 && env.Flags.Verbose == 0 {
		// long chains: the plan table above says it all; per-file details only on request
		fmt.Fprintf(env.Err, "%s %d modules requis : détail fichier par fichier avec -v\n", env.IconInfo(), len(p.Parents))
		fmt.Fprintln(env.Err, env.Muted("── artifact principal ──"))
		printOnePlan(p)
		printSummary(p)
		return
	}
	for i, par := range p.Parents {
		fmt.Fprintln(env.Err, env.Muted(fmt.Sprintf("── module requis %d/%d (promu avant l'artifact) ──", i+1, len(p.Parents))))
		printOnePlan(par)
	}
	if len(p.Parents) > 0 {
		fmt.Fprintln(env.Err, env.Muted("── artifact principal ──"))
	}
	printOnePlan(p)
	printSummary(p)
}

// printSummary states what the plan means overall (existing release, nothing to do).
func printSummary(p *module.PromotePlan) {
	conflicts, copies := 0, 0
	for _, pl := range append(append([]*module.PromotePlan{}, p.Parents...), p) {
		for _, it := range pl.Items {
			switch {
			case it.Action == "conflict":
				conflicts++
			case it.Action == "copy" && it.Kind == module.KindFile, it.MissingChecksums:
				copies++
			}
		}
	}
	if conflicts > 0 {
		fmt.Fprintf(env.Err, "%s %d fichier(s) en conflit : la version %s existe déjà dans %s\n", env.IconWarn(), conflicts, env.Accent(p.TargetVersion), p.Destination)
		fmt.Fprintf(env.Err, "  %s %s\n", env.Arrow(), conflictHint(p, p.Destination))
	}
	if conflicts == 0 && copies == 0 {
		fmt.Fprintf(env.Err, "%s rien à promouvoir : tous les fichiers sont déjà présents et identiques\n", env.IconInfo())
	}
}

func printOnePlan(p *module.PromotePlan) {
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
			if it.MissingChecksums {
				mark = e.Info("compléter")
			}
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
		if it.Action == "conflict" {
			if it.RemoteSize > 0 || !it.RemoteModified.IsZero() {
				d := "destination :"
				if it.RemoteSize > 0 {
					d += " " + ui.HumanSize(it.RemoteSize)
				}
				if !it.RemoteModified.IsZero() {
					d += ", publié le " + it.RemoteModified.Local().Format("2006-01-02 15:04")
				}
				if it.Size > 0 {
					d += " — source : " + ui.HumanSize(it.Size)
				}
				fmt.Fprintf(e.Err, "             %s\n", e.Muted(d))
			}
			for _, l := range it.DiffLines {
				switch {
				case strings.HasPrefix(l, "- "):
					l = e.Error(l)
				case strings.HasPrefix(l, "+ "):
					l = e.Success(l)
				default:
					l = e.Muted(l)
				}
				fmt.Fprintf(e.Err, "             %s\n", l)
			}
		}
		if it.Action == "copy" {
			total += it.Size
		}
	}
	for _, n := range p.Notes {
		fmt.Fprintf(e.Err, "  %s %s\n", e.IconInfo(), n)
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
		if r.ChecksumsAdded > 0 {
			extra += fmt.Sprintf(", empreintes .sha1/.md5 ajoutées à %d fichier(s)", r.ChecksumsAdded)
		}
		e.Successf("%d copié(s), %d ignoré(s), sha1 vérifiés%s", r.Copied, r.Skipped, extra)
	}
}

type barReporter struct{ bar *ui.Bar }

func (b *barReporter) AssetStart(p string, size int64) { b.bar = env.NewBar(p, size) }
func (b *barReporter) AssetBytes(n int64)              { b.bar.Add(n) }
func (b *barReporter) AssetDone(_ string, err error)   { b.bar.Done(err) }
