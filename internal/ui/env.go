// Package ui implements the terminal rendering rules of docs/CLI-UX-GUIDELINES.md.
// It is the only package (with cmd) allowed to write to the terminal.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// Flags are the global presentation flags shared by every command.
type Flags struct {
	Output  string // table|plain|json|ndjson ("" = auto)
	NoColor bool
	ASCII   bool
	NoPager bool
	Stream  bool
	Yes     bool
	Verbose int
}

// Env describes the terminal the CLI talks to.
type Env struct {
	Out, Err io.Writer
	In       *os.File
	OutTTY   bool
	InTTY    bool
	Color    bool
	ASCII    bool
	Width    int
	Height   int
	Flags    Flags
	reader   *bufio.Reader
}

// Detect inspects the process environment and resolves the presentation mode.
func Detect(f Flags) *Env {
	e := &Env{Out: os.Stdout, Err: os.Stderr, In: os.Stdin, Flags: f, Width: 80, Height: 24}
	e.OutTTY = term.IsTerminal(int(os.Stdout.Fd()))
	e.InTTY = term.IsTerminal(int(os.Stdin.Fd()))
	if e.OutTTY {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
			e.Width, e.Height = w, h
		}
	}
	e.Color = e.OutTTY
	if os.Getenv("CLICOLOR_FORCE") == "1" {
		e.Color = true
	}
	if f.NoColor || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		e.Color = false
	}
	e.ASCII = f.ASCII || !utf8Locale()
	if os.Getenv("CI") == "true" {
		e.InTTY = false
	}
	return e
}

func utf8Locale() bool {
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		if v := os.Getenv(k); v != "" {
			v = strings.ToUpper(v)
			return strings.Contains(v, "UTF-8") || strings.Contains(v, "UTF8")
		}
	}
	return true
}

// Mode resolves the effective output format.
func (e *Env) Mode() string {
	if e.Flags.Output != "" {
		return e.Flags.Output
	}
	if e.OutTTY {
		return "table"
	}
	return "plain"
}

// Machine reports whether the output is meant for programs (json/ndjson/plain).
func (e *Env) Machine() bool { return e.Mode() != "table" }

// Verbosef prints debug lines on stderr when -v is set.
func (e *Env) Verbosef(level int, format string, a ...any) {
	if e.Flags.Verbose >= level {
		fmt.Fprintf(e.Err, e.Muted("  "+format)+"\n", a...)
	}
}
