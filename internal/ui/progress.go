package ui

import (
	"fmt"
	"strings"
	"time"
)

// Bar prints a progress bar on stderr (one line, rewritten on TTY, silent otherwise until Done).
type Bar struct {
	e          *Env
	label      string
	total, cur int64
	last       time.Time // last repaint (throttled: downloads call Add for every chunk)
}

func (e *Env) NewBar(label string, total int64) *Bar { return &Bar{e: e, label: label, total: total} }

func (b *Bar) Add(n int64) {
	b.cur += n
	if !b.e.OutTTY || b.e.Flags.Output == "json" {
		return
	}
	if now := time.Now(); now.Sub(b.last) < 80*time.Millisecond && (b.total == 0 || b.cur < b.total) {
		return
	} else {
		b.last = now
	}
	width := 20
	filled := 0
	pct := 0
	if b.total > 0 {
		filled = int(b.cur * int64(width) / b.total)
		pct = int(b.cur * 100 / b.total)
	}
	if b.total <= 0 { // size unknown: bytes only
		fmt.Fprintf(b.e.Err, "\r  %s %s\x1b[K", b.label, HumanSize(b.cur))
		return
	}
	fill, empty := "█", "░"
	if b.e.ASCII {
		fill, empty = "#", "-"
	}
	fmt.Fprintf(b.e.Err, "\r  %s [%s%s] %3d%% %s/%s\x1b[K", b.label,
		strings.Repeat(fill, filled), strings.Repeat(empty, width-filled), pct, HumanSize(b.cur), HumanSize(b.total))
}

// Done terminates the bar line with a success or failure icon.
func (b *Bar) Done(err error) {
	icon := b.e.IconOK()
	if err != nil {
		icon = b.e.IconErr()
	}
	if b.e.OutTTY {
		fmt.Fprint(b.e.Err, "\r\x1b[K")
	}
	size := b.total
	if b.cur > size {
		size = b.cur
	}
	fmt.Fprintf(b.e.Err, "  %s %s %s\n", icon, b.label, b.e.Muted(HumanSize(size)))
}
