package maven

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

var _ module.Inspector = (*Module)(nil)

// maxSizeLookups bounds the HEAD requests used to fill in sizes the search API does not report.
const maxSizeLookups = 40

func dirURLs(t module.Target, group, artifact, version string) (dir, browse string) {
	p := strings.ReplaceAll(group, ".", "/") + "/" + artifact
	if version != "" {
		p += "/" + version
	}
	dir = t.Client.RepoURL(t.Repo.Name, p+"/")
	browse = t.Client.Base + "/#browse/browse:" + t.Repo.Name + ":" + url.PathEscape(p)
	return dir, browse
}

// Versions lists every version of group:artifact in the repository. Timestamped snapshot
// builds are grouped under their X-SNAPSHOT version.
func (m *Module) Versions(ctx context.Context, t module.Target, group, artifact string) (*module.ArtifactSummary, error) {
	q := url.Values{"repository": {t.Repo.Name}, "group": {group}, "name": {artifact}}
	byVersion := map[string]*module.VersionInfo{}
	seenBuild := map[string]bool{}
	token := ""
	for {
		page, err := t.Client.SearchComponents(ctx, q, token)
		if err != nil {
			return nil, err
		}
		for _, c := range page.Items {
			if c.Group != group || c.Name != artifact {
				continue
			}
			key, kind := c.Version, "release"
			if base, _, _, ok := SplitSnapshotVersion(c.Version); ok {
				key, kind = base+"-SNAPSHOT", "snapshot"
			} else if IsSnapshot(c.Version) {
				kind = "snapshot"
			}
			v := byVersion[key]
			if v == nil {
				v = &module.VersionInfo{Version: key, Kind: kind}
				byVersion[key] = v
			}
			if kind == "snapshot" && !seenBuild[c.Version] {
				seenBuild[c.Version] = true
				v.Builds++
			}
			for _, a := range c.Assets {
				if isGenerated(a.Path) {
					continue
				}
				v.Files++
				if a.LastModified.After(v.Modified) {
					v.Modified = a.LastModified
				}
			}
		}
		if token = page.ContinuationToken; token == "" {
			break
		}
	}
	if len(byVersion) == 0 {
		return nil, fmt.Errorf("aucun composant %s:%s dans %s", group, artifact, t.Repo.Alias)
	}
	s := &module.ArtifactSummary{Repository: t.Repo.Name, Group: group, Artifact: artifact}
	for _, v := range byVersion {
		s.Versions = append(s.Versions, *v)
	}
	sort.Slice(s.Versions, func(i, j int) bool { return CompareVersions(s.Versions[i].Version, s.Versions[j].Version) < 0 })
	s.DirectoryURL, s.BrowseURL = dirURLs(t, group, artifact, "")
	return s, nil
}

// Inspect describes one version: coordinates, publication facts, and one direct link per file.
func (m *Module) Inspect(ctx context.Context, t module.Target, in module.InspectInput) (*module.ComponentDetails, error) {
	if in.Group == "" || in.Artifact == "" || in.Version == "" {
		return nil, fmt.Errorf("coordonnées incomplètes : groupId:artifactId:version attendus")
	}
	d := &module.ComponentDetails{Repository: t.Repo.Name, Format: t.Repo.Format, Type: t.Repo.Type,
		Group: in.Group, Artifact: in.Artifact, Version: in.Version, Kind: "release"}
	var assets []nexus.Asset
	versionDir := in.Version

	base, ts, n, isTS := SplitSnapshotVersion(in.Version)
	if isTS || IsSnapshot(in.Version) {
		d.Kind = "snapshot"
		wanted := in.Build
		if isTS {
			wanted = ts + "-" + fmt.Sprint(n)
		} else {
			base = in.Version[:len(in.Version)-len("-SNAPSHOT")]
		}
		builds, err := listBuilds(ctx, t.Client, t.Repo.Name, in.Group, in.Artifact, base)
		if err != nil {
			return nil, err
		}
		b, err := selectBuild(builds, wanted)
		if err != nil {
			return nil, err
		}
		for _, o := range builds {
			d.Builds = append(d.Builds, o.Key)
		}
		d.Build = b.Key
		d.Version = base + "-SNAPSHOT"
		versionDir = base + "-SNAPSHOT"
		for _, a := range b.Assets {
			assets = append(assets, a.Asset)
		}
	} else {
		c, err := findComponent(ctx, t, module.PromoteInput{Group: in.Group, Artifact: in.Artifact, Version: in.Version})
		if err != nil {
			return nil, err
		}
		assets = c.Assets
	}

	lookups := 0
	for _, a := range assets {
		gen := isGenerated(a.Path)
		if gen && !in.All {
			continue
		}
		f := module.FileDetail{Name: path.Base(a.Path), Path: a.Path, URL: t.Client.RepoURL(t.Repo.Name, a.Path),
			Size: a.FileSize, SHA1: a.Checksum["sha1"], MD5: a.Checksum["md5"], ContentType: a.ContentType,
			Modified: a.LastModified, Uploader: a.Uploader, Generated: gen}
		if (f.Size == 0 || f.Modified.IsZero()) && lookups < maxSizeLookups { // not every Nexus reports them in search results
			lookups++
			if n, mod, err := t.Client.HeadInfo(ctx, f.URL); err == nil {
				if f.Size == 0 && n > 0 {
					f.Size = n
				}
				if f.Modified.IsZero() {
					f.Modified = mod
				}
			}
		}
		d.TotalSize += f.Size
		if f.Modified.After(d.Published) {
			d.Published = f.Modified
		}
		if d.Uploader == "" {
			d.Uploader = a.Uploader
		}
		d.Files = append(d.Files, f)
	}
	if len(d.Files) == 0 {
		return nil, fmt.Errorf("%s:%s:%s sans fichier dans %s", in.Group, in.Artifact, in.Version, t.Repo.Alias)
	}
	if in.All {
		// the search API does not list checksum files or maven-metadata: look next to each file
		for _, f := range probeGenerated(ctx, t, d.Files, in.Group, in.Artifact, versionDir) {
			d.TotalSize += f.Size
			d.Files = append(d.Files, f)
		}
	}
	sort.Slice(d.Files, func(i, j int) bool { return d.Files[i].Name < d.Files[j].Name })
	d.DirectoryURL, d.BrowseURL = dirURLs(t, in.Group, in.Artifact, versionDir)
	return d, nil
}

// maxProbes bounds the HEAD requests made to discover checksum and metadata files.
const maxProbes = 200

// probeGenerated finds the .sha1/.md5 files next to files, and the maven-metadata.xml of the
// version directory (of the artifact directory when the version has none), with their checksums.
// Only files that really exist are returned. Metadata names would collide when downloading, so
// only one maven-metadata.xml is reported.
func probeGenerated(ctx context.Context, t module.Target, files []module.FileDetail, group, artifact, versionDir string) []module.FileDetail {
	have := map[string]bool{}
	for _, f := range files {
		have[f.Path] = true
	}
	var out []module.FileDetail
	probes := 0
	try := func(p string) bool {
		if have[p] || probes >= maxProbes {
			return false
		}
		probes++
		u := t.Client.RepoURL(t.Repo.Name, p)
		n, mod, err := t.Client.HeadInfo(ctx, u)
		if err != nil {
			return false
		}
		have[p] = true
		if n < 0 {
			n = 0
		}
		out = append(out, module.FileDetail{Name: path.Base(p), Path: p, URL: u, Size: n, Modified: mod, Generated: true})
		return true
	}
	for _, f := range files {
		if f.Generated {
			continue
		}
		try(f.Path + ".sha1")
		try(f.Path + ".md5")
	}
	gp := strings.ReplaceAll(group, ".", "/") + "/" + artifact
	meta := gp + "/" + versionDir + "/maven-metadata.xml"
	if !try(meta) {
		meta = gp + "/maven-metadata.xml"
		try(meta)
	}
	if have[meta] {
		try(meta + ".sha1")
		try(meta + ".md5")
	}
	return out
}
