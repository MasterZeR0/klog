// Package render writes parsed lines as pretty text, JSON or raw.
package render

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"klog/internal/filter"
	"klog/internal/parse"
	"klog/internal/theme"
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

// Options configures a Renderer. The zero value is pretty text in UTC, no
// colour, default theme.
type Options struct {
	Format    Format
	Color     bool
	TZ        *time.Location // nil = UTC
	NoFlatten bool           // pretty: keep JSON lines as raw JSON
	Theme     theme.Theme    // zero value (no Labels) = theme.Default()
}

type Renderer struct {
	w     io.Writer
	o     Options
	enc   *json.Encoder
	width int // widest "[label]" seen so far; only grows
}

func New(w io.Writer, o Options) *Renderer {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if o.TZ == nil {
		o.TZ = time.UTC
	}
	if len(o.Theme.Labels) == 0 {
		o.Theme = theme.Default()
	}
	return &Renderer{w: w, o: o, enc: enc}
}

type record struct {
	Source string         `json:"source"`
	Time   *time.Time     `json:"time,omitempty"`
	Raw    string         `json:"raw"`
	JSON   map[string]any `json:"json,omitempty"`
}

func (r *Renderer) Write(l parse.Line) error {
	switch r.o.Format {
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
	plain := "[" + l.Label + "]"
	n := utf8.RuneCountInString(plain)
	// ponytail: the width only grows, so early lines can be misaligned until the
	// longest label appears. Pre-scan the resolved streams in tail if that matters.
	if n > r.width {
		r.width = n
	}
	h := fnv.New32a()
	h.Write([]byte(l.Label))
	labels := r.o.Theme.Labels
	prefix := r.paint(labels[h.Sum32()%uint32(len(labels))], plain) + strings.Repeat(" ", r.width-n)
	ts := ""
	if !l.Time.IsZero() {
		ts = r.paint(r.o.Theme.Timestamp, l.Time.In(r.o.TZ).Format("15:04:05.000")) + " "
	}
	_, err := fmt.Fprintf(r.w, "%s %s%s\n", prefix, ts, r.body(l))
	return err
}

func (r *Renderer) paint(sgr, s string) string {
	if !r.o.Color {
		return s
	}
	return theme.Paint(sgr, s)
}

// body is the styled text after the label and timestamp.
func (r *Renderer) body(l parse.Line) string {
	if l.JSON == nil {
		if filter.IsContinuation(l.Raw) {
			return r.paint(r.traceStyle(l.Raw), l.Raw)
		}
		return l.Raw
	}
	lv, hasLevel := filter.LineLevel(l)
	if r.o.NoFlatten {
		if hasLevel {
			return r.paint(r.levelStyle(lv), l.Raw)
		}
		return l.Raw
	}
	return r.flatten(l, lv, hasLevel)
}

var levelNames = [...]string{
	filter.Trace: "TRACE", filter.Debug: "DEBUG", filter.Info: "INFO",
	filter.Warn: "WARN", filter.Error: "ERROR", filter.Fatal: "FATAL",
}

var msgKeys = []string{"msg", "message"}

// flatten renders a JSON line as "LEVEL msg  key=val ...".
func (r *Renderer) flatten(l parse.Line, lv filter.Level, hasLevel bool) string {
	msgKey, msg := "", ""
	for _, k := range msgKeys {
		if s, ok := l.JSON[k].(string); ok && s != "" {
			msgKey, msg = k, s
			break
		}
	}
	shown := !l.Time.IsZero() // kubectl's timestamp already covers the JSON time fields
	keys := make([]string, 0, len(l.JSON))
	for k := range l.JSON {
		switch {
		case msgKey != "" && k == msgKey:
			continue
		// ponytail: all three level keys go when one is recognised; a level and a
		// different severity on one line loses the severity. Rare; return the winning key if it matters.
		case hasLevel && (k == "level" || k == "severity" || k == "lvl"):
			continue
		case shown && (k == "time" || k == "ts" || k == "timestamp"):
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var head []string
	if hasLevel {
		head = append(head, r.paint(r.levelStyle(lv), levelNames[lv]))
	}
	if msg != "" {
		head = append(head, escapeText(msg))
	}
	out := strings.Join(head, " ")
	if len(keys) > 0 {
		pairs := make([]string, len(keys))
		for i, k := range keys {
			pairs[i] = r.paint(r.o.Theme.Keys, escapeText(k)+"=") + fmtValue(l.JSON[k])
		}
		if out != "" {
			out += "  "
		}
		out += strings.Join(pairs, " ")
	}
	if out == "" {
		return l.Raw
	}
	return out
}

// escapeText prints s with every non-printable rune (ESC, BEL, tab, newline,
// bidi overrides...) as a Go escape, so log content never reaches the terminal
// as a control sequence and one record stays one line.
func escapeText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != ' ' && !unicode.IsPrint(r) {
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// fmtValue prints one JSON value for a key=value pair.
func fmtValue(v any) string {
	switch x := v.(type) {
	case string:
		if x == "" || strings.ContainsAny(x, " \"") || strings.IndexFunc(x, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
			return strconv.Quote(x)
		}
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.Encode(v) // values came out of json.Decode, so Encode cannot fail
	return strings.TrimSuffix(buf.String(), "\n")
}

var omitted = regexp.MustCompile(`^\.\.\. \d+ (more|common frames omitted)`)

// traceStyle picks the style of a continuation line from its indent-trimmed
// text: Java indents nested "Suppressed:" and "... N more" with a tab.
func (r *Renderer) traceStyle(raw string) string {
	t := strings.TrimLeft(raw, " \t")
	switch {
	case strings.HasPrefix(t, "Caused by:"), strings.HasPrefix(t, "Suppressed:"):
		return r.o.Theme.Trace.CausedBy
	case omitted.MatchString(t):
		return r.o.Theme.Trace.Omitted
	}
	return r.o.Theme.Trace.Frame
}

func (r *Renderer) levelStyle(lv filter.Level) string {
	s := r.o.Theme.Levels
	switch lv {
	case filter.Trace:
		return s.Trace
	case filter.Debug:
		return s.Debug
	case filter.Info:
		return s.Info
	case filter.Warn:
		return s.Warn
	case filter.Error:
		return s.Error
	case filter.Fatal:
		return s.Fatal
	}
	return ""
}
