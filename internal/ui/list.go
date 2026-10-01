package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Source yields rows page by page (typically one Nexus continuationToken page per call).
type Source interface {
	Columns() []Column
	// Next returns the next batch; done=true when no more batches exist.
	Next(ctx context.Context) (rows []Row, done bool, err error)
}

// List renders a Source following the rules of docs/CLI-UX-GUIDELINES.md §6-7:
// json/ndjson/plain → flux; table on TTY → table or pager; table elsewhere → flux table.
func (e *Env) List(ctx context.Context, src Source) error {
	switch e.Mode() {
	case "json":
		return e.listJSON(ctx, src)
	case "ndjson":
		return e.stream(ctx, src, func(r Row) error {
			b, err := json.Marshal(r.Raw)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(e.Out, string(b))
			return err
		})
	case "plain":
		return e.stream(ctx, src, func(r Row) error {
			_, err := fmt.Fprintln(e.Out, strings.Join(r.Cells, "\t"))
			return err
		})
	}
	return e.listTable(ctx, src)
}

func (e *Env) stream(ctx context.Context, src Source, emit func(Row) error) error {
	n := 0
	for {
		rows, done, err := src.Next(ctx)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if err := emit(r); err != nil {
				return err
			}
			n++
		}
		if done {
			break
		}
	}
	e.count(n)
	return nil
}

func (e *Env) count(n int) {
	switch n {
	case 0:
		fmt.Fprintln(e.Err, e.Muted("0 résultat"))
	case 1:
		fmt.Fprintln(e.Err, e.Muted("1 résultat"))
	default:
		fmt.Fprintln(e.Err, e.Muted(fmt.Sprintf("%d résultats", n)))
	}
}

func (e *Env) listJSON(ctx context.Context, src Source) error {
	items := []any{}
	if err := e.stream(ctx, src, func(r Row) error { items = append(items, r.Raw); return nil }); err != nil {
		return err
	}
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"schema": 1, "items": items})
}

func (e *Env) listTable(ctx context.Context, src Source) error {
	cols := src.Columns()
	rows, done, err := src.Next(ctx)
	if err != nil {
		return err
	}
	pagerOK := e.OutTTY && e.InTTY && !e.Flags.NoPager && !e.Flags.Stream
	if pagerOK && (!done || len(rows) > e.Height-3) {
		return e.runPager(ctx, src, cols, rows, done)
	}
	if e.Flags.Stream || !e.OutTTY {
		// flux: header + widths from the first batch, then rows as they arrive.
		l := computeLayout(cols, rows, e.Width)
		fmt.Fprintln(e.Out, e.headerLine(cols, l))
		n := 0
		for {
			for _, r := range rows {
				fmt.Fprintln(e.Out, e.rowLine(cols, l, r))
				n++
			}
			if done {
				break
			}
			if rows, done, err = src.Next(ctx); err != nil {
				return err
			}
		}
		e.count(n)
		return nil
	}
	for !done { // TTY, pager disabled: drain then render
		var more []Row
		if more, done, err = src.Next(ctx); err != nil {
			return err
		}
		rows = append(rows, more...)
	}
	l := computeLayout(cols, rows, e.Width)
	if len(rows) > 0 {
		fmt.Fprintln(e.Out, e.headerLine(cols, l))
	}
	for _, r := range rows {
		fmt.Fprintln(e.Out, e.rowLine(cols, l, r))
	}
	e.count(len(rows))
	return nil
}

// SliceSource adapts an in-memory slice (used for small lists such as `repos list`).
type SliceSource struct {
	Cols []Column
	Rows []Row
	used bool
}

func (s *SliceSource) Columns() []Column { return s.Cols }
func (s *SliceSource) Next(context.Context) ([]Row, bool, error) {
	if s.used {
		return nil, true, nil
	}
	s.used = true
	return s.Rows, true, nil
}
