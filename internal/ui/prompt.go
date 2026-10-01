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
	s, err := bufio.NewReader(e.In).ReadString('\n')
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
