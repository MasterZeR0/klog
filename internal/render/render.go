// Package render writes parsed lines as pretty text, JSON or raw.
package render

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"time"

	"klog/internal/parse"
)

type Format int

const (
	Pretty Format = iota
	JSON
	Raw
)

func ParseFormat(s string) (Format, error) {
	switch s {
	case "pretty":
		return Pretty, nil
	case "json":
		return JSON, nil
	case "raw":
		return Raw, nil
	}
	return 0, fmt.Errorf("unknown --format %q (want pretty, json or raw)", s)
}

type Renderer struct {
	w     io.Writer
	f     Format
	color bool
	enc   *json.Encoder
}

func New(w io.Writer, f Format, color bool) *Renderer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Renderer{w: w, f: f, color: color, enc: enc}
}

type record struct {
	Source string         `json:"source"`
	Time   *time.Time     `json:"time,omitempty"`
	Raw    string         `json:"raw"`
	JSON   map[string]any `json:"json,omitempty"`
}

var palette = []int{36, 32, 33, 35, 34, 31}

func (r *Renderer) Write(l parse.Line) error {
	switch r.f {
	case Raw:
		_, err := fmt.Fprintln(r.w, l.Raw)
		return err
	case JSON:
		rec := record{Source: l.Label, Raw: l.Raw, JSON: l.JSON}
		if !l.Time.IsZero() {
			rec.Time = &l.Time
		}
		return r.enc.Encode(rec) // Encode appends the newline
	}
	prefix := "[" + l.Label + "]"
	if r.color {
		h := fnv.New32a()
		h.Write([]byte(l.Label))
		prefix = fmt.Sprintf("\x1b[%dm%s\x1b[0m", palette[h.Sum32()%uint32(len(palette))], prefix)
	}
	ts := ""
	if !l.Time.IsZero() {
		ts = l.Time.UTC().Format("15:04:05.000") + " "
	}
	_, err := fmt.Fprintf(r.w, "%s %s%s\n", prefix, ts, l.Raw)
	return err
}
