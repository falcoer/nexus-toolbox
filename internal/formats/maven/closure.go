package maven

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

// resolveNeed decides what happens to one SNAPSHOT module a pom needs, and returns the version
// the pom must reference:
//
//   - target version: a pin if given; the root's target version when the module shares the
//     root's base version (same reactor); otherwise its own base version without -SNAPSHOT;
//   - released: the destination already holds that version, nothing to publish;
//   - promote: a snapshot build exists, it is planned (dependencies first);
//   - blocked: neither exists, the promotion cannot go on (the plan says what is available).
func (m *Module) resolveNeed(ctx context.Context, src, dst module.Target, in module.PromoteInput, plan *module.PromotePlan, n Need) (string, bool, error) {
	chain := in.Chain
	g := strings.ReplaceAll(n.Group, "${project.groupId}", in.Group)
	if g == "" || n.Artifact == "" || strings.Contains(g+n.Artifact, "${") || !IsSnapshot(n.Version) {
		return "", false, nil
	}
	snapBase := n.Version[:len(n.Version)-len("-SNAPSHOT")]
	target := snapBase
	if pin, pinned := findPin(in.Pins, g, n.Artifact); pinned && n.Kind != "property" {
		target = pin.Version
	} else if chain.RootBase != "" && snapBase == chain.RootBase && chain.RootTarget != "" {
		target = chain.RootTarget
	}
	key := g + ":" + n.Artifact + ":" + target
	if st, ok := chain.Seen[key]; ok {
		return target, st.Status != "blocked", nil
	}
	st := &module.ModuleStatus{Group: g, Artifact: n.Artifact, Version: target}
	chain.Seen[key] = st

	if releasedInDestination(ctx, dst, g, n.Artifact, target) {
		st.Status, st.Detail = "released", "déjà dans "+dst.Repo.Alias
		plan.Modules = append(plan.Modules, *st)
		return target, true, nil
	}

	sub := in
	sub.Group, sub.Artifact, sub.Version = g, n.Artifact, snapBase+"-SNAPSHOT"
	sub.AsVersion, sub.Build, sub.DeleteSource, sub.Force = "", "", false, in.Force
	if target != snapBase {
		sub.AsVersion = target
	}
	sub.Ancestors = append(append([]string(nil), in.Ancestors...), in.Group+":"+in.Artifact)
	pp, err := m.PlanPromote(ctx, src, dst, sub)
	switch {
	case err == nil:
		// flatten: what the module needs comes first, then the module itself
		plan.Parents = append(plan.Parents, pp.Parents...)
		plan.Modules = append(plan.Modules, pp.Modules...)
		plan.Blockers = append(plan.Blockers, pp.Blockers...)
		plan.PinsUsed = append(plan.PinsUsed, pp.PinsUsed...)
		plan.PropsUsed = append(plan.PropsUsed, pp.PropsUsed...)
		pp.Parents, pp.Modules, pp.Blockers = nil, nil, nil
		plan.Parents = append(plan.Parents, pp)
		st.Status, st.Detail = "promote", "build "+pp.SourceBuild
		plan.Modules = append(plan.Modules, *st)
		return target, true, nil
	case errors.Is(err, ErrNoBuild) && allowedMissing(in.AllowMissing, g, n.Artifact):
		// explicitly accepted by the user: not published, the pom still points at the target version
		st.Status = "ignored"
		st.Detail = "absent, ignoré (--allow-missing) : la référence pointe vers un artifact inexistant dans " + dst.Repo.Alias
		plan.Modules = append(plan.Modules, *st)
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s:%s:%s absent de %s : référence conservée (--allow-missing)", g, n.Artifact, target, dst.Repo.Alias))
		return target, true, nil
	case errors.Is(err, ErrNoBuild):
		st.Status = "blocked"
		st.Detail = m.missingDetail(ctx, src, dst, g, n, target)
		plan.Modules = append(plan.Modules, *st)
		plan.Blockers = append(plan.Blockers, fmt.Sprintf("%s:%s:%s — %s", g, n.Artifact, target, st.Detail))
		return "", false, nil
	default:
		return "", false, err
	}
}

// allowedMissing reports whether g:a (or the bare artifactId) is listed in --allow-missing.
func allowedMissing(list []string, g, a string) bool {
	for _, x := range list {
		if x == g+":"+a || x == a {
			return true
		}
	}
	return false
}

// missingDetail explains a blocking module and what to do about it.
func (m *Module) missingDetail(ctx context.Context, src, dst module.Target, g string, n Need, target string) string {
	d := fmt.Sprintf("absent de %s et sans build snapshot dans %s", dst.Repo.Alias, src.Repo.Alias)
	list := func(t module.Target, kind string) string {
		s, err := m.Versions(ctx, t, g, n.Artifact)
		if err != nil {
			return ""
		}
		var vs []string
		for _, v := range s.Versions {
			if v.Kind == kind {
				vs = append(vs, v.Version)
			}
		}
		if len(vs) > 6 {
			vs = vs[len(vs)-6:]
		}
		return strings.Join(vs, ", ")
	}
	if rel := list(dst, "release"); rel != "" {
		d += " ; versions en release : " + rel
	}
	if snap := list(src, "snapshot"); snap != "" {
		d += " ; versions snapshot : " + snap
	}
	if n.Kind == "property" {
		return d + fmt.Sprintf(" → --set-property %s=<version> pour viser une version existante, ou --allow-missing %s:%s si le build ne l'utilise pas", n.Property, g, n.Artifact)
	}
	return d + fmt.Sprintf(" → --pin %s:%s=<version> pour viser une version existante, ou --allow-missing %s:%s si le build ne l'utilise pas", g, n.Artifact, g, n.Artifact)
}
