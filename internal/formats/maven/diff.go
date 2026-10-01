package maven

import (
	"fmt"
	"strings"
)

// lineDiff lists the lines that differ between remote and expected: "- " for lines only in
// remote, "+ " for lines only in expected. It is a multiset comparison (order ignored), which
// is what matters for pom property/version changes. At most max lines are returned.
func lineDiff(remote, expected []byte, max int) []string {
	norm := func(b []byte) []string {
		var out []string
		for _, l := range strings.Split(string(b), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				out = append(out, l)
			}
		}
		return out
	}
	r, e := norm(remote), norm(expected)
	count := func(ls []string) map[string]int {
		m := map[string]int{}
		for _, l := range ls {
			m[l]++
		}
		return m
	}
	ec := count(e)
	var out []string
	for _, l := range r {
		if ec[l] > 0 {
			ec[l]--
			continue
		}
		out = append(out, "- "+l)
	}
	ec = count(e)
	rc2 := count(r)
	for _, l := range e {
		if rc2[l] > 0 {
			rc2[l]--
			continue
		}
		out = append(out, "+ "+l)
	}
	if len(out) > max {
		extra := len(out) - max
		out = append(out[:max], fmt.Sprintf("… %d autre(s) ligne(s)", extra))
	}
	return out
}
