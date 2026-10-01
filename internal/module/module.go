// Package module defines the per-format plug-in model. A module implements the
// capability interfaces (Searcher, Promoter, …) it supports; commands dispatch on
// the repository format and report unsupported actions explicitly.
//
// Inputs and outputs are plain serialisable structs so that a future MCP server can
// expose the same actions without touching the terminal layer.
package module

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/falcoer/nexus-toolbox/internal/config"
	"github.com/falcoer/nexus-toolbox/internal/nexus"
)

// Target is a configured repository bound to an authenticated client.
type Target struct {
	Repo   config.Repo
	Client *nexus.Client
}

// Module is a plug-in for one repository format ("maven2", "docker", …).
type Module interface {
	Format() string
}

// Iterator yields results page by page.
type Iterator[T any] interface {
	Next(ctx context.Context) (items []T, done bool, err error)
}

// ---- search ----

type SearchInput struct {
	Query       string `json:"query,omitempty"`
	Group       string `json:"group,omitempty"`
	Artifact    string `json:"artifact,omitempty"`
	Version     string `json:"version,omitempty"`
	FromVersion string `json:"from_version,omitempty"`
	ToVersion   string `json:"to_version,omitempty"`
	Sort        string `json:"sort,omitempty"` // group|name|version
	Limit       int    `json:"limit,omitempty"`
}

type Hit struct {
	ID         string    `json:"id"`
	Repository string    `json:"repository"`
	Group      string    `json:"group"`
	Artifact   string    `json:"artifact"`
	Version    string    `json:"version"`
	Assets     int       `json:"assets"`
	Size       int64     `json:"size"`
	Modified   time.Time `json:"modified,omitzero"`
}

type Searcher interface {
	Search(ctx context.Context, t Target, in SearchInput) (Iterator[Hit], error)
}

// ---- promote ----

// Pin rewrites a SNAPSHOT reference (parent or dependency) of the promoted pom.
type Pin struct {
	Group    string `json:"group"`
	Artifact string `json:"artifact"`
	Version  string `json:"version"`
}

type PromoteInput struct {
	Group             string            `json:"group"`
	Artifact          string            `json:"artifact"`
	Version           string            `json:"version"`              // 1.2.3, 1.2.3-SNAPSHOT or 1.2.3-20260914.070210-43
	AsVersion         string            `json:"as_version,omitempty"` // target version (default: source without -SNAPSHOT)
	Build             string            `json:"build,omitempty"`      // snapshot build: "43" or "20260914.070210-43"
	Pins              []Pin             `json:"pins,omitempty"`
	AllowSnapshotRefs bool              `json:"allow_snapshot_refs,omitempty"`
	NoMarker          bool              `json:"no_marker,omitempty"`
	SetProperties     map[string]string `json:"set_properties,omitempty"`     // --set-property name=value
	ReleaseProperties bool              `json:"release_properties,omitempty"` // strip -SNAPSHOT from remaining SNAPSHOT properties
	AlignProperties   bool              `json:"align_properties,omitempty"`   // take SNAPSHOT property values from the pom already released at the target
	WithParent        bool              `json:"with_parent,omitempty"`        // also promote SNAPSHOT parent poms, ancestors first
	Ancestors         []string          `json:"-"`                            // group:artifact chain being planned (cycle/depth guard)
	Force             bool              `json:"force,omitempty"`
	DeleteSource      bool              `json:"delete_source,omitempty"`
	Tool              string            `json:"-"` // "nexus-toolbox x.y.z", written in the marker file
}

const (
	KindFile   = "file"
	KindMarker = "marker"
)

type PlanItem struct {
	Kind             string    `json:"kind"`
	SourcePath       string    `json:"source_path,omitempty"`
	Path             string    `json:"path"` // destination path
	Size             int64     `json:"size"`
	SourceSHA1       string    `json:"source_sha1,omitempty"`
	RemoteSHA1       string    `json:"remote_sha1,omitempty"`       // what the destination already holds (conflict)
	MatchBuild       string    `json:"matches_build,omitempty"`     // source build identical to RemoteSHA1
	MissingChecksums bool      `json:"missing_checksums,omitempty"` // identical file present without .sha1/.md5: they will be added
	MD5              string    `json:"-"`
	DiffLines        []string  `json:"conflict_diff,omitempty"` // pom conflict: remote (-) vs expected (+)
	RemoteSize       int64     `json:"remote_size,omitempty"`
	RemoteModified   time.Time `json:"remote_modified,omitzero"`
	SHA1             string    `json:"sha1"` // expected sha1 in the destination
	Transformed      bool      `json:"transformed,omitempty"`
	Diff             []string  `json:"diff,omitempty"`
	Action           string    `json:"action"` // copy | skip | conflict
	Reason           string    `json:"reason,omitempty"`
	DownloadURL      string    `json:"-"`
	Content          []byte    `json:"-"` // in-memory content (rewritten pom)
}

type PromotePlan struct {
	Group             string         `json:"group"`
	Artifact          string         `json:"artifact"`
	Version           string         `json:"version"` // as requested
	SourceVersion     string         `json:"source_version"`
	OriginVersion     string         `json:"origin_version,omitempty"` // exact source version, e.g. 1.0-20260930.120201-1 (used in the marker name)
	SourceBuild       string         `json:"source_build,omitempty"`
	Builds            []string       `json:"available_builds,omitempty"`
	TargetVersion     string         `json:"target_version"`
	Source            string         `json:"source"`
	Destination       string         `json:"destination"`
	SourceID          string         `json:"source_component_id,omitempty"`
	Parents           []*PromotePlan `json:"parents,omitempty"` // SNAPSHOT parents to promote first (highest ancestor first)
	WritePolicy       string         `json:"destination_write_policy,omitempty"`
	PinsUsed          []string       `json:"-"`               // pins that changed a pom of the chain
	PropsUsed         []string       `json:"-"`               // --set-property names found in a pom of the chain
	Notes             []string       `json:"notes,omitempty"` // diagnostics (provenance of an existing release…)
	Items             []PlanItem     `json:"items"`
	SnapshotRefs      []string       `json:"unresolved_snapshot_refs,omitempty"`
	AllowSnapshotRefs bool           `json:"allow_snapshot_refs,omitempty"`
	Force             bool           `json:"force"`
	DeleteSource      bool           `json:"delete_source"`
	Tool              string         `json:"-"`
	Warnings          []string       `json:"warnings,omitempty"`
}

// Blocking returns the items that prevent the promotion (conflicts without --force),
// parents included.
func (p *PromotePlan) Blocking() []PlanItem {
	var out []PlanItem
	for _, par := range p.Parents {
		out = append(out, par.Blocking()...)
	}
	for _, it := range p.Items {
		if it.Action == "conflict" {
			out = append(out, it)
		}
	}
	return out
}

// BlockingRefs returns SNAPSHOT references that forbid the promotion, parents included.
func (p *PromotePlan) BlockingRefs() []string {
	var out []string
	for _, par := range p.Parents {
		out = append(out, par.BlockingRefs()...)
	}
	if !p.AllowSnapshotRefs {
		out = append(out, p.SnapshotRefs...)
	}
	return out
}

type PromoteResult struct {
	Copied         int      `json:"copied"`
	Skipped        int      `json:"skipped"`
	Failed         []string `json:"failed,omitempty"`
	Verified       bool     `json:"verified"`
	ChecksumsAdded int      `json:"checksums_added,omitempty"` // files that received their .sha1/.md5
	Published      []string `json:"published,omitempty"`       // files verified in the destination (copied now or already identical)
	MarkerWritten  bool     `json:"marker_written"`
	MetadataOK     bool     `json:"metadata_ok"`
	SourceDeleted  bool     `json:"source_deleted"`
	Warnings       []string `json:"warnings,omitempty"`
}

// Reporter receives progress events (CLI draws bars; MCP could emit notifications).
type Reporter interface {
	AssetStart(path string, size int64)
	AssetBytes(n int64)
	AssetDone(path string, err error)
}

// ErrPartial is returned with a PromoteResult when only part of the assets was promoted.
var ErrPartial = errors.New("promotion partielle")

type Promoter interface {
	PlanPromote(ctx context.Context, src, dst Target, in PromoteInput) (*PromotePlan, error)
	ExecutePromote(ctx context.Context, src, dst Target, plan *PromotePlan, rep Reporter) (*PromoteResult, error)
}

// ---- registry ----

type Registry struct{ mods map[string]Module }

func NewRegistry(mods ...Module) *Registry {
	r := &Registry{mods: map[string]Module{}}
	for _, m := range mods {
		r.mods[m.Format()] = m
	}
	return r
}

func (r *Registry) For(format string) (Module, error) {
	m, ok := r.mods[format]
	if !ok {
		return nil, fmt.Errorf("format %q non supporté (disponibles : %v)", format, r.Formats())
	}
	return m, nil
}

func (r *Registry) Formats() []string {
	out := make([]string, 0, len(r.mods))
	for k := range r.mods {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Capabilities lists the action names a module supports.
func Capabilities(m Module) []string {
	var out []string
	if _, ok := m.(Searcher); ok {
		out = append(out, "search")
	}
	if _, ok := m.(Promoter); ok {
		out = append(out, "promote")
	}
	return out
}

// Unsupported builds the standard "action not available" error.
func Unsupported(action, format string) error {
	return fmt.Errorf("l'action %q n'est pas disponible pour le format %s", action, format)
}
