package ui

import (
	"bufio"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/term"
)

// ErrNoTTY is returned when a prompt is needed but no terminal is available.
var ErrNoTTY = errors.New("saisie interactive impossible (pas de terminal) : utilisez --yes ou les variables d'environnement")

// Line reads a visible line from the user.
func (e *Env) Line(label string) (string, error) {
	if !e.InTTY {
		return "", ErrNoTTY
	}
	fmt.Fprintf(e.Err, "%s ", label)
	if e.reader == nil { // one reader for the whole session: a new one per call would swallow buffered lines
		e.reader = bufio.NewReader(e.In)
	}
	s, err := e.reader.ReadString('\n')
	if err != nil && s != "" {
		err = nil // last line without a trailing newline
	}
	return strings.TrimSpace(s), err
}

// Secret reads a masked line.
func (e *Env) Secret(label string) (string, error) {
	if !e.InTTY {
		return "", ErrNoTTY
	}
	fmt.Fprintf(e.Err, "%s ", label)
	b, err := term.ReadPassword(int(e.In.Fd()))
	fmt.Fprintln(e.Err)
	return string(b), err
}

// Confirm asks "Continuer ? [o/N]" (default no). --yes skips the question.
func (e *Env) Confirm(question string) (bool, error) {
	if e.Flags.Yes {
		return true, nil
	}
	ans, err := e.Line(question + " [o/N]")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(ans) {
	case "o", "oui", "y", "yes":
		return true, nil
	}
	return false, nil
}

// ConfirmStrict asks "[o/N]" like Confirm but is NOT skipped by --yes: it guards decisions
// (such as publishing extra modules) that a generic "yes to everything" must not authorize.
func (e *Env) ConfirmStrict(question string) (bool, error) {
	ans, err := e.Line(question + " [o/N]")
	if err != nil {
		return false, err
	}
	switch strings.ToLower(ans) {
	case "o", "oui", "y", "yes":
		return true, nil
	}
	return false, nil
}

// Choose shows a numbered list and returns the index of the chosen option. An empty answer
// selects def (0-based). It needs a terminal.
func (e *Env) Choose(label string, options []string, def int) (int, error) {
	if len(options) == 0 {
		return 0, fmt.Errorf("%s : aucun choix possible", label)
	}
	if !e.InTTY {
		return 0, ErrNoTTY
	}
	fmt.Fprintf(e.Err, "%s\n", e.Heading(label))
	for i, o := range options {
		mark := " "
		if i == def {
			mark = e.Accent("›")
		}
		fmt.Fprintf(e.Err, " %s %2d  %s\n", mark, i+1, o)
	}
	for {
		ans, err := e.Line(fmt.Sprintf("Votre choix [%d] :", def+1))
		if err != nil {
			return 0, err
		}
		if ans == "" {
			return def, nil
		}
		n := 0
		if _, err := fmt.Sscanf(ans, "%d", &n); err == nil && n >= 1 && n <= len(options) {
			return n - 1, nil
		}
		fmt.Fprintf(e.Err, "  %s répondez par un numéro de 1 à %d\n", e.IconWarn(), len(options))
	}
}

// Ask reads a line with a default shown in brackets.
func (e *Env) Ask(label, def string) (string, error) {
	prompt := label
	if def != "" {
		prompt += " [" + def + "]"
	}
	ans, err := e.Line(prompt + " :")
	if err != nil {
		return "", err
	}
	if ans == "" {
		return def, nil
	}
	return ans, nil
}
