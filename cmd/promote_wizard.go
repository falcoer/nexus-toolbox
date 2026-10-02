package cmd

import (
	"fmt"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/ui"
	"github.com/spf13/cobra"
)

// runWizard guides a promotion step by step, then hands over to runPromote, which shows the plan,
// asks for confirmation and finally prints the short command that redoes it.
func runWizard(cmd *cobra.Command, req *promoteReq) error {
	if !env.InTTY {
		return usageError{fmt.Errorf("l'assistant exige un terminal interactif : utilisez `nexus promote <artifact> [version-cible]`")}
	}
	ctx := cmd.Context()
	fmt.Fprintln(env.Err, env.Heading("Assistant de promotion")+env.Muted("  (Ctrl+C pour quitter)"))
	if err := wizardRepos(req); err != nil {
		return err
	}
	m, src, err := resolve(req.fromAlias)
	if err != nil {
		return err
	}
	insp, ok := m.(module.Inspector)
	if !ok {
		return module.Unsupported("info", src.Repo.Format)
	}

	name, err := env.Ask("Artifact (artifactId ou groupId:artifactId)", "")
	if err != nil {
		return err
	}
	if name == "" {
		return usageError{fmt.Errorf("aucun artifact indiqué")}
	}
	g, a, v, err := resolveArtifact(ctx, m, src, name)
	if err != nil {
		return err
	}
	req.in.Group, req.in.Artifact = g, a
	req.artifactSpec = a
	if strings.Contains(name, ":") {
		req.artifactSpec = g + ":" + a
	}

	// source version
	version := v
	if version == "" {
		snaps, err := snapshotVersions(ctx, insp, src, g, a)
		if err != nil {
			return err
		}
		idx := 0
		if len(snaps) > 1 {
			opts := make([]string, len(snaps))
			for i, s := range snaps {
				opts[i] = fmt.Sprintf("%s  %s", s.Version, env.Muted(fmt.Sprintf("(%d build(s), %s)", s.Builds, ui.HumanAgo(s.Modified))))
			}
			if idx, err = env.Choose("Version snapshot à promouvoir :", opts, 0); err != nil {
				return err
			}
		}
		version = snaps[idx].Version
		if idx > 0 {
			req.fromVersion = version
		}
	}
	req.in.Version = version

	// build
	d, err := insp.Inspect(ctx, src, module.InspectInput{Group: g, Artifact: a, Version: version})
	if err != nil {
		return err
	}
	if len(d.Builds) > 1 {
		opts := make([]string, 0, len(d.Builds))
		keys := make([]string, 0, len(d.Builds))
		for i := len(d.Builds) - 1; i >= 0; i-- { // newest first
			label := d.Builds[i]
			if i == len(d.Builds)-1 {
				label += env.Muted("  (le plus récent)")
			}
			opts = append(opts, label)
			keys = append(keys, d.Builds[i])
		}
		idx, err := env.Choose("Build :", opts, 0)
		if err != nil {
			return err
		}
		if idx > 0 {
			req.in.Build = keys[idx]
		}
	}

	// target version
	base := strings.TrimSuffix(strings.TrimSuffix(d.Version, "-SNAPSHOT"), "-snapshot")
	target, err := env.Ask("Version cible dans "+req.toAlias, base)
	if err != nil {
		return err
	}
	if target != base {
		req.in.AsVersion = target
	}
	req.in.WithParent = true
	return runPromote(cmd, req)
}

// wizardRepos picks the source and destination: --from/--to, else the default pair, else a choice.
func wizardRepos(req *promoteReq) error {
	req.fromAlias, req.toAlias = req.from, req.to
	if req.fromAlias != "" && req.toAlias != "" {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pf, pt, perr := cfg.PromotionPair()
	var cands []string
	for _, al := range cfg.Aliases() {
		if r := cfg.Repos[al]; r.Format == "maven2" && (r.Type == "" || r.Type == "hosted") {
			cands = append(cands, al)
		}
	}
	pick := func(label, def string) (string, error) {
		if def != "" {
			return def, nil
		}
		i, err := env.Choose(label, cands, 0)
		if err != nil {
			return "", err
		}
		return cands[i], nil
	}
	if req.fromAlias == "" {
		if perr != nil {
			pf = ""
		}
		if req.fromAlias, err = pick("Repository source (snapshots) :", pf); err != nil {
			return err
		}
	}
	if req.toAlias == "" {
		if perr != nil {
			pt = ""
		}
		if req.toAlias, err = pick("Repository destination (releases) :", pt); err != nil {
			return err
		}
	}
	env.Infof("%s %s %s", env.Accent(req.fromAlias), env.Arrow(), env.Accent(req.toAlias))
	return nil
}
