package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

func newInfo() *cobra.Command {
	var in module.InspectInput
	var links bool
	c := &cobra.Command{
		Use:     "info <repo> <groupId:artifactId[:version]>",
		Aliases: []string{"show"},
		Short:   "Détails d'un artifact et liens de téléchargement direct",
		Long: `Sans version : liste les versions de l'artifact (les builds d'un snapshot sont regroupés).
Avec une version : coordonnées, date de publication, taille, empreintes, puis un lien de
téléchargement direct par fichier. Pour un -SNAPSHOT, le build le plus récent est décrit
(--build pour un autre) et la liste des builds est donnée.

Les liens sont écrits en clair, un par ligne : cliquables avec Ctrl+clic dans Windows Terminal,
VS Code, iTerm2 et la plupart des terminaux. Ils exigent vos identifiants si le repository
n'est pas public.`,
		Example: `  nexus info rel com.acme:flux-editor
  nexus info rel com.acme:flux-editor:18.00.00.beta1-0
  nexus info snap com.acme:flux-editor:18.00.00-0-SNAPSHOT --build 1
  nexus info rel com.acme:flux-editor:18.00.00.beta1-0 --links
  nexus info rel com.acme:flux-editor:18.00.00.beta1-0 -o json`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: aliasCompletion(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			parts := strings.Split(args[1], ":")
			if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" || (len(parts) == 3 && parts[2] == "") {
				return usageError{fmt.Errorf("coordonnées attendues sous la forme groupId:artifactId[:version], reçu %q", args[1])}
			}
			in.Group, in.Artifact = parts[0], parts[1]
			if len(parts) == 3 {
				in.Version = parts[2]
			}
			m, t, err := resolve(args[0])
			if err != nil {
				return err
			}
			insp, ok := m.(module.Inspector)
			if !ok {
				return module.Unsupported("info", t.Repo.Format)
			}
			if in.Version == "" {
				s, err := insp.Versions(cmd.Context(), t, in.Group, in.Artifact)
				if err != nil {
					return err
				}
				return renderVersions(cmd, s, args[0])
			}
			d, err := insp.Inspect(cmd.Context(), t, in)
			if err != nil {
				return err
			}
			return renderDetails(cmd, d, links)
		},
	}
	f := c.Flags()
	f.StringVar(&in.Build, "build", "", "build du snapshot à décrire : 43 ou 20260914.070210-43 (défaut : le plus récent)")
	f.BoolVar(&in.All, "all", false, "inclut les fichiers .sha1/.md5 et maven-metadata")
	f.BoolVar(&links, "links", false, "n'affiche que les liens de téléchargement, un par ligne")
	return c
}

func renderVersions(cmd *cobra.Command, s *module.ArtifactSummary, alias string) error {
	e := env
	// newest first
	rows := make([]ui.Row, 0, len(s.Versions))
	for i := len(s.Versions) - 1; i >= 0; i-- {
		v := s.Versions[i]
		builds := "-"
		if v.Builds > 0 {
			builds = fmt.Sprint(v.Builds)
		}
		rows = append(rows, ui.Row{Cells: []string{v.Version, v.Kind, builds, fmt.Sprint(v.Files), ui.HumanAgo(v.Modified)}, Raw: v})
	}
	if e.Mode() == "json" {
		return emitJSONTo(map[string]any{"schema": 1, "artifact": s})
	}
	if !e.Machine() {
		fmt.Fprintf(e.Out, "%s\n", e.Heading(s.Group+":"+s.Artifact))
		field(e, "Dépôt", s.Repository)
		field(e, "Versions", fmt.Sprintf("%d", len(s.Versions)))
		field(e, "Répertoire", s.DirectoryURL)
		field(e, "Interface", s.BrowseURL)
		fmt.Fprintln(e.Out)
	}
	src := &ui.SliceSource{Cols: []ui.Column{
		{Title: "version", Style: (*ui.Env).Accent},
		{Title: "type", Priority: 3},
		{Title: "builds", Right: true, Priority: 2},
		{Title: "files", Right: true, Priority: 3},
		{Title: "modifié", Priority: 1, Style: (*ui.Env).Muted},
	}, Rows: rows}
	if err := e.List(cmd.Context(), src); err != nil {
		return err
	}
	if !e.Machine() {
		fmt.Fprintf(e.Err, "%s\n", e.Muted(fmt.Sprintf("détail et liens : nexus info %s %s:%s:<version>", alias, s.Group, s.Artifact)))
	}
	return nil
}

func renderDetails(cmd *cobra.Command, d *module.ComponentDetails, linksOnly bool) error {
	e := env
	switch {
	case e.Mode() == "json":
		return emitJSONTo(map[string]any{"schema": 1, "component": d})
	case linksOnly:
		for _, f := range d.Files {
			fmt.Fprintln(e.Out, f.URL)
		}
		return nil
	case e.Mode() == "plain" || e.Mode() == "ndjson":
		for _, f := range d.Files {
			if e.Mode() == "ndjson" {
				b, _ := json.Marshal(f)
				fmt.Fprintln(e.Out, string(b))
				continue
			}
			fmt.Fprintf(e.Out, "%s\t%d\t%s\t%s\n", f.Name, f.Size, f.SHA1, f.URL)
		}
		return nil
	}

	fmt.Fprintf(e.Out, "%s %s\n", e.Heading(d.Artifact), e.Accent(d.Version))
	field(e, "Artifact", d.Group+":"+d.Artifact)
	repo := d.Repository
	if d.Format != "" {
		repo += e.Muted(fmt.Sprintf("  (%s%s)", d.Format, map[bool]string{true: " · " + d.Type}[d.Type != ""]))
	}
	field(e, "Dépôt", repo)
	typ := d.Kind
	if d.Kind == "snapshot" {
		typ = fmt.Sprintf("snapshot · build %s", d.Build)
	}
	field(e, "Type", typ)
	if len(d.Builds) > 1 {
		shown := d.Builds
		more := ""
		if len(shown) > 6 {
			more = fmt.Sprintf(" … (+%d)", len(shown)-6)
			shown = shown[len(shown)-6:]
		}
		field(e, "Builds", strings.Join(shown, ", ")+more)
	}
	if !d.Published.IsZero() {
		pub := d.Published.Local().Format("2006-01-02 15:04") + e.Muted(" ("+ui.HumanAgo(d.Published)+")")
		if d.Uploader != "" {
			pub += " par " + d.Uploader
		}
		field(e, "Publié", pub)
	}
	size := fmt.Sprintf("%d fichier(s)", len(d.Files))
	if d.TotalSize > 0 {
		size = ui.HumanSize(d.TotalSize) + " · " + size
	}
	field(e, "Taille", size)
	field(e, "Répertoire", d.DirectoryURL)
	field(e, "Interface", d.BrowseURL)
	fmt.Fprintln(e.Out)

	rows := make([]ui.Row, len(d.Files))
	for i, f := range d.Files {
		size := "-"
		if f.Size > 0 {
			size = ui.HumanSize(f.Size)
		}
		sha := f.SHA1
		if len(sha) > 12 {
			sha = sha[:12] + e.Ellipsis()
		}
		rows[i] = ui.Row{Cells: []string{f.Name, size, sha, ui.HumanAgo(f.Modified)}, Raw: f}
	}
	if err := e.List(cmd.Context(), &ui.SliceSource{Cols: []ui.Column{
		{Title: "fichier", Style: (*ui.Env).Accent},
		{Title: "taille", Right: true},
		{Title: "sha1", Priority: 2, Style: (*ui.Env).Muted},
		{Title: "modifié", Priority: 1, Style: (*ui.Env).Muted},
	}, Rows: rows}); err != nil {
		return err
	}

	fmt.Fprintf(e.Out, "\n%s\n", e.Heading("Téléchargement direct"))
	for _, f := range d.Files {
		fmt.Fprintf(e.Out, "  %s\n", f.URL)
	}
	return nil
}

func field(e *ui.Env, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(e.Out, "  %s %s\n", e.Muted(fmt.Sprintf("%-11s", label)), value)
}

func emitJSONTo(v any) error {
	enc := json.NewEncoder(env.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
