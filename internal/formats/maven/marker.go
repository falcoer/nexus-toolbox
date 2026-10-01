package maven

import (
	"fmt"
	"strings"
	"time"

	"github.com/falcoer/nexus-toolbox/internal/module"
)

const markerClassifier = "promoted-from"

// BuildMarker renders the traceability file uploaded next to the promoted artifact.
func BuildMarker(plan *module.PromotePlan, srcURL, by string, now time.Time) []byte {
	var b strings.Builder
	w := func(k, v string) { fmt.Fprintf(&b, "%s: %s\n", k, v) }
	w("artifact", plan.Group+":"+plan.Artifact)
	w("source-repository", plan.Source)
	w("source-url", srcURL)
	w("source-version", plan.SourceVersion)
	if plan.SourceBuild != "" {
		w("source-build", plan.SourceBuild)
	}
	w("target-repository", plan.Destination)
	w("target-version", plan.TargetVersion)
	w("promoted-at", now.UTC().Format(time.RFC3339))
	w("promoted-by", by)
	if plan.Tool != "" {
		w("tool", plan.Tool)
	}
	b.WriteString("files:\n")
	for _, it := range plan.Items {
		if it.Kind == module.KindMarker {
			continue
		}
		note := "binaire inchangé"
		if it.Transformed {
			note = "réécrit : " + strings.Join(it.Diff, " ; ")
		}
		fmt.Fprintf(&b, "  - %s\n      source-sha1: %s\n      sha1: %s\n      %s\n", it.Path, it.SourceSHA1, it.SHA1, note)
	}
	return []byte(b.String())
}
