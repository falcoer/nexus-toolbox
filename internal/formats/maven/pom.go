package maven

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

// PomResult is the outcome of RewritePom.
type PomResult struct {
	Out        []byte
	Diff       []string // human readable list of changes
	Refs       []string // SNAPSHOT references still present after the rewrite
	UnusedPins []string
	HasVersion bool // the pom declares its own <version>
	Parent     *ParentRef
	Props      map[string]string // every <properties> entry, as found
}

// ParentRef is the <parent> declared by a pom (as found, before any pin).
type ParentRef struct{ Group, Artifact, Version string }

type edit struct {
	start, end int
	repl       string
}

// coordCtx collects the coordinates of a <parent>, <dependency> or <plugin> element.
type coordCtx struct {
	kind            string
	depth           int
	group, artifact string
	vStart, vEnd    int
	version         string
	hasVersion      bool
}

// RewritePom sets the project's own <version> to newVersion and applies pins to
// SNAPSHOT parent/dependency/plugin references. It only splices the bytes of the
// affected values, so formatting, comments and line endings are preserved.
func RewritePom(src []byte, newVersion string, pins []module.Pin) (*PomResult, error) {
	return RewritePomOpts(src, newVersion, pins, nil)
}

// RewritePomOpts is RewritePom with property overrides: a SNAPSHOT property listed in
// props (with a non-SNAPSHOT value) is replaced. An empty newVersion leaves the project
// version untouched (read-only use, e.g. to collect Props).
func RewritePomOpts(src []byte, newVersion string, pins []module.Pin, props map[string]string) (*PomResult, error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	dec.Strict = false
	var (
		path  []string
		ctxs  []*coordCtx
		edits []edit
		res   = &PomResult{Props: map[string]string{}}
		used  = map[int]bool{}
		prev  int
		// text capture for leaf elements we care about
		textStart, textEnd int
		textFor            string
	)
	label := func(c *coordCtx) string { return fmt.Sprintf("%s %s:%s", c.kind, c.group, c.artifact) }
	for {
		prev = int(dec.InputOffset())
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("pom illisible : %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			path = append(path, t.Name.Local)
			textFor = ""
			n := len(path)
			if n == 1 && t.Name.Local != "project" {
				return nil, fmt.Errorf("ce fichier n'est pas un pom (racine <%s>)", t.Name.Local)
			}
			switch {
			case n == 2 && path[0] == "project" && t.Name.Local == "parent":
				ctxs = append(ctxs, &coordCtx{kind: "parent", depth: n})
			case n >= 3 && path[n-2] == "dependencies" && t.Name.Local == "dependency":
				ctxs = append(ctxs, &coordCtx{kind: "dépendance", depth: n})
			case n >= 3 && path[n-2] == "plugins" && t.Name.Local == "plugin":
				ctxs = append(ctxs, &coordCtx{kind: "plugin", depth: n})
			}
			switch {
			case n == 2 && path[0] == "project" && t.Name.Local == "version":
				textFor = "project.version"
			case len(ctxs) > 0 && n == ctxs[len(ctxs)-1].depth+1:
				switch t.Name.Local {
				case "groupId", "artifactId", "version":
					textFor = "ctx." + t.Name.Local
				}
			case n == 3 && path[1] == "properties":
				textFor = "prop"
			}
		case xml.CharData:
			if textFor != "" && textEnd == 0 {
				textStart, textEnd = prev, int(dec.InputOffset())
				val := strings.TrimSpace(string(t))
				lead := len(t) - len(bytes.TrimLeft(t, " \t\r\n"))
				start, end := textStart+lead, textStart+lead+len(val)
				switch textFor {
				case "project.version":
					res.HasVersion = true
					if strings.Contains(val, "${") {
						return nil, fmt.Errorf("la version du projet est une propriété (%s) : promotion impossible, figez-la dans le pom", val)
					}
					if newVersion != "" && val != newVersion {
						edits = append(edits, edit{start, end, newVersion})
						res.Diff = append(res.Diff, fmt.Sprintf("version du projet : %s → %s", val, newVersion))
					}
				case "ctx.groupId":
					ctxs[len(ctxs)-1].group = val
				case "ctx.artifactId":
					ctxs[len(ctxs)-1].artifact = val
				case "ctx.version":
					c := ctxs[len(ctxs)-1]
					c.version, c.hasVersion, c.vStart, c.vEnd = val, true, start, end
				case "prop":
					name := path[len(path)-1]
					res.Props[name] = val
					if IsSnapshot(val) {
						if nv, ok := props[name]; ok && nv != "" && !IsSnapshot(nv) {
							edits = append(edits, edit{start, end, nv})
							res.Diff = append(res.Diff, fmt.Sprintf("propriété %s : %s → %s (valeur de la release existante)", name, val, nv))
						} else {
							res.Refs = append(res.Refs, fmt.Sprintf("propriété %s = %s", name, val))
						}
					}
				}
			}
		case xml.EndElement:
			textFor, textEnd = "", 0
			n := len(path)
			if len(ctxs) > 0 && ctxs[len(ctxs)-1].depth == n {
				c := ctxs[len(ctxs)-1]
				ctxs = ctxs[:len(ctxs)-1]
				if c.kind == "parent" {
					res.Parent = &ParentRef{c.group, c.artifact, c.version}
				}
				if c.hasVersion && IsSnapshot(c.version) {
					pinned := false
					for i, p := range pins {
						if p.Group == c.group && p.Artifact == c.artifact {
							edits = append(edits, edit{c.vStart, c.vEnd, p.Version})
							res.Diff = append(res.Diff, fmt.Sprintf("%s : %s → %s", label(c), c.version, p.Version))
							used[i], pinned = true, true
							break
						}
					}
					if !pinned {
						res.Refs = append(res.Refs, fmt.Sprintf("%s : %s", label(c), c.version))
					}
				}
			}
			path = path[:n-1]
		}
	}
	for i, p := range pins {
		if !used[i] {
			res.UnusedPins = append(res.UnusedPins, fmt.Sprintf("%s:%s=%s", p.Group, p.Artifact, p.Version))
		}
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := append([]byte(nil), src...)
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.repl), out[e.end:]...)...)
	}
	res.Out = out
	return res, nil
}
