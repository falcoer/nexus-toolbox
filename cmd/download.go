package cmd

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

func newDownload() *cobra.Command {
	var in module.InspectInput
	var dir string
	var include, exclude []string
	var force, dry bool
	c := &cobra.Command{
		Use:   "download <repo> <groupId:artifactId:version>",
		Short: "Télécharge les fichiers d'un artifact",
		Long: `Télécharge les fichiers d'une version (ceux que montre ` + "`nexus info`" + `) dans un dossier.

Chaque fichier est d'abord écrit en .part, comparé au sha1 donné par Nexus, puis renommé :
un transfert interrompu ou corrompu ne laisse jamais un fichier qui paraît complet. Un fichier
déjà présent et identique est ignoré ; un fichier différent est refusé sauf --force.
Pour un -SNAPSHOT, le build le plus récent est téléchargé (--build pour un autre).
Les chemins des fichiers téléchargés sont écrits sur la sortie standard, un par ligne.`,
		Example: `  nexus download rel com.acme:flux-editor:18.00.00.beta1-0
  nexus download rel com.acme:flux-editor:18.00.00.beta1-0 --dir .\dist --include "*.zip"
  nexus download snap com.acme:flux-editor:18.00.00-0-SNAPSHOT --build 1 --dry-run`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: aliasCompletion(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[1], ":")
			if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
				return usageError{fmt.Errorf("coordonnées attendues sous la forme groupId:artifactId:version, reçu %q (`nexus info` liste les versions)", args[1])}
			}
			in.Group, in.Artifact, in.Version = parts[0], parts[1], parts[2]
			for _, g := range append(append([]string{}, include...), exclude...) {
				if _, err := path.Match(g, "x"); err != nil {
					return usageError{fmt.Errorf("motif invalide %q : %w", g, err)}
				}
			}
			m, t, err := resolve(args[0])
			if err != nil {
				return err
			}
			insp, ok := m.(module.Inspector)
			if !ok {
				return module.Unsupported("download", t.Repo.Format)
			}
			d, err := insp.Inspect(cmd.Context(), t, in)
			if err != nil {
				return err
			}
			files := filterFiles(d.Files, include, exclude)
			if len(files) == 0 {
				return fmt.Errorf("aucun fichier ne correspond (%d disponible(s) : %s)", len(d.Files), fileNames(d.Files))
			}
			label := d.Artifact + " " + d.Version
			if d.Build != "" {
				label += " (build " + d.Build + ")"
			}
			if dry {
				env.Infof("dry-run : %s → %s", label, env.Accent(dir))
				for _, f := range files {
					size := ""
					if f.Size > 0 {
						size = "  " + env.Muted(ui.HumanSize(f.Size))
					}
					fmt.Fprintf(env.Err, "  %s %s%s\n", env.Arrow(), f.Name, size)
				}
				return emitJSON(map[string]any{"schema": 1, "dry_run": true, "files": files})
			}
			env.Infof("%s → %s", label, env.Accent(dir))
			res, err := module.Download(cmd.Context(), t, files, module.DownloadOptions{Dir: dir, Force: force}, &barReporter{})
			if res != nil {
				for _, f := range res.Files {
					switch f.Status {
					case "skipped":
						fmt.Fprintf(env.Err, "  %s %s %s\n", env.IconInfo(), f.Name, env.Muted("déjà présent et identique"))
					case "failed":
						fmt.Fprintf(env.Err, "  %s %s : %s\n", env.IconErr(), f.Name, f.Error)
					}
					if f.Status != "failed" && env.Mode() != "json" {
						fmt.Fprintln(env.Out, f.Path)
					}
				}
				if res.Failed == 0 {
					env.Successf("%d téléchargé(s) (%s), %d déjà présent(s) dans %s", res.Downloaded, ui.HumanSize(res.Bytes), res.Skipped, res.Dir)
				}
				_ = emitJSON(map[string]any{"schema": 1, "result": res})
			}
			if errors.Is(err, module.ErrPartial) {
				env.Failure("Téléchargement partiel", "certains fichiers n'ont pas pu être récupérés",
					"relancer la même commande reprend là où elle s'est arrêtée : les fichiers identiques sont ignorés")
			}
			return err
		},
	}
	f := c.Flags()
	f.StringVarP(&dir, "dir", "d", ".", "dossier de destination (créé si besoin)")
	f.StringArrayVar(&include, "include", nil, "ne télécharge que les fichiers dont le nom correspond (motif, ex. \"*.zip\" ; répétable)")
	f.StringArrayVar(&exclude, "exclude", nil, "exclut les fichiers dont le nom correspond (motif ; répétable)")
	f.StringVar(&in.Build, "build", "", "build du snapshot : 43 ou 20260914.070210-43 (défaut : le plus récent)")
	f.BoolVar(&in.All, "all", false, "inclut aussi les fichiers .sha1/.md5 et maven-metadata")
	f.BoolVar(&force, "force", false, "remplace un fichier local existant au contenu différent")
	f.BoolVarP(&dry, "dry-run", "n", false, "liste ce qui serait téléchargé sans rien écrire")
	return c
}

func filterFiles(files []module.FileDetail, include, exclude []string) []module.FileDetail {
	match := func(globs []string, name string) bool {
		for _, g := range globs {
			if ok, _ := path.Match(g, name); ok {
				return true
			}
		}
		return false
	}
	var out []module.FileDetail
	for _, f := range files {
		if len(include) > 0 && !match(include, f.Name) {
			continue
		}
		if match(exclude, f.Name) {
			continue
		}
		out = append(out, f)
	}
	return out
}

func fileNames(files []module.FileDetail) string {
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}
