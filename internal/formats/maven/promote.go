package maven

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

// Nexus 3 OSS has no promote API: promotion = verified copy (+ optional delete of the source).

func isGenerated(p string) bool {
	base := path.Base(p)
	if strings.HasPrefix(base, "maven-metadata.xml") {
		return true
	}
	for _, s := range []string{".sha1", ".sha256", ".sha512", ".md5"} {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	return false
}

// PlanPromote validates the request and computes, per file, what has to be done. It writes nothing.
func (m *Module) PlanPromote(ctx context.Context, src, dst module.Target, in module.PromoteInput) (*module.PromotePlan, error) {
	if in.Group == "" || in.Artifact == "" || in.Version == "" {
		return nil, fmt.Errorf("coordonnées incomplètes : group:artifact:version attendus")
	}
	if IsSnapshot(in.Version) {
		return nil, fmt.Errorf("la version %s est un SNAPSHOT : seule une version figée peut être promue vers un repository release", in.Version)
	}
	for _, t := range []module.Target{src, dst} {
		if t.Repo.Format != "maven2" {
			return nil, fmt.Errorf("%s est de format %s : promote ne supporte que maven2", t.Repo.Alias, t.Repo.Format)
		}
		if t.Repo.Type != "" && t.Repo.Type != "hosted" {
			return nil, fmt.Errorf("%s est de type %s : promote exige des repositories hosted", t.Repo.Alias, t.Repo.Type)
		}
	}
	if src.Repo.Base == dst.Repo.Base && src.Repo.Name == dst.Repo.Name {
		return nil, fmt.Errorf("la source et la destination sont le même repository")
	}
	plan := &module.PromotePlan{Group: in.Group, Artifact: in.Artifact, Version: in.Version,
		Source: src.Repo.Name, Destination: dst.Repo.Name, Force: in.Force, DeleteSource: in.DeleteSource}

	// Destination policy (needs read access on repository metadata; tolerate failure).
	forceAllowed := true
	if info, err := dst.Client.Repository(ctx, dst.Repo.Name); err == nil {
		if p := strings.ToUpper(info.Attributes.Maven.VersionPolicy); p == "SNAPSHOT" {
			return nil, fmt.Errorf("%s a une version policy SNAPSHOT : destination invalide pour une release", dst.Repo.Alias)
		}
		switch strings.ToUpper(info.Attributes.Storage.WritePolicy) {
		case "DENY":
			return nil, fmt.Errorf("%s est en lecture seule (write policy DENY)", dst.Repo.Alias)
		case "ALLOW_ONCE":
			forceAllowed = false
		}
	} else {
		plan.Warnings = append(plan.Warnings, "politique de la destination non vérifiée : "+err.Error())
	}

	comp, err := findComponent(ctx, src, in)
	if err != nil {
		return nil, err
	}
	plan.SourceID = comp.ID
	for _, a := range comp.Assets {
		if isGenerated(a.Path) {
			continue
		}
		it := module.PlanItem{Path: a.Path, Size: a.FileSize, SHA1: a.Checksum["sha1"], DownloadURL: a.DownloadURL}
		remote, err := dst.Client.GetText(ctx, dst.Client.RepoURL(dst.Repo.Name, a.Path+".sha1"))
		switch {
		case err == nil && remote != "" && strings.EqualFold(remote, it.SHA1):
			it.Action, it.Reason = "skip", "déjà présent et identique"
		case err == nil:
			it.Action, it.Reason = "conflict", "existe déjà avec un contenu différent"
			if in.Force && forceAllowed {
				it.Action, it.Reason = "copy", "écrase la version existante (--force)"
			} else if in.Force {
				it.Reason += " (write policy ALLOW_ONCE : écrasement impossible)"
			}
		case nexus.IsNotFound(err):
			it.Action = "copy"
		default:
			return nil, fmt.Errorf("lecture de la destination pour %s : %w", a.Path, err)
		}
		plan.Items = append(plan.Items, it)
	}
	if len(plan.Items) == 0 {
		return nil, fmt.Errorf("composant %s:%s:%s sans fichier exploitable", in.Group, in.Artifact, in.Version)
	}
	sort.SliceStable(plan.Items, func(i, j int) bool {
		return strings.HasSuffix(plan.Items[j].Path, ".pom") && !strings.HasSuffix(plan.Items[i].Path, ".pom")
	})
	return plan, nil
}

func findComponent(ctx context.Context, src module.Target, in module.PromoteInput) (*nexus.Component, error) {
	q := url.Values{"repository": {src.Repo.Name}, "group": {in.Group}, "name": {in.Artifact}, "version": {in.Version}}
	token := ""
	for {
		page, err := src.Client.SearchComponents(ctx, q, token)
		if err != nil {
			return nil, err
		}
		for _, c := range page.Items {
			if c.Group == in.Group && c.Name == in.Artifact && c.Version == in.Version {
				c := c
				return &c, nil
			}
		}
		if token = page.ContinuationToken; token == "" {
			return nil, fmt.Errorf("%s:%s:%s introuvable dans %s", in.Group, in.Artifact, in.Version, src.Repo.Alias)
		}
	}
}

// ExecutePromote copies the planned files, verifies them, and optionally deletes the source.
func (m *Module) ExecutePromote(ctx context.Context, src, dst module.Target, plan *module.PromotePlan, rep module.Reporter) (*module.PromoteResult, error) {
	if len(plan.Blocking()) > 0 {
		return nil, fmt.Errorf("%d fichier(s) en conflit : promotion annulée", len(plan.Blocking()))
	}
	res := &module.PromoteResult{}
	var copied []module.PlanItem
	for _, it := range plan.Items {
		if it.Action == "skip" {
			res.Skipped++
			continue
		}
		rep.AssetStart(it.Path, it.Size)
		err := copyOne(ctx, src, dst, it, rep)
		rep.AssetDone(it.Path, err)
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s : %v", it.Path, err))
			if ctx.Err() != nil {
				break
			}
			continue
		}
		res.Copied++
		copied = append(copied, it)
	}
	// Verification: compare the destination's own checksum with the source's.
	res.Verified = len(res.Failed) == 0
	for _, it := range plan.Items {
		if it.Action != "skip" && !contains(copied, it.Path) {
			continue
		}
		remote, err := dst.Client.GetText(ctx, dst.Client.RepoURL(dst.Repo.Name, it.Path+".sha1"))
		if err != nil || !strings.EqualFold(remote, it.SHA1) {
			res.Verified = false
			res.Failed = append(res.Failed, fmt.Sprintf("%s : vérification sha1 échouée", it.Path))
		}
	}
	if len(res.Failed) > 0 {
		return res, module.ErrPartial
	}
	if plan.DeleteSource {
		if err := src.Client.DeleteComponent(ctx, plan.SourceID); err != nil {
			return res, fmt.Errorf("copie vérifiée mais suppression de la source impossible : %w", err)
		}
		res.SourceDeleted = true
	}
	return res, nil
}

func contains(items []module.PlanItem, p string) bool {
	for _, it := range items {
		if it.Path == p {
			return true
		}
	}
	return false
}

func copyOne(ctx context.Context, src, dst module.Target, it module.PlanItem, rep module.Reporter) error {
	tmp, err := os.CreateTemp("", "nexus-promote-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	h := sha1.New()
	n, err := src.Client.Download(ctx, it.DownloadURL, io.MultiWriter(tmp, h))
	if err != nil {
		return fmt.Errorf("téléchargement : %w", err)
	}
	sum := hex.EncodeToString(h.Sum(nil))
	if it.SHA1 != "" && !strings.EqualFold(sum, it.SHA1) {
		return fmt.Errorf("sha1 téléchargé (%s) ≠ sha1 annoncé par la source (%s)", sum, it.SHA1)
	}
	rep.AssetBytes(n)
	open := func() (io.Reader, error) {
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		return io.NopCloser(tmp), nil
	}
	if err := dst.Client.Put(ctx, dst.Repo.Name, it.Path, open, n, contentType(it.Path)); err != nil {
		return fmt.Errorf("envoi : %w", err)
	}
	return nil
}

func contentType(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".jar", ".war", ".ear":
		return "application/java-archive"
	case ".pom", ".xml":
		return "application/xml"
	}
	return "application/octet-stream"
}
