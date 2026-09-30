# klog Pretty Output Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `--format pretty` readable: coloured levels, JSON lines flattened to `LEVEL msg key=val`, styled stack traces, aligned labels, a `--no-flatten` flag and a `--theme` config file.

**Architecture:** A new `internal/theme` package holds SGR colour strings and loads/validates a JSON theme file. `render` takes an `Options` struct (format, colour, timezone, no-flatten, theme) and styles each line on its own; stack-trace lines are recognised with `filter.IsContinuation`, which `filter.Filter` already uses. `cmd/klog` adds two flags and loads the theme in `build()`.

**Tech Stack:** Go 1.22 (module `klog`), stdlib only (`encoding/json`, `regexp`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-09-30-pretty-output-design.md`

## Global Constraints

- Stdlib only; no new module dependencies (`go.mod` must not change).
- `--format json` and `--format raw` output is byte-for-byte unchanged.
- Filtering is unchanged: `--level`, `--field`, `--grep`, `--exclude` still see the original line, and `Filter.Keep` behaves exactly as before (existing filter tests stay green).
- Theme values are SGR parameter strings matching `^[0-9;]*$`; the empty string means "no styling".
- Theme file: `--theme FILE`, else `os.UserConfigDir()/klog/theme.json`. A missing default file means defaults; a missing explicit `--theme FILE` is an error. Any theme error exits 2 with `klog: invalid theme <path>: <reason>`.
- Unknown theme keys are rejected (`DisallowUnknownFields`); `labels` must be non-empty.
- Default theme: `timestamp "2"`; levels `trace "2"`, `debug "2"`, `info ""`, `warn "33"`, `error "1;31"`, `fatal "1;97;41"`; trace `frame "2"`, `caused_by "31"`, `omitted "2"`; `keys "36"`; `labels ["36","32","33","35","34","31"]`.
- Colour is on only when stdout is a terminal, `NO_COLOR` is unset, and (fetch) `--out` is not used: the existing `useColor` rule, unchanged.
- Go style of this repo: table-driven tests, no test frameworks, `// ponytail:` comments for deliberate shortcuts with a known ceiling.
- Run shell commands through `rtk` (for example `rtk go test ./...`).

## Review Focus

Inputs the spec implies but does not spell out, most likely to bite first. Each has a test in the task named.

- **Numeric level** (pino `{"level":30,"msg":"m"}`): the level key must not vanish; it stays as `level=30`. Task 4.
- **No kubectl timestamp + a `ts`/`time` key**: the JSON time field is the only timestamp, so it must be kept. Task 4.
- **Newlines in a message or value**: must not split one record across several output lines. Task 4.
- **`{}` and empty-string values**: no blank line for `{}` (falls back to the raw text); `""` prints as `key=""`, not `key=`. Task 4.
- **Broken or missing theme, and leakage of the developer's real theme**: a bad file must exit 2 even with `--format json`; tests must not read the real `~/.config`. A zero-value `render.Options` must not panic. Tasks 3 and 5.

---

### Task 1: Export `filter.IsContinuation`

**Files:**
- Modify: `internal/filter/filter.go:37,44-45`
- Test: `internal/filter/filter_test.go`

**Interfaces:**
- Consumes: the existing `continuation` regexp in `filter.go`.
- Produces: `func IsContinuation(raw string) bool` in package `filter`. Task 3 calls it from `render`.

- [ ] **Step 1: Write the failing test**

Append to `internal/filter/filter_test.go`:

```go
func TestIsContinuation(t *testing.T) {
	cases := map[string]bool{
		"\tat Foo.bar(Foo.java:1)":         true,
		"  at Foo":                        true,
		"Caused by: java.io.IOException": true,
		"Suppressed: X":                  true,
		"... 12 more":                    true,
		"... 3 common frames omitted":    true,
		"":                               false,
		"plain":                          false,
		"java.lang.IOException: boom":    false,
		"Caused by":                      false,
		"caused by: x":                   false,
		"...12 more":                     false,
	}
	for raw, want := range cases {
		if got := IsContinuation(raw); got != want {
			t.Errorf("IsContinuation(%q) = %v, want %v", raw, got, want)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `rtk go test ./internal/filter -run TestIsContinuation`
Expected: FAIL to compile, `undefined: IsContinuation`.

- [ ] **Step 3: Implement**

In `internal/filter/filter.go`, change the line in `Keep` from
`if l.JSON == nil && continuation.MatchString(l.Raw) {` to
`if l.JSON == nil && IsContinuation(l.Raw) {`, and replace the `continuation` var block with:

```go
// continuation matches the start of a stack-trace continuation line.
var continuation = regexp.MustCompile(`^(\s|Caused by:|Suppressed:|\.\.\. \d+ (more|common frames omitted))`)

// IsContinuation reports whether raw, a non-JSON line, continues the stack
// trace or message before it. Filter and the pretty renderer share this.
func IsContinuation(raw string) bool { return continuation.MatchString(raw) }
```

- [ ] **Step 4: Run the package tests**

Run: `rtk go test ./internal/filter`
Expected: PASS (including the existing continuation tests).

- [ ] **Step 5: Commit**

```bash
git add internal/filter/filter.go internal/filter/filter_test.go
git commit -m "refactor: export filter.IsContinuation"
```

---

### Task 2: `internal/theme` package

**Files:**
- Create: `internal/theme/theme.go`
- Create: `internal/theme/theme_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces (package `theme`):
  - `type Levels struct{ Trace, Debug, Info, Warn, Error, Fatal string }`
  - `type Trace struct{ Frame, CausedBy, Omitted string }`
  - `type Theme struct{ Timestamp string; Levels Levels; Trace Trace; Keys string; Labels []string }`
  - `func Default() Theme` (a fresh `Labels` slice per call)
  - `func Load(path string) (Theme, error)`: merges the file over `Default()`; every error reads `invalid theme <path>: <reason>` and wraps the cause with `%w` (so `errors.Is(err, os.ErrNotExist)` works)
  - `func Paint(sgr, s string) string`: returns `s` unchanged when `sgr == ""`, else `"\x1b[" + sgr + "m" + s + "\x1b[0m"`

- [ ] **Step 1: Write the failing tests**

Create `internal/theme/theme_test.go`:

```go
package theme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func file(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefault(t *testing.T) {
	d := Default()
	if d.Timestamp != "2" || d.Levels.Error != "1;31" || d.Levels.Info != "" ||
		d.Levels.Fatal != "1;97;41" || d.Trace.CausedBy != "31" || d.Keys != "36" || len(d.Labels) != 6 {
		t.Fatalf("unexpected default %+v", d)
	}
	d.Labels[0] = "changed"
	if Default().Labels[0] != "36" {
		t.Fatal("Default shares its Labels slice between calls")
	}
}

func TestPaint(t *testing.T) {
	if got := Paint("", "x"); got != "x" {
		t.Errorf("empty sgr: got %q", got)
	}
	if got := Paint("1;31", "x"); got != "\x1b[1;31mx\x1b[0m" {
		t.Errorf("got %q", got)
	}
}

func TestLoadPartialKeepsDefaults(t *testing.T) {
	th, err := Load(file(t, `{"levels":{"warn":"34"},"keys":"1","labels":["90"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if th.Levels.Warn != "34" || th.Keys != "1" || len(th.Labels) != 1 || th.Labels[0] != "90" {
		t.Fatalf("overrides not applied: %+v", th)
	}
	if th.Levels.Error != "1;31" || th.Timestamp != "2" || th.Trace.Frame != "2" {
		t.Fatalf("defaults lost: %+v", th)
	}
}

func TestLoadExplicitEmptyValueClearsStyle(t *testing.T) {
	th, err := Load(file(t, `{"levels":{"error":""}}`))
	if err != nil || th.Levels.Error != "" {
		t.Fatalf("got %+v, %v", th, err)
	}
}

func TestLoadRejects(t *testing.T) {
	bad := map[string]string{
		"unknown top key":    `{"colour":"1"}`,
		"unknown level key":  `{"levels":{"loud":"1"}}`,
		"word instead sgr":   `{"keys":"red"}`,
		"letter in sgr":      `{"timestamp":"1m"}`,
		"nested bad sgr":     `{"trace":{"frame":"x"}}`,
		"empty labels":       `{"labels":[]}`,
		"bad label entry":    `{"labels":["36","x"]}`,
		"wrong type":         `{"keys":1}`,
		"trailing data":      `{} {}`,
		"empty file":         ``,
		"not json":           `nope`,
	}
	for name, content := range bad {
		t.Run(name, func(t *testing.T) {
			p := file(t, content)
			_, err := Load(p)
			if err == nil || !strings.HasPrefix(err.Error(), "invalid theme "+p+": ") {
				t.Fatalf("Load(%s) error = %v", content, err)
			}
		})
	}
}

func TestLoadMissingFileWrapsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `rtk go test ./internal/theme`
Expected: FAIL to compile (`undefined: Default`, `Load`, `Paint`).

- [ ] **Step 3: Implement**

Create `internal/theme/theme.go`:

```go
// Package theme holds the colours of the pretty renderer and loads them from
// a JSON file. Every value is an SGR parameter string such as "1;31".
package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

type Levels struct {
	Trace string `json:"trace"`
	Debug string `json:"debug"`
	Info  string `json:"info"`
	Warn  string `json:"warn"`
	Error string `json:"error"`
	Fatal string `json:"fatal"`
}

type Trace struct {
	Frame    string `json:"frame"`
	CausedBy string `json:"caused_by"`
	Omitted  string `json:"omitted"`
}

type Theme struct {
	Timestamp string   `json:"timestamp"`
	Levels    Levels   `json:"levels"`
	Trace     Trace    `json:"trace"`
	Keys      string   `json:"keys"`
	Labels    []string `json:"labels"`
}

// Default is the built-in theme. Labels is a fresh slice on every call.
func Default() Theme {
	return Theme{
		Timestamp: "2",
		Levels:    Levels{Trace: "2", Debug: "2", Info: "", Warn: "33", Error: "1;31", Fatal: "1;97;41"},
		Trace:     Trace{Frame: "2", CausedBy: "31", Omitted: "2"},
		Keys:      "36",
		Labels:    []string{"36", "32", "33", "35", "34", "31"},
	}
}

// Paint wraps s in the SGR sequence sgr. An empty sgr leaves s untouched.
func Paint(sgr, s string) string {
	if sgr == "" {
		return s
	}
	return "\x1b[" + sgr + "m" + s + "\x1b[0m"
}

// Load reads a theme file over Default(): fields the file omits keep their
// default. Every error names the file and wraps its cause.
func Load(path string) (Theme, error) {
	t, err := load(path)
	if err != nil {
		return Theme{}, fmt.Errorf("invalid theme %s: %w", path, err)
	}
	return t, nil
}

func load(path string) (Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, err
	}
	t := Default()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return Theme{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Theme{}, errors.New("unexpected data after the theme object")
	}
	if err := t.validate(); err != nil {
		return Theme{}, err
	}
	return t, nil
}

var sgrRe = regexp.MustCompile(`^[0-9;]*$`)

func (t Theme) validate() error {
	fields := []struct{ name, val string }{
		{"timestamp", t.Timestamp},
		{"levels.trace", t.Levels.Trace}, {"levels.debug", t.Levels.Debug},
		{"levels.info", t.Levels.Info}, {"levels.warn", t.Levels.Warn},
		{"levels.error", t.Levels.Error}, {"levels.fatal", t.Levels.Fatal},
		{"trace.frame", t.Trace.Frame}, {"trace.caused_by", t.Trace.CausedBy},
		{"trace.omitted", t.Trace.Omitted},
		{"keys", t.Keys},
	}
	for i, l := range t.Labels {
		fields = append(fields, struct{ name, val string }{fmt.Sprintf("labels[%d]", i), l})
	}
	for _, f := range fields {
		if !sgrRe.MatchString(f.val) {
			return fmt.Errorf("%s: %q is not an SGR code (digits and ';' only)", f.name, f.val)
		}
	}
	if len(t.Labels) == 0 {
		return errors.New("labels must not be empty")
	}
	return nil
}
```

- [ ] **Step 4: Run the tests**

Run: `rtk go test ./internal/theme && rtk gofmt -l internal/theme`
Expected: PASS, and `gofmt -l` prints nothing. If gofmt lists `theme_test.go`, run `gofmt -w internal/theme/theme_test.go` (the aligned map literal is the usual cause).

- [ ] **Step 5: Commit**

```bash
git add internal/theme
git commit -m "feat: add theme package for pretty output colours"
```

---

### Task 3: `render.Options`, padding, timestamp, level and stack-trace styling

JSON lines are still printed raw in this task (only level-coloured); Task 4 adds flattening. The `NoFlatten` option exists from here but is not read until Task 4, so every test added here sets `NoFlatten: true` and stays valid afterwards.

**Files:**
- Modify: `internal/render/render.go` (whole file)
- Modify: `internal/render/render_test.go` (helper and new tests)
- Modify: `cmd/klog/flags.go` (the `common` struct and `build()`, minimal), `cmd/klog/tail.go:68`, `cmd/klog/fetch.go:82`

**Interfaces:**
- Consumes: `filter.IsContinuation`, `filter.LineLevel`, `filter.Level` constants (Task 1 and existing); `theme.Theme`, `theme.Default`, `theme.Paint` (Task 2).
- Produces:
  - `type Options struct{ Format Format; Color bool; TZ *time.Location; NoFlatten bool; Theme theme.Theme }`
  - `func New(w io.Writer, o Options) *Renderer` (replaces `New(w, f, color, tz)`; nil `TZ` means UTC; a zero `Theme`, detected by empty `Labels`, means `theme.Default()`)
  - `cmd/klog`: `type common` loses `format` and `tz` and gains `view render.Options` (Format, TZ, NoFlatten, Theme; callers set `Color`).

- [ ] **Step 1: Rewrite the test helper and add failing tests**

In `internal/render/render_test.go`, replace the `write` helper with these three helpers (keep the `ts` var and every existing test unchanged):

```go
func writeOpts(t *testing.T, o Options, ls ...parse.Line) string {
	t.Helper()
	var buf bytes.Buffer
	r := New(&buf, o)
	for _, l := range ls {
		if err := r.Write(l); err != nil {
			t.Fatal(err)
		}
	}
	return buf.String()
}

func write(t *testing.T, f Format, color bool, l parse.Line) string {
	t.Helper()
	return writeOpts(t, Options{Format: f, Color: color, TZ: time.UTC}, l)
}

func esc(sgr, s string) string { return "\x1b[" + sgr + "m" + s + "\x1b[0m" }
```

Append these tests:

```go
func TestPrettyTimestampIsStyled(t *testing.T) {
	got := write(t, Pretty, true, parse.Line{Label: "p", Time: ts, Raw: "hello"})
	if !strings.Contains(got, esc("2", "12:00:01.500")+" hello\n") {
		t.Fatalf("got %q", got)
	}
}

func TestPrettyLevelStyles(t *testing.T) {
	opts := Options{Format: Pretty, Color: true, NoFlatten: true}
	for lvl, sgr := range map[string]string{
		"TRACE": "2", "DEBUG": "2", "WARN": "33", "ERROR": "1;31", "FATAL": "1;97;41",
	} {
		raw := `{"level":"` + lvl + `","msg":"hi"}`
		if got := writeOpts(t, opts, parse.Parse("p", raw)); !strings.Contains(got, esc(sgr, raw)+"\n") {
			t.Errorf("%s: got %q", lvl, got)
		}
	}
	raw := `{"level":"INFO","msg":"hi"}`
	got := writeOpts(t, opts, parse.Parse("p", raw))
	if !strings.Contains(got, " "+raw+"\n") || strings.Contains(got, "\x1b[m") {
		t.Errorf("INFO should be unstyled, got %q", got)
	}
	raw = `{"msg":"no level"}`
	if got := writeOpts(t, opts, parse.Parse("p", raw)); !strings.Contains(got, " "+raw+"\n") {
		t.Errorf("no level should be unstyled, got %q", got)
	}
}

func TestPrettyStackTraceStyles(t *testing.T) {
	opts := Options{Format: Pretty, Color: true, NoFlatten: true}
	for raw, want := range map[string]string{
		"\tat Foo.bar(Foo.java:1)":        esc("2", "\tat Foo.bar(Foo.java:1)"),
		"Caused by: java.io.IOException": esc("31", "Caused by: java.io.IOException"),
		"\tSuppressed: X":                esc("31", "\tSuppressed: X"),
		"\t... 12 more":                  esc("2", "\t... 12 more"),
		"... 3 common frames omitted":    esc("2", "... 3 common frames omitted"),
		"java.lang.IOException: boom":    "java.lang.IOException: boom", // the header line is not a continuation
	} {
		got := writeOpts(t, opts, parse.Line{Label: "p", Raw: raw})
		if !strings.HasSuffix(got, " "+want+"\n") {
			t.Errorf("%q: got %q, want suffix %q", raw, got, want)
		}
	}
}

func TestPrettyStackTraceColourOffKeepsText(t *testing.T) {
	got := write(t, Pretty, false, parse.Line{Label: "p", Raw: "\tat Foo"})
	if got != "[p] \tat Foo\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrettyInterleavedTracesRenderLikeTheyDoAlone(t *testing.T) {
	opts := Options{Format: Pretty, Color: true, NoFlatten: true}
	a := []parse.Line{
		{Label: "a-1", Raw: "java.lang.IOException: boom"}, {Label: "a-1", Raw: "\tat A.one(A.java:1)"},
		{Label: "a-1", Raw: "Caused by: X"},
	}
	b := []parse.Line{
		{Label: "b-1", Raw: "plain b"}, {Label: "b-1", Raw: "\t... 2 more"}, {Label: "b-1", Raw: "\tat B.two(B.java:2)"},
	}
	mixed := writeOpts(t, opts, a[0], b[0], a[1], b[1], a[2], b[2])
	only := func(out, label string) []string {
		var keep []string
		for _, ln := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if strings.Contains(ln, label) {
				keep = append(keep, ln)
			}
		}
		return keep
	}
	for label, want := range map[string]string{"a-1": writeOpts(t, opts, a...), "b-1": writeOpts(t, opts, b...)} {
		if got, w := strings.Join(only(mixed, label), "\n"), strings.TrimSuffix(want, "\n"); got != w {
			t.Errorf("%s: interleaved\n%s\nalone\n%s", label, got, w)
		}
	}
}

func TestPrettyPadsLabelsAndNeverShrinks(t *testing.T) {
	got := writeOpts(t, Options{Format: Pretty}, // colour off
		parse.Line{Label: "ab", Raw: "x"}, parse.Line{Label: "abcd", Raw: "x"}, parse.Line{Label: "ab", Raw: "x"})
	if want := "[ab] x\n[abcd] x\n[ab]   x\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	coloured := writeOpts(t, Options{Format: Pretty, Color: true},
		parse.Line{Label: "abcd", Raw: "x"}, parse.Line{Label: "ab", Raw: "x"})
	if !strings.Contains(coloured, "\x1b[0m   x\n") { // padding sits outside the escape codes
		t.Fatalf("got %q", coloured)
	}
}

func TestNewZeroOptionsUsesDefaultTheme(t *testing.T) {
	got := writeOpts(t, Options{Format: Pretty, Color: true}, parse.Line{Label: "p", Raw: "x"})
	if !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, " x\n") {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `rtk go test ./internal/render`
Expected: FAIL to compile (`undefined: Options`, `too many arguments`...).

- [ ] **Step 3: Rewrite `internal/render/render.go`**

```go
// Package render writes parsed lines as pretty text, JSON or raw.
package render

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"regexp"
	"strings"
	"time"
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
	if lv, ok := filter.LineLevel(l); ok {
		return r.paint(r.levelStyle(lv), l.Raw)
	}
	return l.Raw
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
```

- [ ] **Step 4: Fix the callers so the module compiles**

In `cmd/klog/flags.go`: in `type common`, replace

```go
	format  render.Format
	tz      *time.Location
```
with
```go
	view    render.Options // Format, TZ, NoFlatten, Theme; callers set Color
```
and in `build()` replace `out.format, err = render.ParseFormat(c.format)` with `out.view.Format, err = render.ParseFormat(c.format)`, and in the `--tz` block replace `out.tz = time.UTC` with `out.view.TZ = time.UTC` and `out.tz, err = time.LoadLocation(c.tz)` with `out.view.TZ, err = time.LoadLocation(c.tz)`.

In `cmd/klog/tail.go`, replace line 68 with:

```go
	view := c.view
	view.Color = useColor(stdout)
	renderer := render.New(stdout, view)
```

In `cmd/klog/fetch.go`, replace line 82 with:

```go
	view := c.view
	view.Color = *outPath == "" && useColor(stdout)
	renderer := render.New(w, view)
```

- [ ] **Step 5: Run the whole suite**

Run: `rtk go build ./... && rtk go vet ./... && rtk go test ./...`
Expected: all PASS. The existing `TestFetchPrettyShowsLabelAndTime` still passes (JSON lines are raw until Task 4).

- [ ] **Step 6: Commit**

```bash
git add internal/render cmd/klog/flags.go cmd/klog/tail.go cmd/klog/fetch.go
git commit -m "feat: style levels, timestamps and stack traces; align labels"
```

---

### Task 4: Flatten JSON lines and honour `NoFlatten`

**Files:**
- Modify: `internal/render/render.go` (imports, `body`, new `flatten` and `fmtValue`)
- Modify: `internal/render/render_test.go` (new tests)
- Modify: `cmd/klog/fetch_test.go` (`TestFetchPrettyShowsLabelAndTime`)

**Interfaces:**
- Consumes: `Options.NoFlatten`, `Renderer.paint`, `levelStyle` (Task 3); `filter.LineLevel`.
- Produces: pretty output for JSON lines as `LEVEL msg  key=val ...` (rules below).

Rules implemented (from the spec, with the refinements the Review Focus list asks for):
- Level token: `filter.LineLevel`, upper-case (`WARNING` prints `WARN`), styled from `theme.levels`. A level that is numeric or unknown gives no token and its key is kept as a pair.
- Message: the first of `msg`, `message` whose value is a string. Only that key is consumed. `\n` and `\r` in it print as `\n` and `\r` (two characters).
- Pairs: every remaining key, sorted. `key=` is styled with `theme.keys`, the value is not. Strings print bare unless empty or containing whitespace or `"` (then `strconv.Quote`). Numbers print as decoded, `null` and booleans as JSON, objects and arrays as compact JSON without HTML escaping.
- `level`/`severity`/`lvl` are dropped only when a level was recognised; `time`/`ts`/`timestamp` only when the line has a kubectl timestamp.
- Spacing: `level`, `msg` joined by one space; two spaces before the pairs; pairs joined by one space. An empty result (for example `{}`) falls back to the raw text.

- [ ] **Step 1: Write the failing tests**

Append to `internal/render/render_test.go`:

```go
func TestPrettyFlatten(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"level msg and sorted keys", `{"level":"INFO","msg":"hello","b":2,"a":"x"}`, `INFO hello  a=x b=2`},
		{"level alias is normalised", `{"severity":"warning","msg":"m"}`, `WARN m`},
		{"no msg", `{"level":"WARN","a":1}`, `WARN  a=1`},
		{"no level", `{"msg":"hi","a":1}`, `hi  a=1`},
		{"msg only", `{"msg":"hi"}`, `hi`},
		{"message alias", `{"message":"hi"}`, `hi`},
		{"msg wins and message is kept", `{"msg":"a","message":"b"}`, `a  message=b`},
		{"non-string msg is kept", `{"msg":5}`, `msg=5`},
		{"numeric level is kept", `{"level":30,"msg":"m"}`, `m  level=30`},
		{"empty object falls back to raw", `{}`, `{}`},
		{"empty string value is quoted", `{"msg":"m","e":""}`, `m  e=""`},
		{"spaces and quotes are quoted", `{"msg":"m","k":"a b","q":"say \"hi\""}`, `m  k="a b" q="say \"hi\""`},
		{"newline in value is escaped", `{"msg":"m","n":"l1\nl2"}`, `m  n="l1\nl2"`},
		{"newline in msg is escaped", `{"msg":"a\nb"}`, `a\nb`},
		{"big integer stays exact", `{"msg":"m","id":12345678901234567890}`, `m  id=12345678901234567890`},
		{"nested values are compact json", `{"msg":"m","o":{"b":1,"a":[1,"x<y"]},"t":true,"z":null}`, `m  o={"a":[1,"x<y"],"b":1} t=true z=null`},
		{"time keys are kept without a kubectl timestamp", `{"msg":"m","ts":"2026"}`, `m  ts=2026`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := writeOpts(t, Options{Format: Pretty}, parse.Parse("p", tc.in))
			if want := "[p] " + tc.want + "\n"; got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestPrettyFlattenDropsTimeKeysWhenKubectlTimeIsShown(t *testing.T) {
	l := parse.Parse("p", "2026-09-30T12:00:01.5Z "+`{"msg":"m","ts":"a","time":"b","timestamp":"c","a":1}`)
	got := writeOpts(t, Options{Format: Pretty, TZ: time.UTC}, l)
	if want := "[p] 12:00:01.500 m  a=1\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPrettyFlattenColours(t *testing.T) {
	opts := Options{Format: Pretty, Color: true}
	for lvl, sgr := range map[string]string{
		"TRACE": "2", "DEBUG": "2", "WARN": "33", "ERROR": "1;31", "FATAL": "1;97;41",
	} {
		got := writeOpts(t, opts, parse.Parse("p", `{"level":"`+lvl+`","msg":"hi"}`))
		if !strings.HasSuffix(got, " "+esc(sgr, lvl)+" hi\n") {
			t.Errorf("%s: got %q", lvl, got)
		}
	}
	got := writeOpts(t, opts, parse.Parse("p", `{"level":"INFO","msg":"hi"}`))
	if !strings.HasSuffix(got, " INFO hi\n") || strings.Contains(got, "\x1b[m") {
		t.Errorf("INFO should be unstyled, got %q", got)
	}
	got = writeOpts(t, opts, parse.Parse("p", `{"level":"ERROR","msg":"boom","k":"v"}`))
	if !strings.HasSuffix(got, " "+esc("1;31", "ERROR")+" boom  "+esc("36", "k=")+"v\n") {
		t.Errorf("keys: got %q", got)
	}
}

func TestPrettyNoFlattenKeepsRawJSON(t *testing.T) {
	raw := `{"level":"ERROR","msg":"x","a":1}`
	if got := writeOpts(t, Options{Format: Pretty, NoFlatten: true}, parse.Parse("p", raw)); got != "[p] "+raw+"\n" {
		t.Fatalf("got %q", got)
	}
	got := writeOpts(t, Options{Format: Pretty, NoFlatten: true, Color: true}, parse.Parse("p", raw))
	if !strings.HasSuffix(got, " "+esc("1;31", raw)+"\n") {
		t.Fatalf("level style should still apply, got %q", got)
	}
}

func TestPrettyFlattenOnlyAffectsPretty(t *testing.T) {
	raw := `{"level":"INFO","msg":"x"}`
	if got := write(t, Raw, false, parse.Parse("p", raw)); got != raw+"\n" {
		t.Fatalf("raw changed: %q", got)
	}
	if got := write(t, JSON, false, parse.Parse("p", raw)); !strings.Contains(got, `"raw":"{\"level\":\"INFO\",\"msg\":\"x\"}"`) {
		t.Fatalf("json changed: %q", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `rtk go test ./internal/render`
Expected: FAIL (`TestPrettyFlatten*` and `TestPrettyNoFlatten*` print raw JSON instead of flattened text).

- [ ] **Step 3: Implement**

In `internal/render/render.go`: add `"bytes"`, `"sort"` and `"strconv"` to the imports, replace `body` with the version below, and append `flatten`, `levelNames`, `fmtValue`.

```go
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
		if s, ok := l.JSON[k].(string); ok {
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
		head = append(head, strings.NewReplacer("\n", `\n`, "\r", `\r`).Replace(msg))
	}
	out := strings.Join(head, " ")
	if len(keys) > 0 {
		pairs := make([]string, len(keys))
		for i, k := range keys {
			pairs[i] = r.paint(r.o.Theme.Keys, k+"=") + fmtValue(l.JSON[k])
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

// fmtValue prints one JSON value for a key=value pair.
func fmtValue(v any) string {
	switch x := v.(type) {
	case string:
		if x == "" || strings.ContainsAny(x, " \t\r\n\"") {
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
```

- [ ] **Step 4: Update the one existing cmd test that expected raw JSON in pretty mode**

In `cmd/klog/fetch_test.go`, in `TestFetchPrettyShowsLabelAndTime`, change
`want := "[a-1] 12:00:03.000 " + aThree` to
`want := "[a-1] 12:00:03.000 ERROR a-three  orderId=12345678901234567890 requestId=r1"`.

- [ ] **Step 5: Run the whole suite**

Run: `rtk go vet ./... && rtk go test ./...`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/render cmd/klog/fetch_test.go
git commit -m "feat: flatten JSON log lines to LEVEL msg key=val in pretty output"
```

---

### Task 5: `--no-flatten`, `--theme`, theme loading, docs

**Files:**
- Modify: `cmd/klog/flags.go` (`commonFlags`, `addCommon`, `build`, new `loadTheme`, imports)
- Modify: `cmd/klog/fetch_test.go` (`setup`)
- Create: `cmd/klog/output_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `theme.Load`, `theme.Default`, `theme.Theme` (Task 2); `common.view` (Task 3).
- Produces: flags `--no-flatten` and `--theme FILE` on both `tail` and `fetch`; `func loadTheme(path string) (theme.Theme, error)` in package main.

- [ ] **Step 1: Isolate tests from the developer's real config**

In `cmd/klog/fetch_test.go`, in `setup`, add after `t.Helper()`:

```go
	// os.UserConfigDir reads these; keep the developer's real theme out of tests.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
```

- [ ] **Step 2: Write the failing tests**

Create `cmd/klog/output_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTheme(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFetchPrettyFlattensJSON(t *testing.T) {
	setup(t)
	code, out, _ := klog(t, with()...)
	want := []string{
		"[a-1] 12:00:01.000 INFO a-one",
		"[b-1] 12:00:02.000 WARN b-two  requestId=r2",
		"[a-1] 12:00:03.000 ERROR a-three  orderId=12345678901234567890 requestId=r1",
		"[b-1] 12:00:04.000 plain b-four",
	}
	if code != 0 || !equal(lines(out), want) {
		t.Fatalf("code %d, got\n%s\nwant\n%s", code, out, strings.Join(want, "\n"))
	}
}

func TestFetchNoFlattenKeepsRawJSON(t *testing.T) {
	setup(t)
	code, out, _ := klog(t, with("--no-flatten")...)
	want := []string{
		"[a-1] 12:00:01.000 " + aOne,
		"[b-1] 12:00:02.000 " + bTwo,
		"[a-1] 12:00:03.000 " + aThree,
		"[b-1] 12:00:04.000 " + bFour,
	}
	if code != 0 || !equal(lines(out), want) {
		t.Fatalf("code %d, got\n%s\nwant\n%s", code, out, strings.Join(want, "\n"))
	}
}

func TestThemeErrorsExit2AndNeverCallKubectl(t *testing.T) {
	f := setup(t)
	cfg, err := os.UserConfigDir() // HOME was redirected by setup
	if err != nil {
		t.Skip("no user config dir")
	}
	if err := os.MkdirAll(filepath.Join(cfg, "klog"), 0o755); err != nil {
		t.Fatal(err)
	}
	defaultPath := filepath.Join(cfg, "klog", "theme.json")
	if err := os.WriteFile(defaultPath, []byte(`{"colour":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := writeTheme(t, `{"keys":"red"}`)
	cases := map[string][]string{
		"malformed default file":      with(),
		"explicit file is malformed":  with("--theme", bad),
		"explicit file is missing":    with("--theme", filepath.Join(t.TempDir(), "nope.json")),
		"still validated for json":    with("--format", "json", "--theme", bad),
		"still validated for raw":     with("--format", "raw", "--theme", bad),
		"tail validates it as well":   {"tail", "-n", "shop", "-l", "app=web", "--theme", bad},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, errs := klog(t, args...)
			if code != 2 || !strings.Contains(errs, "klog: invalid theme ") {
				t.Fatalf("code %d, stderr %q", code, errs)
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}

func TestThemeFlagAcceptsAValidFile(t *testing.T) {
	setup(t)
	p := writeTheme(t, `{"levels":{"warn":"34"},"labels":["90"]}`)
	code, out, errs := klog(t, with("--theme", p)...)
	if code != 0 || !strings.Contains(out, "WARN b-two  requestId=r2") {
		t.Fatalf("code %d, stderr %q, stdout\n%s", code, errs, out)
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `rtk go test ./cmd/klog -run 'Theme|NoFlatten'`
Expected: FAIL (`flag provided but not defined: -no-flatten`, `-theme`; exit code 2 with a different message).

- [ ] **Step 4: Implement**

In `cmd/klog/flags.go`:

1. Imports: add `"path/filepath"` and `"klog/internal/theme"`.
2. `commonFlags`: add two fields to the struct:

```go
	theme      string
	noFlatten  bool
```
3. `addCommon`: add before `return c`:

```go
	fs.BoolVar(&c.noFlatten, "no-flatten", false, "pretty format: print JSON lines as raw JSON instead of LEVEL msg key=val")
	fs.StringVar(&c.theme, "theme", "", "theme file (default: <user config dir>/klog/theme.json)")
```
4. `build()`: replace the final `return out, nil` (after the `--tz` block) with:

```go
	out.view.NoFlatten = c.noFlatten
	if out.view.Theme, err = loadTheme(c.theme); err != nil {
		return out, err
	}
	return out, nil
```
5. Add below `compileOpt`:

```go
// loadTheme reads --theme FILE, or the default file in the user config dir.
// A missing default file means the built-in theme; a missing explicit file is an error.
func loadTheme(path string) (theme.Theme, error) {
	if path != "" {
		return theme.Load(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return theme.Default(), nil // no $HOME: nothing to read, use the defaults
	}
	th, err := theme.Load(filepath.Join(dir, "klog", "theme.json"))
	if errors.Is(err, os.ErrNotExist) {
		return theme.Default(), nil
	}
	return th, err
}
```

- [ ] **Step 5: Document it**

In `README.md`, replace the line starting ``- `--format pretty` (default)`` through its end (the line `` `--format pretty` (default), `json` (...) or `raw`. ``) with:

```markdown
`--format pretty` (default), `json` (one object per line: `source`, `time`, `raw`, `json`) or `raw`.

Pretty output colours levels and stack traces on a terminal (`NO_COLOR` turns it off) and prints JSON lines as `LEVEL msg  key=val ...`; keep the raw JSON with `--no-flatten`.

Colours come from `--theme FILE`, else `<user config dir>/klog/theme.json` (`~/.config/klog/theme.json` on Linux, `~/Library/Application Support/klog/theme.json` on macOS). Every value is an SGR code such as `1;31`; omitted fields keep their default:

```json
{
  "timestamp": "2",
  "levels": {"trace": "2", "debug": "2", "info": "", "warn": "33", "error": "1;31", "fatal": "1;97;41"},
  "trace":  {"frame": "2", "caused_by": "31", "omitted": "2"},
  "keys":   "36",
  "labels": ["36", "32", "33", "35", "34", "31"]
}
```
```

- [ ] **Step 6: Run everything**

Run: `rtk gofmt -l . ; rtk go vet ./... && rtk go test ./...`
Expected: `gofmt -l` prints nothing (run `gofmt -w` on any file it lists, usually the aligned map literals in the new tests); vet and all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/klog README.md
git commit -m "feat: add --no-flatten and --theme for pretty output"
```

---

## Self-review (spec coverage)

- Level colours, dim timestamp, label padding: Task 3. JSON flattening and drop rules: Task 4. Stack-trace styling via the shared `filter.IsContinuation`: Tasks 1 and 3. `--no-flatten`: Tasks 3 (option) and 4 (behaviour), 5 (flag). Theme file, validation, location and error policy: Tasks 2 and 5. `render.Options` replacing positional args and the callers: Task 3. README: Task 5.
- Spec deviations made deliberately (the spec was updated to match): numeric levels and JSON time fields are kept when they are the only copy; `msg` newlines are escaped; the spec's `inTrace` state was already removed.
- Types used across tasks: `theme.Theme{Timestamp, Levels, Trace, Keys, Labels}`, `render.Options{Format, Color, TZ, NoFlatten, Theme}`, `common.view`, `loadTheme`, `filter.IsContinuation`, `Renderer.paint/levelStyle/traceStyle/flatten`, `fmtValue`: names match in every task.
