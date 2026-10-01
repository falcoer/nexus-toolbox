package maven

import (
	"bytes"
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
	"time"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

// Nexus 3 OSS has no promote API: promotion = rename + verified copy (+ optional delete of the source).
// Binary files are copied byte for byte; the pom is rewritten (own version, pinned references).

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

func sha1Hex(b []byte) string { h := sha1.Sum(b); return hex.EncodeToString(h[:]) }

// srcFile is a source file with the part of its name after "<artifactId>-<fileVersion>".
type srcFile struct {
	nexus.Asset
	Rest string
}

type resolved struct {
	sourceVersion, sourceBuild, fileVersion string
	builds                                  []string
	files                                   []srcFile
	componentIDs                            []string
	target                                  string
}

// resolveSource finds the files to promote: a release version, or one build of a snapshot.
func resolveSource(ctx context.Context, src module.Target, in module.PromoteInput) (*resolved, error) {
	r := &resolved{}
	base, ts, n, isTS := SplitSnapshotVersion(in.Version)
	switch {
	case isTS || IsSnapshot(in.Version):
		wanted := in.Build
		if isTS {
			if wanted != "" && wanted != ts+"-"+fmt.Sprint(n) && wanted != fmt.Sprint(n) {
				return nil, fmt.Errorf("--build %s contredit la version %s", wanted, in.Version)
			}
			wanted = ts + "-" + fmt.Sprint(n)
		} else {
			base = in.Version[:len(in.Version)-len("-SNAPSHOT")]
		}
		builds, err := listBuilds(ctx, src.Client, src.Repo.Name, in.Group, in.Artifact, base)
		if err != nil {
			return nil, err
		}
		b, err := selectBuild(builds, wanted)
		if err != nil {
			return nil, err
		}
		r.sourceVersion, r.sourceBuild, r.fileVersion = base+"-SNAPSHOT", b.Key, b.FileVersion
		for _, o := range builds {
			r.builds = append(r.builds, o.Key)
			if o != b {
				for id := range o.Components {
					if b.Components[id] {
						if in.DeleteSource {
							return nil, fmt.Errorf("--delete-source impossible : le composant %s regroupe plusieurs builds du snapshot", id)
						}
					}
				}
			}
		}
		for _, a := range b.Assets {
			r.files = append(r.files, srcFile{a.Asset, a.Rest})
		}
		for id := range b.Components {
			r.componentIDs = append(r.componentIDs, id)
		}
		r.target = base
	default:
		c, err := findComponent(ctx, src, in)
		if err != nil {
			return nil, err
		}
		r.sourceVersion, r.fileVersion, r.target = in.Version, in.Version, in.Version
		r.componentIDs = []string{c.ID}
		prefix := in.Artifact + "-" + in.Version
		for _, a := range c.Assets {
			if isGenerated(a.Path) || !strings.HasPrefix(path.Base(a.Path), prefix) {
				continue
			}
			r.files = append(r.files, srcFile{a, strings.TrimPrefix(path.Base(a.Path), prefix)})
		}
	}
	if in.AsVersion != "" {
		r.target = in.AsVersion
	}
	return r, nil
}

// PlanPromote validates the request and computes, per file, what has to be done. It writes nothing.
func (m *Module) PlanPromote(ctx context.Context, src, dst module.Target, in module.PromoteInput) (*module.PromotePlan, error) {
	if in.Group == "" || in.Artifact == "" || in.Version == "" {
		return nil, fmt.Errorf("coordonnées incomplètes : group:artifact:version attendus")
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
	if in.AsVersion != "" && (IsSnapshot(in.AsVersion) || strings.ContainsAny(in.AsVersion, "/ \t")) {
		return nil, fmt.Errorf("version cible invalide : %q (une version figée est attendue)", in.AsVersion)
	}

	res, err := resolveSource(ctx, src, in)
	if err != nil {
		return nil, err
	}
	target := res.target
	if IsSnapshot(target) {
		return nil, fmt.Errorf("la version cible %s est un SNAPSHOT : une version figée est attendue", target)
	}
	if len(res.files) == 0 {
		return nil, fmt.Errorf("%s:%s:%s sans fichier exploitable", in.Group, in.Artifact, in.Version)
	}
	plan := &module.PromotePlan{Group: in.Group, Artifact: in.Artifact, Version: in.Version,
		SourceVersion: res.sourceVersion, SourceBuild: res.sourceBuild, Builds: res.builds, TargetVersion: target,
		Source: src.Repo.Name, Destination: dst.Repo.Name, Force: in.Force, DeleteSource: in.DeleteSource,
		AllowSnapshotRefs: in.AllowSnapshotRefs, Tool: in.Tool}
	if len(res.componentIDs) == 1 {
		plan.SourceID = res.componentIDs[0]
	} else if in.DeleteSource {
		return nil, fmt.Errorf("--delete-source impossible : les fichiers sont répartis sur %d composants", len(res.componentIDs))
	}

	// Destination policy (needs read access on repository metadata; tolerate failure).
	forceAllowed := true
	if info, err := dst.Client.Repository(ctx, dst.Repo.Name); err == nil {
		if strings.EqualFold(info.Attributes.Maven.VersionPolicy, "SNAPSHOT") {
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

	gpath := strings.ReplaceAll(in.Group, ".", "/")
	dstName := func(rest string) string { return in.Artifact + "-" + target + rest }
	dstPath := func(rest string) string { return gpath + "/" + in.Artifact + "/" + target + "/" + dstName(rest) }

	seen := map[string]bool{}
	hasPom := false
	for _, f := range res.files {
		it := module.PlanItem{Kind: module.KindFile, SourcePath: f.Path, Path: dstPath(f.Rest), Size: f.FileSize,
			SourceSHA1: f.Checksum["sha1"], SHA1: f.Checksum["sha1"], DownloadURL: f.DownloadURL}
		if seen[it.Path] {
			return nil, fmt.Errorf("deux fichiers sources donnent le même chemin de destination : %s", it.Path)
		}
		seen[it.Path] = true
		if f.Rest == ".pom" {
			hasPom = true
			if err := rewritePomItem(ctx, src, &it, plan, target, in); err != nil {
				return nil, err
			}
		}
		plan.Items = append(plan.Items, it)
	}
	if !hasPom {
		plan.Warnings = append(plan.Warnings, "aucun .pom trouvé pour ce build : la release serait inutilisable par Maven")
	}
	if res.fileVersion != target {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("les binaires (.war, .jar…) sont copiés tels quels : leurs métadonnées internes (META-INF) peuvent encore mentionner %s", res.sourceVersion))
	}

	for i := range plan.Items {
		classify(ctx, dst, &plan.Items[i], in.Force, forceAllowed)
		if plan.Items[i].Action == "" {
			return nil, fmt.Errorf("lecture de la destination pour %s impossible", plan.Items[i].Path)
		}
	}
	// Order: binaries first, pom last, marker very last.
	sort.SliceStable(plan.Items, func(i, j int) bool {
		return strings.HasSuffix(plan.Items[j].Path, ".pom") && !strings.HasSuffix(plan.Items[i].Path, ".pom")
	})
	if !in.NoMarker {
		mk := module.PlanItem{Kind: module.KindMarker, Path: dstPath("-" + markerClassifier + ".txt")}
		classifyMarker(ctx, dst, &mk, in.Force, forceAllowed)
		plan.Items = append(plan.Items, mk)
	}
	return plan, nil
}

// rewritePomItem downloads the pom, rewrites it, and records the expected destination sha1.
func rewritePomItem(ctx context.Context, src module.Target, it *module.PlanItem, plan *module.PromotePlan, target string, in module.PromoteInput) error {
	raw, err := src.Client.GetBytes(ctx, it.DownloadURL)
	if err != nil {
		return fmt.Errorf("lecture du pom source : %w", err)
	}
	if it.SourceSHA1 != "" && !strings.EqualFold(sha1Hex(raw), it.SourceSHA1) {
		return fmt.Errorf("pom source : sha1 téléchargé ≠ sha1 annoncé par Nexus")
	}
	pr, err := RewritePom(raw, target, in.Pins)
	if err != nil {
		return err
	}
	if !pr.HasVersion {
		plan.Warnings = append(plan.Warnings, "le pom ne déclare pas de <version> propre (héritée du parent) : rien à réécrire")
	}
	for _, p := range pr.UnusedPins {
		plan.Warnings = append(plan.Warnings, "--pin sans effet (aucune référence SNAPSHOT correspondante) : "+p)
	}
	plan.SnapshotRefs = append(plan.SnapshotRefs, pr.Refs...)
	it.Content, it.Size, it.SHA1 = pr.Out, int64(len(pr.Out)), sha1Hex(pr.Out)
	it.Transformed, it.Diff = !bytes.Equal(pr.Out, raw), pr.Diff
	return nil
}

// classify compares an item with what the destination already holds.
func classify(ctx context.Context, dst module.Target, it *module.PlanItem, force, forceAllowed bool) {
	remote, err := dst.Client.GetText(ctx, dst.Client.RepoURL(dst.Repo.Name, it.Path+".sha1"))
	switch {
	case err == nil && remote != "" && strings.EqualFold(remote, it.SHA1):
		it.Action, it.Reason = "skip", "déjà présent et identique"
	case err == nil:
		it.Action, it.Reason = "conflict", "existe déjà avec un contenu différent"
		if force && forceAllowed {
			it.Action, it.Reason = "copy", "écrase la version existante (--force)"
		} else if force {
			it.Reason += " (write policy ALLOW_ONCE : écrasement impossible)"
		}
	case nexus.IsNotFound(err):
		it.Action = "copy"
	}
}

// classifyMarker never conflicts: an existing marker is kept (its content embeds a date).
func classifyMarker(ctx context.Context, dst module.Target, it *module.PlanItem, force, forceAllowed bool) {
	_, err := dst.Client.GetText(ctx, dst.Client.RepoURL(dst.Repo.Name, it.Path+".sha1"))
	switch {
	case err == nil && force && forceAllowed:
		it.Action, it.Reason = "copy", "remplace le marqueur existant (--force)"
	case err == nil:
		it.Action, it.Reason = "skip", "marqueur déjà présent, conservé"
	default:
		it.Action = "copy"
	}
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

// ExecutePromote copies the planned files, verifies them, writes the marker, and optionally deletes the source.
func (m *Module) ExecutePromote(ctx context.Context, src, dst module.Target, plan *module.PromotePlan, rep module.Reporter) (*module.PromoteResult, error) {
	if n := len(plan.Blocking()); n > 0 {
		return nil, fmt.Errorf("%d fichier(s) en conflit : promotion annulée", n)
	}
	if refs := plan.BlockingRefs(); len(refs) > 0 {
		return nil, fmt.Errorf("%d référence(s) SNAPSHOT dans le pom : promotion annulée", len(refs))
	}
	res := &module.PromoteResult{}
	var files []module.PlanItem
	var marker *module.PlanItem
	for i := range plan.Items {
		it := plan.Items[i]
		if it.Kind == module.KindMarker {
			marker = &plan.Items[i]
			continue
		}
		files = append(files, it)
	}
	for _, it := range files {
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
	}
	// Verification: the destination's own checksum must match the expected one.
	for _, it := range files {
		remote, err := dst.Client.GetText(ctx, dst.Client.RepoURL(dst.Repo.Name, it.Path+".sha1"))
		if err != nil || !strings.EqualFold(remote, it.SHA1) {
			res.Failed = append(res.Failed, fmt.Sprintf("%s : vérification sha1 échouée", it.Path))
		}
	}
	res.Verified = len(res.Failed) == 0
	if !res.Verified {
		return res, module.ErrPartial
	}

	md, err := dst.Client.GetBytes(ctx, dst.Client.RepoURL(dst.Repo.Name, strings.ReplaceAll(plan.Group, ".", "/")+"/"+plan.Artifact+"/maven-metadata.xml"))
	res.MetadataOK = err == nil && bytes.Contains(md, []byte("<version>"+plan.TargetVersion+"</version>"))
	if !res.MetadataOK {
		res.Warnings = append(res.Warnings, fmt.Sprintf("maven-metadata.xml de %s:%s ne liste pas encore %s : lancez la tâche Nexus « Rebuild Maven repository metadata »", plan.Group, plan.Artifact, plan.TargetVersion))
	}

	if marker != nil && marker.Action == "copy" {
		body := BuildMarker(plan, src.Repo.URL, dst.Client.User, time.Now())
		rep.AssetStart(marker.Path, int64(len(body)))
		err := putBytes(ctx, dst, marker.Path, body, "text/plain; charset=utf-8")
		if err == nil {
			var remote string
			if remote, err = dst.Client.GetText(ctx, dst.Client.RepoURL(dst.Repo.Name, marker.Path+".sha1")); err == nil && !strings.EqualFold(remote, sha1Hex(body)) {
				err = fmt.Errorf("vérification sha1 échouée")
			}
		}
		if err == nil {
			rep.AssetBytes(int64(len(body)))
		}
		rep.AssetDone(marker.Path, err)
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s : %v", marker.Path, err))
			return res, module.ErrPartial
		}
		res.MarkerWritten = true
	}

	if plan.DeleteSource {
		if err := src.Client.DeleteComponent(ctx, plan.SourceID); err != nil {
			return res, fmt.Errorf("copie vérifiée mais suppression de la source impossible : %w", err)
		}
		res.SourceDeleted = true
	}
	return res, nil
}

func putBytes(ctx context.Context, dst module.Target, p string, b []byte, ctype string) error {
	open := func() (io.Reader, error) { return bytes.NewReader(b), nil }
	if err := dst.Client.Put(ctx, dst.Repo.Name, p, open, int64(len(b)), ctype); err != nil {
		return fmt.Errorf("envoi : %w", err)
	}
	return nil
}

// copyOne uploads the in-memory content (rewritten pom) or streams the source file through a temp file.
func copyOne(ctx context.Context, src, dst module.Target, it module.PlanItem, rep module.Reporter) error {
	if it.Content != nil {
		if err := putBytes(ctx, dst, it.Path, it.Content, contentType(it.Path)); err != nil {
			return err
		}
		rep.AssetBytes(int64(len(it.Content)))
		return nil
	}
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
	if it.SourceSHA1 != "" && !strings.EqualFold(sum, it.SourceSHA1) {
		return fmt.Errorf("sha1 téléchargé (%s) ≠ sha1 annoncé par la source (%s)", sum, it.SourceSHA1)
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
	case ".txt":
		return "text/plain"
	}
	return "application/octet-stream"
}
