# klog pretty output: design

Status: approved in conversation, 2026-09-30. Next step: implementation plan.

## Goal

Make `--format pretty` (the default) easier to read in a terminal:

- colour log levels,
- flatten JSON log lines to `LEVEL msg key=val ...`,
- style multi-line stack traces,
- align pod labels,
- let users change the colours with a theme file,
- let users keep raw JSON with `--no-flatten`.

`--format json` and `--format raw` do not change. Filtering does not change
(`--field`, `--grep`, `--exclude` and `--level` still see the original line).

## Target output

Colour on, Pretty format:

```
[web-1/app] 12:00:01.500 ERROR db timeout  host=a retries=3
[web-1/app]              java.lang.IOException: boom        plain line, unchanged
[web-1/app]                at Foo.bar(Foo.java:1)           dim
[web-1/app]              Caused by: X                       red
[web-1/app]                ... 12 more                      dim
```

Colour off (pipe, `NO_COLOR`): same layout, no escape codes. Continuation
lines keep their original text. The timestamp column is shown whenever the
line has one, as today; the sketch above leaves it out on trace lines for
brevity.

## Components

### `internal/theme` (new)

- `Theme` struct holding SGR parameter strings (see Theme file).
- `Default() Theme`.
- `Load(path string) (Theme, error)`: reads JSON, merges over `Default()`.
- Stdlib only (`encoding/json`, `regexp`).

### `internal/filter`

- Export `IsContinuation(raw string) bool`, wrapping the existing
  `continuation` regex.
- `Filter.Keep` calls it. One definition of "continuation line" for both
  filtering and rendering.

### `internal/render`

- `New(w, f, color, tz)` becomes `New(w io.Writer, o Options)` with
  `Options{Format, Color bool, TZ *time.Location, NoFlatten bool, Theme theme.Theme}`.
  Callers to update: `cmd/klog/tail.go`, `cmd/klog/fetch.go`, `render_test.go`.
- `Renderer` gains a grow-only label width. Stack-trace styling needs no
  per-label state: each line is classified on its own by `IsContinuation`, so
  lines from different pods can interleave safely. `Renderer` is used from one
  goroutine only (tail's main loop, fetch's writer), so no locking.
- Imports `filter` for `LineLevel` and `IsContinuation`. `filter` imports only
  `parse`, so there is no cycle.

### `cmd/klog`

- `--no-flatten` (bool) and `--theme FILE` added in `addCommon`, so `tail` and
  `fetch` both get them.
- `build()` loads the theme and returns it in `common`.

## Rendering rules (Pretty)

1. **Prefix.** `[label]` padded to the widest label seen so far, coloured from
   `theme.labels` by the existing FNV hash.
2. **Timestamp.** `15:04:05.000` in `--tz`, styled with `theme.timestamp`.
3. **JSON line, flattened** (default): `LEVEL msg  key=val ...`.
   - Level from `filter.LineLevel`, printed upper-case, styled from
     `theme.levels`. A line with no recognised level prints no level token.
   - Message: the first of `msg`, `message` whose value is a NON-empty string;
     only that key is consumed, so `{"msg":"","a":1}` prints `a=1 msg=""`.
     Absent: omitted. Control characters are escaped, see below.
   - Remaining keys sorted, as `key=val`; `key=` is styled with `theme.keys`,
     the value is not. Empty strings print as `key=""`.
   - Dropped keys: `level`, `severity`, `lvl` only when a level was recognised
     (a numeric level such as pino's `30` stays as `level=30`); `time`, `ts`,
     `timestamp` only when the line has a kubectl timestamp (otherwise the JSON
     field is the only timestamp).
   - If nothing is left (for example `{}`), the raw text is printed.
   - Control characters never reach the terminal. In the message and in key
     names, every non-printable rune (`!unicode.IsPrint`, except the space) prints as its Go
     escape without quotes: `\n`, `\r`, `\t`, `\x1b`, `\u202e`. A string value
     is quoted with `strconv.Quote` when it is empty, contains a space or `"`, or
     contains any non-printable rune. Non-JSON lines stay raw (unchanged
     behaviour). Nested JSON values are escaped by `encoding/json` only.
   - Strings otherwise print bare. Numbers print as decoded (`UseNumber`). Nested objects and
     arrays print as compact JSON.
4. **JSON line, `--no-flatten`:** the raw text is printed as today. The level
   style from `theme.levels` still applies to the whole body.
5. **Non-JSON line, not a continuation:** printed unchanged.
6. **Continuation line** (`l.JSON == nil && filter.IsContinuation(l.Raw)`),
   styled by kind, judged on the indent-trimmed text (Java indents nested
   `Suppressed:` and `... N more` with a tab):
   - starts with `Caused by:` or `Suppressed:` -> `theme.trace.caused_by`
   - matches `... N more` / `... N common frames omitted` -> `theme.trace.omitted`
   - anything else (indented frame) -> `theme.trace.frame`

Classification is stateless, so the header line of a trace (for example
`java.lang.IOException: boom`) stays unstyled and only its continuation lines
are styled.

## Theme file

- Path: `--theme FILE`, else `os.UserConfigDir()/klog/theme.json`.
- Missing default file, or a default path blocked by a regular file
  (`ENOTDIR`, for example `<config>/klog` is a file): built-in defaults.
  Missing explicit `--theme FILE`: error.
- Every value is an SGR parameter string. Empty string means no styling.

```json
{
  "timestamp": "2",
  "levels": {"trace":"2","debug":"2","info":"","warn":"33","error":"1;31","fatal":"1;97;41"},
  "trace":  {"frame":"2","caused_by":"31","omitted":"2"},
  "keys":   "36",
  "labels": ["36","32","33","35","34","31"]
}
```

- Validation: unknown keys rejected (`DisallowUnknownFields`); each value must
  match `^[0-9;]*$`; `labels` must be non-empty when present.
- Partial files are fine: missing fields keep their default.
- Any failure: exit 2, `klog: invalid theme <path>: <reason>`.
- The theme is loaded on every run, including pipes and `--format json`, so a
  broken file is reported consistently rather than only on a terminal.

## Errors and edge cases

- Theme load error: usage error, exit 2 (same path as the other flag errors in
  `build()`).
- `--no-flatten` with `--format json` or `raw`: accepted, no effect.
- A level key that is numeric or unknown: no level token, no level style (the
  existing `LineLevel` contract).
- Label width only grows, so early lines can be misaligned until the longest
  label appears. `// ponytail:` comment in code; fix only if it bothers users
  (for example by pre-scanning resolved streams in `tail`).

## Testing

Table-driven, in the existing `render_test.go` style, plus a new
`theme_test.go`. Helper `write` takes `Options`.

- Level colours for each level, and no level.
- Flattening: key order, dropped keys, quoting, nested values, missing `msg`.
- `--no-flatten` keeps raw text and still applies the level style.
- Colour off: no escape codes anywhere.
- Stack trace: frame, `Caused by:`, `... N more` get their theme styles; the
  header line stays plain; two labels interleaved render identically to each
  label alone.
- Label padding grows and never shrinks.
- `filter.IsContinuation` has a table test; `Filter.Keep` behaviour is unchanged
  (existing tests stay green).
- Theme: defaults, partial file, unknown key, bad SGR value, empty `labels`,
  missing default file, missing explicit file.
- `flags_test`/`tail_test`: `--no-flatten` and `--theme` are wired through.

## Out of scope

- Theme presets, a `KLOG_THEME` env var, hot reload, per-pod colour overrides.
- Changes to `--format json` / `raw`, or to filter semantics.
- Styling stack traces in `--format json` / `raw`.
