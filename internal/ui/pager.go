package ui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

type loadedMsg struct {
	rows []Row
	done bool
	err  error
}

type pager struct {
	e         *Env
	ctx       context.Context
	src       Source
	cols      []Column
	rows      []Row
	done      bool
	loading   bool
	err       error
	top       int
	w, h      int
	searching bool
	query     string
	input     string
	l         layout
}

func (e *Env) runPager(ctx context.Context, src Source, cols []Column, first []Row, done bool) error {
	m := &pager{e: e, ctx: ctx, src: src, cols: cols, rows: first, done: done, w: e.Width, h: e.Height}
	m.relayout()
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithContext(ctx))
	if _, err := p.Run(); err != nil && ctx.Err() == nil {
		return err
	}
	return m.err
}

func (m *pager) relayout() { m.l = computeLayout(m.cols, m.rows, m.w) }

func (m *pager) body() int { return max(1, m.h-2) }

func (m *pager) Init() tea.Cmd { return nil }

func (m *pager) load() tea.Cmd {
	if m.done || m.loading {
		return nil
	}
	m.loading = true
	return func() tea.Msg {
		rows, done, err := m.src.Next(m.ctx)
		return loadedMsg{rows, done, err}
	}
}

func (m *pager) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.relayout()
	case loadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.rows = append(m.rows, msg.rows...)
		m.done = msg.done
		m.relayout()
		return m, m.prefetch()
	case tea.KeyMsg:
		if m.searching {
			return m.updateSearch(msg)
		}
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "down", "j", "enter":
			m.scroll(1)
		case "up", "k":
			m.scroll(-1)
		case "pgdown", " ", "f":
			m.scroll(m.body())
		case "pgup", "b":
			m.scroll(-m.body())
		case "g", "home":
			m.top = 0
		case "G", "end":
			m.top = max(0, len(m.rows)-m.body())
			return m, m.load()
		case "/":
			m.searching, m.input = true, ""
		case "n":
			m.find(1)
		case "N":
			m.find(-1)
		}
		return m, m.prefetch()
	}
	return m, nil
}

// prefetch loads the next server page when the viewport nears the end.
func (m *pager) prefetch() tea.Cmd {
	if m.top+2*m.body() >= len(m.rows) {
		return m.load()
	}
	return nil
}

func (m *pager) scroll(d int) {
	m.top = min(max(0, m.top+d), max(0, len(m.rows)-m.body()))
}

func (m *pager) updateSearch(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEnter:
		m.searching, m.query = false, m.input
		m.find(1)
	case tea.KeyEsc:
		m.searching = false
	case tea.KeyBackspace:
		if len(m.input) > 0 {
			m.input = m.input[:len(m.input)-1]
		}
	case tea.KeyRunes, tea.KeySpace:
		m.input += string(k.Runes)
	}
	return m, nil
}

func (m *pager) find(dir int) {
	if m.query == "" || len(m.rows) == 0 {
		return
	}
	q := strings.ToLower(m.query)
	for i := 1; i <= len(m.rows); i++ {
		idx := ((m.top+dir*i)%len(m.rows) + len(m.rows)) % len(m.rows)
		if strings.Contains(strings.ToLower(strings.Join(m.rows[idx].Cells, " ")), q) {
			m.top = min(idx, max(0, len(m.rows)-m.body()))
			return
		}
	}
}

func (m *pager) View() string {
	var b strings.Builder
	b.WriteString(ansi.Truncate(m.e.headerLine(m.cols, m.l), m.w, "") + "\n")
	end := min(len(m.rows), m.top+m.body())
	for i := m.top; i < end; i++ {
		b.WriteString(m.e.rowLine(m.cols, m.l, m.rows[i]) + "\n")
	}
	for i := end - m.top; i < m.body(); i++ {
		b.WriteString("\n")
	}
	status := fmt.Sprintf("%d–%d / %d", min(m.top+1, len(m.rows)), end, len(m.rows))
	if !m.done {
		status += "+"
	}
	if m.loading {
		status += "  chargement" + m.e.Ellipsis()
	}
	if m.searching {
		status = "/" + m.input
	} else {
		status += "   (↑↓ défiler · espace page · / chercher · q quitter)"
	}
	b.WriteString(m.e.Muted(ansi.Truncate(status, m.w, "")))
	return b.String()
}
