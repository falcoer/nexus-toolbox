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
	Props      map[string]string      // every <properties> entry, as found
	Changed    map[string]string      // properties rewritten: name → new value
	PropUsers  map[string][]ParentRef // property → parent/dependency/plugin coordinates using ${property} as version
	SetUsed    []string               // --set-property names found in this pom
	Needs      []Need                 // SNAPSHOT modules this pom still depends on (see Need)
}

// Need is a SNAPSHOT module the pom refers to: its parent, a dependency/plugin written with a
// SNAPSHOT version, or a dependency whose version comes from a SNAPSHOT property.
type Need struct {
	Kind     string // parent | dependency | plugin | property
	Group    string // may be ${project.groupId}
	Artifact string // empty for a property that no dependency uses
	Version  string // the SNAPSHOT version found
	Property string // name of the property (Kind == "property")
}

// PomOpts tunes property rewriting. Precedence: Set > Aligned > Strip.
type PomOpts struct {
	Set           map[string]string // explicit values (--set-property), applied whatever the current value
	Aligned       map[string]string // values taken from the release already published (--align-properties)
	StripSnapshot bool              // --release-properties: drop "-SNAPSHOT" from remaining SNAPSHOT properties
	Targets       map[string]string // property → version of the module it designates (resolved by the promotion plan)
	StripUnlinked bool              // drop "-SNAPSHOT" from SNAPSHOT properties no dependency uses as a version
}

// ParentRef is the <parent> declared by a pom (as found, before any pin).
type ParentRef struct{ Group, Artifact, Version string }

// propSpan is one <properties> entry with the byte range of its value.
type propSpan struct {
	name, val  string
	start, end int
}

type edit struct {
	start, end int
	repl       string
}

// coordCtx collects the coordinates of a <parent>, <dependency> or <plugin> element.
type coordCtx struct {
	kind            string
	key             string // parent | dependency | plugin
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
	return RewritePomOpts(src, newVersion, pins, PomOpts{})
}

// RewritePomOpts is RewritePom with property rewriting (see PomOpts). An empty newVersion
// leaves the project version untouched (read-only use, e.g. to collect Props).
func RewritePomOpts(src []byte, newVersion string, pins []module.Pin, opts PomOpts) (*PomResult, error) {
	dec := xml.NewDecoder(bytes.NewReader(src))
	dec.Strict = false
	var (
		path  []string
		ctxs  []*coordCtx
		edits []edit
		res   = &PomResult{Props: map[string]string{}, Changed: map[string]string{}, PropUsers: map[string][]ParentRef{}}
		used  = map[int]bool{}
		prev  int
		props []propSpan
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
				ctxs = append(ctxs, &coordCtx{kind: "parent", key: "parent", depth: n})
			case n >= 3 && path[n-2] == "dependencies" && t.Name.Local == "dependency":
				ctxs = append(ctxs, &coordCtx{kind: "dépendance", key: "dependency", depth: n})
			case n >= 3 && path[n-2] == "plugins" && t.Name.Local == "plugin":
				ctxs = append(ctxs, &coordCtx{kind: "plugin", key: "plugin", depth: n})
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
					props = append(props, propSpan{name, val, start, end})
				}
			}
		case xml.EndElement:
			textFor, textEnd = "", 0
			n := len(path)
			if len(ctxs) > 0 && ctxs[len(ctxs)-1].depth == n {
				c := ctxs[len(ctxs)-1]
				ctxs = ctxs[:len(ctxs)-1]
				if v := strings.TrimSpace(c.version); strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") {
					name := v[2 : len(v)-1]
					res.PropUsers[name] = append(res.PropUsers[name], ParentRef{Group: c.group, Artifact: c.artifact})
				}
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
					// pinned or not, the module must exist in the destination (or be promoted first)
					res.Needs = append(res.Needs, Need{Kind: c.key, Group: c.group, Artifact: c.artifact, Version: c.version})
				}
			}
			path = path[:n-1]
		}
	}
	// Properties are settled once every usage is known (dependencies may come after <properties>).
	for _, sp := range props {
		name, val := sp.name, sp.val
		apply := func(nv, why string) {
			if nv != val {
				edits = append(edits, edit{sp.start, sp.end, nv})
				res.Changed[name] = nv
				res.Diff = append(res.Diff, fmt.Sprintf("propriété %s : %s → %s (%s)", name, val, nv, why))
			}
			if IsSnapshot(nv) {
				res.Refs = append(res.Refs, fmt.Sprintf("propriété %s = %s", name, nv))
			}
		}
		if nv, ok := opts.Set[name]; ok {
			res.SetUsed = append(res.SetUsed, name)
			apply(nv, "--set-property")
			continue
		}
		if !IsSnapshot(val) {
			continue
		}
		stripped := val[:len(val)-len("-SNAPSHOT")]
		switch nv, ok := opts.Aligned[name]; {
		case ok && nv != "" && !IsSnapshot(nv):
			apply(nv, "valeur de la release existante")
		case opts.Targets[name] != "":
			apply(opts.Targets[name], "version du module requis")
		case opts.StripSnapshot:
			apply(stripped, "-SNAPSHOT retiré")
		case opts.StripUnlinked && len(res.PropUsers[name]) == 0:
			apply(stripped, "propriété liée à aucun artifact, -SNAPSHOT retiré")
		default:
			res.Refs = append(res.Refs, fmt.Sprintf("propriété %s = %s", name, val))
			if users := res.PropUsers[name]; len(users) > 0 {
				for _, u := range users {
					res.Needs = append(res.Needs, Need{Kind: "property", Group: u.Group, Artifact: u.Artifact, Version: val, Property: name})
				}
			} else {
				res.Needs = append(res.Needs, Need{Kind: "property", Version: val, Property: name})
			}
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
