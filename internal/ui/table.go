package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Column describes one table column.
type Column struct {
	Title string
	Right bool // right-aligned (numbers)
	// Priority 0 is essential; higher values are hidden first when the terminal is narrow.
	Priority int
	Style    func(*Env, string) string
}

// Row is one result: cells for display, Raw for json/ndjson output.
type Row struct {
	Cells []string
	Raw   any
}

const indent = "  "

type layout struct {
	cols   []int // indexes of visible columns
	widths []int // matching widths
}

// computeLayout picks visible columns and widths for the given terminal width.
func computeLayout(cols []Column, rows []Row, width int) layout {
	nat := make([]int, len(cols))
	for i, c := range cols {
		nat[i] = ansi.StringWidth(c.Title)
	}
	for _, r := range rows {
		for i, c := range r.Cells {
			if i < len(nat) {
				if w := ansi.StringWidth(c); w > nat[i] {
					nat[i] = w
				}
			}
		}
	}
	visible := make([]int, len(cols))
	for i := range cols {
		visible[i] = i
	}
	total := func() int {
		t := len(indent)
		for _, i := range visible {
			t += nat[i] + 2
		}
		return t
	}
	for total() > width {
		drop, best := -1, 0
		for k, i := range visible {
			if cols[i].Priority >= best && cols[i].Priority > 0 {
				drop, best = k, cols[i].Priority
			}
		}
		if drop < 0 {
			break
		}
		visible = append(visible[:drop], visible[drop+1:]...)
	}
	for total() > width { // shrink the widest column
		wi, ww := -1, 8
		for _, i := range visible {
			if nat[i] > ww {
				wi, ww = i, nat[i]
			}
		}
		if wi < 0 {
			break
		}
		nat[wi]--
	}
	l := layout{cols: visible}
	for _, i := range visible {
		l.widths = append(l.widths, nat[i])
	}
	return l
}

func (e *Env) fit(s string, w int, right bool) string {
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, e.Ellipsis())
	}
	pad := strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
	if right {
		return pad + s
	}
	return s + pad
}

func (e *Env) headerLine(cols []Column, l layout) string {
	var b strings.Builder
	b.WriteString(indent)
	for k, i := range l.cols {
		b.WriteString(e.Heading(e.fit(strings.ToUpper(cols[i].Title), l.widths[k], cols[i].Right)))
		b.WriteString("  ")
	}
	return strings.TrimRight(b.String(), " ")
}

func (e *Env) rowLine(cols []Column, l layout, r Row) string {
	var b strings.Builder
	b.WriteString(indent)
	for k, i := range l.cols {
		cell := ""
		if i < len(r.Cells) {
			cell = r.Cells[i]
		}
		txt := e.fit(cell, l.widths[k], cols[i].Right)
		if st := cols[i].Style; st != nil {
			txt = st(e, txt)
		}
		b.WriteString(txt)
		b.WriteString("  ")
	}
	return strings.TrimRight(b.String(), " ")
}
