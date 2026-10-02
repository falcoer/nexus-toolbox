// Package config manages ~/.nexus/config.yaml (no secrets inside).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Repo is a configured Nexus repository, referenced by its alias.
type Repo struct {
	URL    string `yaml:"url"`
	Base   string `yaml:"base"`       // Nexus root, e.g. https://host/nexus
	Name   string `yaml:"repository"` // repository name inside Nexus
	Format string `yaml:"format"`     // maven2, docker, npm, pypi...
	Type   string `yaml:"type"`       // hosted, proxy, group
	Policy string `yaml:"policy"`     // maven version policy: RELEASE, SNAPSHOT, MIXED
	User   string `yaml:"user"`
	Alias  string `yaml:"-"`
}

// Promotion is the default source/destination pair of `nexus promote`.
type Promotion struct {
	From string `yaml:"from,omitempty"`
	To   string `yaml:"to,omitempty"`
}

type Config struct {
	Repos     map[string]Repo `yaml:"repos"`
	Promotion Promotion       `yaml:"promotion,omitempty"`
	path      string
}

// Dir returns the config directory (NEXUS_HOME or ~/.nexus).
func Dir() (string, error) {
	if d := os.Getenv("NEXUS_HOME"); d != "" {
		return d, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".nexus"), nil
}

func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	c := &Config{Repos: map[string]Repo{}, path: filepath.Join(dir, "config.yaml")}
	b, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("%s: %w", c.path, err)
	}
	if c.Repos == nil {
		c.Repos = map[string]Repo{}
	}
	return c, nil
}

func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	b, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, b, 0o600)
}

// Get resolves an alias.
func (c *Config) Get(alias string) (Repo, error) {
	r, ok := c.Repos[alias]
	if !ok {
		return Repo{}, fmt.Errorf("dépôt %q inconnu (configurés : %s) — utilisez `nexus init`", alias, strings.Join(c.Aliases(), ", "))
	}
	r.Alias = alias
	return r, nil
}

func (c *Config) Aliases() []string {
	out := make([]string, 0, len(c.Repos))
	for k := range c.Repos {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ParseRepoURL splits https://host/nexus/repository/<name>/ into base and name.
func ParseRepoURL(raw string) (base, name string, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("URL invalide : %q", raw)
	}
	p := strings.Trim(u.Path, "/")
	i := strings.LastIndex("/"+p, "/repository/")
	if i < 0 {
		return "", "", fmt.Errorf("l'URL doit contenir /repository/<nom> : %q", raw)
	}
	prefix := strings.Trim(("/" + p)[:i], "/")
	rest := strings.Split(strings.Trim(("/" + p)[i+len("/repository/"):], "/"), "/")
	if rest[0] == "" {
		return "", "", fmt.Errorf("nom de repository manquant dans %q", raw)
	}
	base = u.Scheme + "://" + u.Host
	if prefix != "" {
		base += "/" + prefix
	}
	return base, rest[0], nil
}

// PromotionPair returns the default (snapshot, release) repositories for `nexus promote`:
// the pair saved with `nexus repos link`, else the only hosted maven2 repository of version
// policy SNAPSHOT together with the only one of policy RELEASE.
func (c *Config) PromotionPair() (from, to string, err error) {
	if c.Promotion.From != "" && c.Promotion.To != "" {
		for _, a := range []string{c.Promotion.From, c.Promotion.To} {
			if _, ok := c.Repos[a]; !ok {
				return "", "", fmt.Errorf("la paire par défaut référence %q, qui n'est plus configuré : `nexus repos link <snapshot> <release>`", a)
			}
		}
		return c.Promotion.From, c.Promotion.To, nil
	}
	var snaps, rels []string
	for _, a := range c.Aliases() {
		r := c.Repos[a]
		if r.Format != "maven2" || (r.Type != "" && r.Type != "hosted") {
			continue
		}
		switch r.EffectivePolicy(a) {
		case "SNAPSHOT":
			snaps = append(snaps, a)
		case "RELEASE":
			rels = append(rels, a)
		}
	}
	if len(snaps) == 1 && len(rels) == 1 {
		return snaps[0], rels[0], nil
	}
	return "", "", fmt.Errorf("dépôts source/destination non déterminables (snapshots : %s ; releases : %s) : enregistrez la paire une fois avec `nexus repos link <snapshot> <release>` (alias visibles avec `nexus repos list`), ou utilisez --from/--to",
		orNone(snaps), orNone(rels))
}

func orNone(l []string) string {
	if len(l) == 0 {
		return "aucun"
	}
	return strings.Join(l, ", ")
}

// EffectivePolicy is the Maven version policy of the repository: the one recorded by `nexus init`
// (SNAPSHOT, RELEASE or MIXED), and when Nexus did not report it, a guess from the names
// ("snapshot" or "release" in the alias, repository name or URL) so that conventionally named
// repositories work without configuration.
func (r Repo) EffectivePolicy(alias string) string {
	if p := strings.ToUpper(r.Policy); p != "" {
		return p
	}
	text := strings.ToLower(alias + " " + r.Name + " " + r.URL)
	switch snap, rel := strings.Contains(text, "snapshot"), strings.Contains(text, "release"); {
	case snap && !rel:
		return "SNAPSHOT"
	case rel && !snap:
		return "RELEASE"
	}
	return ""
}
