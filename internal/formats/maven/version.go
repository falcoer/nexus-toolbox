package maven

import (
	"strconv"
	"strings"
)

// CompareVersions is a simplified Maven version ordering:
// numeric segments compare numerically, qualifiers follow
// alpha < beta < milestone < rc < snapshot < (release) < sp.
func CompareVersions(a, b string) int {
	sa, sb := tokens(a), tokens(b)
	for i := 0; i < max(len(sa), len(sb)); i++ {
		var x, y string
		if i < len(sa) {
			x = sa[i]
		}
		if i < len(sb) {
			y = sb[i]
		}
		if c := cmpToken(x, y); c != 0 {
			return c
		}
	}
	return 0
}

func tokens(v string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, strings.ToLower(cur.String()))
			cur.Reset()
		}
	}
	isDigit := func(r byte) bool { return r >= '0' && r <= '9' }
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c == '.' || c == '-' || c == '_':
			flush()
		case cur.Len() > 0 && isDigit(cur.String()[0]) != isDigit(c):
			flush()
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

var qualRank = map[string]int{"alpha": -5, "a": -5, "beta": -4, "b": -4, "milestone": -3, "m": -3,
	"rc": -2, "cr": -2, "snapshot": -1, "": 0, "final": 0, "ga": 0, "release": 0, "sp": 1}

func cmpToken(x, y string) int {
	nx, ex := strconv.Atoi(x)
	ny, ey := strconv.Atoi(y)
	switch {
	case ex == nil && ey == nil:
		return sign(nx - ny)
	case x == "" && ey == nil: // missing segment == 0
		return sign(0 - ny)
	case y == "" && ex == nil:
		return sign(nx - 0)
	case ex == nil: // number beats qualifier
		return 1
	case ey == nil:
		return -1
	}
	rx, okx := qualRank[x]
	ry, oky := qualRank[y]
	if !okx {
		rx = 2 // unknown qualifiers sort after release
	}
	if !oky {
		ry = 2
	}
	if rx != ry {
		return sign(rx - ry)
	}
	return strings.Compare(x, y)
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	}
	return 0
}

// IsSnapshot reports a -SNAPSHOT version.
func IsSnapshot(v string) bool { return strings.HasSuffix(strings.ToUpper(v), "-SNAPSHOT") }
