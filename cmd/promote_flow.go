package cmd

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/spf13/cobra"
)

// promoteReq gathers what the three entry points (full form, short form, wizard) resolve to.
type promoteReq struct {
	in             module.PromoteInput
	deps, dry      bool
	from, to       string // --from / --to as typed (empty: default pair)
	fromVersion    string // --from-version as typed
	pins, setProps []string

	fromAlias, toAlias string
	artifactSpec       string // artifactId, or groupId:artifactId when ambiguous: shown in the recap
	legacy             bool
	note               string
}

// planRequiredError is returned (exit code 5) when a plan publishes modules besides the
// requested artifact and nobody authorized it.
type planRequiredError struct{ why, hint string }

func (e planRequiredError) Error() string { return e.why }

// runLegacy handles `promote <src> <dst> <g:a:v>`: nothing is inferred, closure only with --with-deps.
func runLegacy(cmd *cobra.Command, req *promoteReq, args []string) error {
	parts := strings.Split(args[2], ":")
	if parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return usageError{fmt.Errorf("coordonnées attendues sous la forme groupId:artifactId:version, reçu %q", args[2])}
	}
	req.in.Group, req.in.Artifact, req.in.Version = parts[0], parts[1], parts[2]
	req.fromAlias, req.toAlias = args[0], args[1]
	req.in.WithParent = req.deps
	req.legacy = true
	return runPromote(cmd, req)
}

// runShort handles `promote <artifact> [version-cible]`.
func runShort(cmd *cobra.Command, req *promoteReq, args []string) error {
	if err := defaultRepos(req); err != nil {
		return err
	}
	m, src, err := resolve(req.fromAlias)
	if err != nil {
		return err
	}
	g, a, v, err := resolveArtifact(cmd.Context(), m, src, args[0])
	if err != nil {
		return err
	}
	req.in.Group, req.in.Artifact = g, a
	req.artifactSpec = args[0]
	if !strings.Contains(args[0], ":") {
		req.artifactSpec = a
	}
	switch {
	case v != "":
		req.in.Version = v
	case req.fromVersion != "":
		req.in.Version = req.fromVersion
	default:
		insp, ok := m.(module.Inspector)
		if !ok {
			return module.Unsupported("info", src.Repo.Format)
		}
		snaps, err := snapshotVersions(cmd.Context(), insp, src, g, a)
		if err != nil {
			return err
		}
		req.in.Version = snaps[0].Version
		if len(snaps) > 1 {
			others := make([]string, 0, len(snaps)-1)
			for _, s := range snaps[1:] {
				others = append(others, s.Version)
			}
			req.note = fmt.Sprintf("version source : %s (la plus récente ; autres : %s — --from-version pour changer)", snaps[0].Version, strings.Join(others, ", "))
		}
	}
	if len(args) == 2 {
		req.in.AsVersion = args[1]
	}
	req.in.WithParent = true
	return runPromote(cmd, req)
}

// defaultRepos fills fromAlias/toAlias from --from/--to and the default pair.
func defaultRepos(req *promoteReq) error {
	req.fromAlias, req.toAlias = req.from, req.to
	if req.fromAlias != "" && req.toAlias != "" {
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	pf, pt, err := cfg.PromotionPair()
	if err != nil {
		return err
	}
	if req.fromAlias == "" {
		req.fromAlias = pf
	}
	if req.toAlias == "" {
		req.toAlias = pt
	}
	return nil
}

// resolveArtifact turns "artifactId", "groupId:artifactId" or "groupId:artifactId:version" into
// coordinates. An artifactId alone is looked up in the source repository.
func resolveArtifact(ctx context.Context, m module.Module, t module.Target, spec string) (g, a, v string, err error) {
	parts := strings.Split(spec, ":")
	for _, p := range parts {
		if p == "" {
			return "", "", "", usageError{fmt.Errorf("artifact invalide : %q (artifactId, groupId:artifactId ou groupId:artifactId:version)", spec)}
		}
	}
	switch len(parts) {
	case 2:
		return parts[0], parts[1], "", nil
	case 3:
		return parts[0], parts[1], parts[2], nil
	case 1:
	default:
		return "", "", "", usageError{fmt.Errorf("artifact invalide : %q", spec)}
	}
	f, ok := m.(module.Finder)
	if !ok {
		return "", "", "", fmt.Errorf("la recherche par nom n'est pas disponible pour le format %s : indiquez groupId:artifactId", t.Repo.Format)
	}
	refs, err := f.FindArtifacts(ctx, t, parts[0])
	if err != nil {
		return "", "", "", err
	}
	switch len(refs) {
	case 0:
		return "", "", "", fmt.Errorf("artifact %q introuvable dans %s (recherche exacte sur l'artifactId)", parts[0], t.Repo.Alias)
	case 1:
		return refs[0].Group, refs[0].Artifact, "", nil
	}
	opts := make([]string, len(refs))
	for i, r := range refs {
		opts[i] = r.Group + ":" + r.Artifact
	}
	if !env.InTTY {
		return "", "", "", fmt.Errorf("%q existe sous plusieurs groupes dans %s : précisez %s", parts[0], t.Repo.Alias, strings.Join(opts, " ou "))
	}
	i, err := env.Choose(fmt.Sprintf("%q existe sous plusieurs groupes :", parts[0]), opts, 0)
	if err != nil {
		return "", "", "", err
	}
	return refs[i].Group, refs[i].Artifact, "", nil
}

// snapshotVersions lists the snapshot versions of an artifact, newest first.
func snapshotVersions(ctx context.Context, insp module.Inspector, t module.Target, g, a string) ([]module.VersionInfo, error) {
	s, err := insp.Versions(ctx, t, g, a)
	if err != nil {
		return nil, err
	}
	var snaps, rel []module.VersionInfo
	for i := len(s.Versions) - 1; i >= 0; i-- {
		if s.Versions[i].Kind == "snapshot" {
			snaps = append(snaps, s.Versions[i])
		} else {
			rel = append(rel, s.Versions[i])
		}
	}
	if len(snaps) == 0 {
		names := make([]string, 0, len(rel))
		for _, r := range rel {
			names = append(names, r.Version)
		}
		return nil, fmt.Errorf("%s:%s n'a aucune version snapshot dans %s (versions : %s)", g, a, t.Repo.Alias, strings.Join(names, ", "))
	}
	return snaps, nil
}

// runPromote plans, gates and executes a promotion; every outcome ends with the equivalent short command.
func runPromote(cmd *cobra.Command, req *promoteReq) error {
	ctx := cmd.Context()
	sm, src, err := resolve(req.fromAlias)
	if err != nil {
		return err
	}
	_, dst, err := resolve(req.toAlias)
	if err != nil {
		return err
	}
	p, ok := sm.(module.Promoter)
	if !ok {
		return module.Unsupported("promote", src.Repo.Format)
	}
	if req.note != "" {
		env.Infof("%s", req.note)
	}
	plan, err := p.PlanPromote(ctx, src, dst, req.in)
	if err != nil {
		return err
	}
	printPromotionPlan(plan, src.Repo.Alias, dst.Repo.Alias)
	printPlan(plan)

	if len(plan.Blockers) > 0 {
		recap(req)
		return fmt.Errorf("%d module(s) requis introuvable(s) : rien n'est publié (une release incohérente est pire qu'une release absente)\n  %s traitez-les d'abord (promouvez-les ou indiquez une version existante avec --pin / --set-property), puis relancez", len(plan.Blockers), env.Arrow())
	}
	if problems := blockingProblems(plan, dst.Repo.Alias); len(problems) > 0 {
		recap(req)
		if len(plan.Blocking()) > 0 && plan.WritePolicy != "ALLOW_ONCE" && !req.in.Force {
			// give the exact command that replaces the published files
			r2 := *req
			r2.in.Force = true
			if c := r2.shortCommand(); c != "" {
				fmt.Fprintf(env.Err, "%s\n  %s\n", env.Muted("Pour remplacer les fichiers publiés (relisez d'abord le plan) :"), c)
			}
		}
		return errors.New(strings.Join(problems, "\n"))
	}
	extra := len(plan.Parents)
	if req.dry {
		env.Infof("dry-run : rien n'a été modifié")
		recap(req)
		return emitJSON(map[string]any{"schema": 1, "plan": plan})
	}

	confirmed := false
	if extra > 0 && !req.deps {
		names := make([]string, 0, extra+1)
		for _, par := range plan.Parents {
			names = append(names, par.Artifact)
		}
		names = append(names, plan.Artifact)
		if !env.InTTY {
			req.deps = true // the command to rerun
			recap(req)
			return planRequiredError{
				why:  fmt.Sprintf("ce plan publie %d artifacts dans %s (%s) : %d module(s) en plus de %s", extra+1, dst.Repo.Alias, strings.Join(names, ", "), extra, plan.Artifact),
				hint: "relisez le plan ci-dessus puis relancez la même commande avec --with-deps pour l'accepter",
			}
		}
		ok, err := env.ConfirmStrict(fmt.Sprintf("Ce plan publie %d artifacts dans %s (%s). L'exécuter ?", extra+1, dst.Repo.Alias, strings.Join(names, ", ")))
		if err != nil {
			return err
		}
		if !ok {
			env.Infof("abandon, rien n'a été modifié")
			recap(req)
			return nil
		}
		req.deps, confirmed = true, true
	}
	if !confirmed {
		ok, err := env.Confirm(confirmText(plan))
		if err != nil {
			return err
		}
		if !ok {
			env.Infof("abandon, rien n'a été modifié")
			recap(req)
			return nil
		}
	}

	res, err := p.ExecutePromote(ctx, src, dst, plan, &barReporter{})
	if res != nil {
		printResult(res)
		_ = emitJSON(map[string]any{"schema": 1, "plan": plan, "result": res})
	}
	if errors.Is(err, module.ErrPartial) {
		why := "certains fichiers n'ont pas pu être promus ou vérifiés"
		if res != nil && len(res.Published) > 0 {
			names := make([]string, len(res.Published))
			for i, p := range res.Published {
				names[i] = path.Base(p)
			}
			why += fmt.Sprintf("\n  déjà publié et vérifié dans %s : %s", dst.Repo.Alias, strings.Join(names, ", "))
		}
		env.Failure("Promotion partielle", why,
			"relancer la même commande est sans risque : les fichiers identiques sont ignorés ; un fichier existant au contenu différent est refusé par Nexus sans rien modifier")
	}
	recap(req)
	return err
}

// printPromotionPlan shows the promotion plan as one table: what exists, what gets published, what blocks.
func printPromotionPlan(plan *module.PromotePlan, from, to string) {
	if len(plan.Modules) == 0 && len(plan.Parents) == 0 {
		return
	}
	e := env
	fmt.Fprintf(e.Err, "%s  %s\n", e.Heading("Plan de promotion"), e.Muted(from+" "+e.Arrow()+" "+to))
	for _, m := range plan.Modules {
		var mark string
		switch m.Status {
		case "released":
			mark = e.IconOK() + " " + e.Muted(fmt.Sprintf("%-13s", "en release"))
		case "promote":
			mark = e.Arrow() + " " + e.Info(fmt.Sprintf("%-13s", "à promouvoir"))
		default:
			mark = e.IconErr() + " " + e.Error(fmt.Sprintf("%-13s", "bloquant"))
		}
		detail := ""
		if m.Detail != "" {
			detail = "  " + e.Muted("("+m.Detail+")")
		}
		fmt.Fprintf(e.Err, "  %s %s %s%s\n", mark, e.Accent(m.Artifact), m.Version, detail)
	}
	build := ""
	if plan.SourceBuild != "" {
		build = "  " + e.Muted("(racine, build "+plan.SourceBuild+")")
	}
	fmt.Fprintf(e.Err, "  %s %s %s%s\n\n", e.Arrow()+" "+e.Info(fmt.Sprintf("%-13s", "à promouvoir")), e.Accent(plan.Artifact), plan.TargetVersion, build)
	for _, b := range plan.Blockers {
		fmt.Fprintf(e.Err, "  %s %s\n", e.IconErr(), b)
	}
}

// recap prints the short command that redoes this promotion.
func recap(req *promoteReq) {
	if c := req.shortCommand(); c != "" {
		fmt.Fprintf(env.Err, "\n%s\n  %s\n", env.Muted("Commande équivalente, à réutiliser :"), c)
	}
}

func (r *promoteReq) shortCommand() string {
	if r.legacy || r.artifactSpec == "" {
		return ""
	}
	parts := []string{"nexus promote", r.artifactSpec}
	if r.in.AsVersion != "" {
		parts = append(parts, r.in.AsVersion)
	}
	add := func(cond bool, s ...string) {
		if cond {
			parts = append(parts, s...)
		}
	}
	add(r.from != "", "--from", r.from)
	add(r.to != "", "--to", r.to)
	add(r.fromVersion != "", "--from-version", r.fromVersion)
	add(r.in.Build != "", "--build", r.in.Build)
	add(r.deps, "--with-deps")
	add(r.in.Force, "--force")
	add(r.in.NoMarker, "--no-marker")
	add(r.in.DeleteSource, "--delete-source")
	add(r.in.AlignProperties, "--align-properties")
	add(r.in.AllowSnapshotRefs, "--allow-snapshot-refs")
	for _, p := range r.pins {
		parts = append(parts, "--pin", p)
	}
	keys := make([]string, 0, len(r.setProps))
	keys = append(keys, r.setProps...)
	sort.Strings(keys)
	for _, p := range keys {
		parts = append(parts, "--set-property", p)
	}
	return strings.Join(parts, " ")
}
