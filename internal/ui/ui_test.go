package ui

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func testEnv(width int) (*Env, *bytes.Buffer, *bytes.Buffer) {
	var out, err bytes.Buffer
	return &Env{Out: &out, Err: &err, Width: width, Height: 24, Flags: Flags{Output: "table"}}, &out, &err
}

var cols = []Column{{Title: "artifact"}, {Title: "version"}, {Title: "size", Right: true, Priority: 2}, {Title: "modifié", Priority: 1}}
var rows = []Row{
	{Cells: []string{"com.acme:quality-core", "1.4.2", "3.1 MiB", "il y a 2 j"}, Raw: map[string]string{"a": "1"}},
	{Cells: []string{"com.acme:quality-web", "2.0.0", "12.4 MiB", "il y a 1 h"}, Raw: map[string]string{"a": "2"}},
}

func TestTableNoANSIAndAligned(t *testing.T) {
	e, out, _ := testEnv(80)
	if err := e.List(context.Background(), &SliceSource{Cols: cols, Rows: rows}); err != nil {
		t.Fatal(err)
	}
	want := "  ARTIFACT               VERSION      SIZE  MODIFIÉ\n" +
		"  com.acme:quality-core  1.4.2     3.1 MiB  il y a 2 j\n" +
		"  com.acme:quality-web   2.0.0    12.4 MiB  il y a 1 h\n"
	if out.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", out.String(), want)
	}
	if strings.Contains(out.String(), "\x1b") {
		t.Error("ANSI in non-colour output")
	}
}

func TestNarrowDropsLowPriorityColumns(t *testing.T) {
	e, out, _ := testEnv(45)
	e.List(context.Background(), &SliceSource{Cols: cols, Rows: rows})
	first := strings.Split(out.String(), "\n")[0]
	if strings.Contains(first, "MODIFIÉ") || !strings.Contains(first, "ARTIFACT") {
		t.Errorf("unexpected header %q", first)
	}
}

func TestTruncation(t *testing.T) {
	e, out, _ := testEnv(24)
	e.List(context.Background(), &SliceSource{Cols: cols, Rows: rows})
	for _, l := range strings.Split(strings.TrimRight(out.String(), "\n"), "\n") {
		if len([]rune(l)) > 24 {
			t.Errorf("line too wide (%d): %q", len([]rune(l)), l)
		}
	}
}

func TestModes(t *testing.T) {
	e, out, errb := testEnv(80)
	e.Flags.Output = "ndjson"
	e.List(context.Background(), &SliceSource{Cols: cols, Rows: rows})
	if out.String() != "{\"a\":\"1\"}\n{\"a\":\"2\"}\n" {
		t.Errorf("ndjson: %q", out.String())
	}
	if !strings.Contains(errb.String(), "2 résultats") {
		t.Errorf("count missing: %q", errb.String())
	}
	e, out, _ = testEnv(80)
	e.Flags.Output = "plain"
	e.List(context.Background(), &SliceSource{Cols: cols, Rows: rows})
	if !strings.HasPrefix(out.String(), "com.acme:quality-core\t1.4.2\t") {
		t.Errorf("plain: %q", out.String())
	}
	e, out, _ = testEnv(80)
	e.Flags.Output = "json"
	e.List(context.Background(), &SliceSource{Cols: cols, Rows: rows})
	if !strings.Contains(out.String(), `"schema": 1`) {
		t.Errorf("json: %q", out.String())
	}
}

func TestColorAndASCII(t *testing.T) {
	e, _, _ := testEnv(80)
	if e.Success("x") != "x" {
		t.Error("colour must be off by default in tests")
	}
	e.Color = true
	if !strings.HasPrefix(e.Success("x"), "\x1b[32m") {
		t.Error("expected green")
	}
	e.ASCII = true
	if e.icon("✔", "[ok]") != "[ok]" {
		t.Error("ascii fallback")
	}
}

func TestHumanSize(t *testing.T) {
	if HumanSize(512) != "512 B" || HumanSize(3*1024*1024+100*1024) != "3.1 MiB" {
		t.Error(HumanSize(3*1024*1024 + 100*1024))
	}
}
