package maven

import (
	"strings"
	"testing"
)

func TestLineDiff(t *testing.T) {
	remote := "<a>1</a>\r\n<mw.version>2.10.10-0</mw.version>\r\n<same>x</same>\r\n"
	exp := "<a>1</a>\n<mw.version>2.10.10-0-SNAPSHOT</mw.version>\n<same>x</same>\n"
	d := lineDiff([]byte(remote), []byte(exp), 40)
	if strings.Join(d, "|") != "- <mw.version>2.10.10-0</mw.version>|+ <mw.version>2.10.10-0-SNAPSHOT</mw.version>" {
		t.Errorf("%v", d)
	}
	if len(lineDiff([]byte("a\nb"), []byte("b\na"), 5)) != 0 {
		t.Error("order must not matter")
	}
	many := strings.Repeat("x\n", 1) + strings.Repeat("l\n", 1)
	_ = many
	var b strings.Builder
	for i := 0; i < 50; i++ {
		b.WriteString("line" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + "\n")
	}
	d = lineDiff([]byte(b.String()), nil, 10)
	if len(d) != 11 || !strings.Contains(d[10], "40 autre(s)") {
		t.Errorf("truncation: %d %v", len(d), d[len(d)-1])
	}
}
