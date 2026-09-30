// Package stats counts lines per source and level for fetch --stats.
package stats

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

	"klog/internal/filter"
	"klog/internal/parse"
)

// names are the columns: one per filter.Level (Level-1), then "?" for lines
// without a recognised level.
var names = [...]string{"TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL", "?"}

type row [len(names)]int

func (r row) total() (n int) {
	for _, c := range r {
		n += c
	}
	return n
}

// Table is a per-source x level count. It is not safe for concurrent use.
type Table struct{ rows map[string]*row }

func New() *Table { return &Table{rows: map[string]*row{}} }

// Add counts l under its label.
func (t *Table) Add(l parse.Line) {
	r := t.rows[l.Label]
	if r == nil {
		r = &row{}
		t.rows[l.Label] = r
	}
	i := len(names) - 1
	if lv, ok := filter.LineLevel(l); ok {
		i = int(lv) - 1
	}
	r[i]++
}

func (t *Table) sources() []string {
	out := make([]string, 0, len(t.rows))
	for s := range t.rows {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// WriteText prints an aligned table sorted by source, with a TOTAL row and column.
func (t *Table) WriteText(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprint(tw, "SOURCE")
	for _, n := range names {
		fmt.Fprintf(tw, "\t%s", n)
	}
	fmt.Fprint(tw, "\tTOTAL\n")
	line := func(name string, r row) {
		fmt.Fprint(tw, name)
		for _, c := range r {
			fmt.Fprintf(tw, "\t%d", c)
		}
		fmt.Fprintf(tw, "\t%d\n", r.total())
	}
	var sum row
	for _, s := range t.sources() {
		line(s, *t.rows[s])
		for i, c := range t.rows[s] {
			sum[i] += c
		}
	}
	line("TOTAL", sum)
	return tw.Flush()
}

// WriteJSON prints one object per source, sorted by source. Every level key is present.
func (t *Table) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, s := range t.sources() {
		r := t.rows[s]
		counts := make(map[string]int, len(names))
		for i, n := range names {
			counts[n] = r[i]
		}
		rec := struct {
			Source string         `json:"source"`
			Counts map[string]int `json:"counts"`
			Total  int            `json:"total"`
		}{s, counts, r.total()}
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}
