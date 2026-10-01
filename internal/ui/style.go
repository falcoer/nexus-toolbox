package ui

import "fmt"

type role int

const (
	roleSuccess role = iota
	roleError
	roleWarning
	roleInfo
	roleAccent
	roleMuted
	roleHeading
)

var roleCode = map[role]string{
	roleSuccess: "32", roleError: "31", roleWarning: "33", roleInfo: "36",
	roleAccent: "35", roleMuted: "90", roleHeading: "1",
}

func (e *Env) paint(r role, s string) string {
	if !e.Color || s == "" {
		return s
	}
	return "\x1b[" + roleCode[r] + "m" + s + "\x1b[0m"
}

func (e *Env) Success(s string) string { return e.paint(roleSuccess, s) }
func (e *Env) Error(s string) string   { return e.paint(roleError, s) }
func (e *Env) Warning(s string) string { return e.paint(roleWarning, s) }
func (e *Env) Info(s string) string    { return e.paint(roleInfo, s) }
func (e *Env) Accent(s string) string  { return e.paint(roleAccent, s) }
func (e *Env) Muted(s string) string   { return e.paint(roleMuted, s) }
func (e *Env) Heading(s string) string { return e.paint(roleHeading, s) }

// Icons (Unicode with ASCII fallback).
func (e *Env) icon(uni, ascii string) string {
	if e.ASCII {
		return ascii
	}
	return uni
}

func (e *Env) IconOK() string   { return e.Success(e.icon("✔", "[ok]")) }
func (e *Env) IconErr() string  { return e.Error(e.icon("✖", "[err]")) }
func (e *Env) IconWarn() string { return e.Warning(e.icon("▲", "[warn]")) }
func (e *Env) IconInfo() string { return e.Info(e.icon("ℹ", "[i]")) }
func (e *Env) Arrow() string    { return e.icon("→", "->") }
func (e *Env) Ellipsis() string { return e.icon("…", "...") }

// Messages go to stderr (stdout is reserved for data).
func (e *Env) Successf(f string, a ...any) {
	fmt.Fprintf(e.Err, "%s %s\n", e.IconOK(), fmt.Sprintf(f, a...))
}
func (e *Env) Warnf(f string, a ...any) {
	fmt.Fprintf(e.Err, "%s %s\n", e.IconWarn(), fmt.Sprintf(f, a...))
}
func (e *Env) Infof(f string, a ...any) {
	fmt.Fprintf(e.Err, "%s %s\n", e.IconInfo(), fmt.Sprintf(f, a...))
}

// Failure prints an error in the "what / why / what to do" format.
func (e *Env) Failure(what, why, hint string) {
	fmt.Fprintf(e.Err, "%s %s\n", e.IconErr(), e.Error(what))
	if why != "" {
		fmt.Fprintf(e.Err, "  %s\n", why)
	}
	if hint != "" {
		fmt.Fprintf(e.Err, "  %s %s\n", e.Arrow(), hint)
	}
}
