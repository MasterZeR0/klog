# klog Commands & Features Reference

Complete reference for all commands, flags, and features.

## Commands

### `klog tail` — Follow logs live

Stream logs from matched pods, showing recent history before following.

```bash
klog tail [flags]
```

**Defaults:**
- `--since 5m` — show 5 minutes of history before following
- `--poll 5s` — check for new/deleted pods every 5 seconds

**Tail-specific flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--since` | duration | 5m | Backlog to show before following (e.g. 10m, 1h, 1d) |
| `--poll` | duration | 5s | How often to look for new and deleted pods |
| `--wait` | bool | false | Keep polling when no pods match, instead of exiting immediately |
| `--out` | string | stdout | Append to this file instead of stdout. Each line is written as it is rendered, so `tail -f` on the file works. Nothing is printed to stdout |

**Example:**
```bash
klog tail -n shop -d checkout --level WARN --since 30m
klog tail -n shop -l app=web --poll 10s --wait
klog tail -n shop -d checkout --level WARN --out warnings.log
```

---

### `klog fetch` — Fetch logs for a time range

Retrieve logs from a past time range, sorted by timestamp.

```bash
klog fetch [flags]
```

**Requires one of:** `--since` or `--since-time`

**Fetch-specific flags:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--since` | duration | (required) | How far back to fetch Example: 2h, 30m, 2d |
| `--since-time` | string | — | Absolute start time in RFC3339 format. Mutually exclusive with `--since` |
| `--until` | string | — | End time: RFC3339 format or duration meaning "that long ago" |
| `--previous` | bool | false | Include logs from the previous container instance (after restarts) |
| `--out` | string | stdout | Write to this file atomically instead of stdout |
| `--stats` | bool | false | Print a pod x level count table instead of the lines (see [Incident debugging](#incident-debugging)) |

**Example:**
```bash
klog fetch -n shop -d checkout --since 2h --level ERROR
klog fetch -n shop -l app=web --since-time 2026-09-30T10:00:00Z --until 30m
klog fetch -n prod -p '^api-' --since 1h --out errors.log --format json
```

---

## Profiles

Both commands accept a leading `@name` that expands to a saved list of flags from `profiles.json` (see [CONFIG.md](CONFIG.md#5-profiles-saved-flag-sets)). Flags you add after it override the profile's scalar flags and add to repeatable ones such as `--field`.

```bash
klog tail  @checkout-prod
klog tail  @checkout-prod --level ERROR --field requestId=abc-123
klog fetch @checkout-prod --since 2h --out errors.log
```

An unknown profile or a malformed file is a usage error (exit 2) and lists the profiles that exist.

---

## Pod Selection (Required)

Exactly one of these must be specified:

| Flag | Type | Description |
|------|------|-------------|
| `-l` | label selector | Select pods by label selector (e.g. `-l app=web` or `-l tier in (cache,db)`) |
| `-d` | deployment name | Select all pods of a deployment (e.g. `-d checkout`) |
| `-p` | regex | Select pods matching a regex pattern (e.g. `-p '^web-'`) |

**Optional pod filters:**

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `-n` | namespace | context default | Kubernetes namespace |
| `--context` | string | — | kubeconfig context (uses current context if not set) |
| `-c` | regex | all containers | Container name regex (default: all containers in selected pods) |

**Example:**
```bash
# By deployment
klog tail -n shop -d checkout

# By label
klog tail -l app=web --context staging

# By pod regex
klog fetch -p '^api-worker-' -n production
```

---

## Filtering

All filters are optional and cumulative (all must match for a line to be kept).

### Log Level

| Flag | Type | Description |
|------|------|-------------|
| `--level` | string | Minimum level: TRACE, DEBUG, INFO, WARN, ERROR or FATAL. Lines without a recognized level are dropped. Reads `level`, `severity` or `lvl` field from JSON lines, as a name or a number. Non-JSON lines are dropped. |

**Recognized levels (in order):**
- TRACE
- DEBUG
- INFO
- WARN (shows WARN and above)
- ERROR (shows ERROR and FATAL)
- FATAL

**Numeric levels** (pino/bunyan): 10=TRACE, 20=DEBUG, 30=INFO, 40=WARN, 50=ERROR, 60=FATAL. Values between steps round down (35 is INFO); 60 and above are FATAL; below 10 has no level.

**Examples:**
```bash
klog tail --level WARN        # WARN and above (WARN, ERROR, FATAL)
klog tail --level ERROR       # ERROR and FATAL only
klog fetch --level DEBUG      # DEBUG, INFO, WARN, ERROR, FATAL
```

### JSON Field Filters

| Flag | Type | Description |
|------|------|-------------|
| `--field` | string | Filter on JSON keys, including nested ones by dotted path. Repeatable. Non-JSON lines are dropped. |

**Syntax:**
- `key=value` — exact match
- `key!=value` — not equal (also matches missing keys)
- `key~regex` — regex match on the value
- `key>n`, `key>=n`, `key<n`, `key<=n` — numeric comparison (as float64). False unless the value is a JSON number (a string such as `"500"` does not count). `n` must be a number, else the flag is rejected.

The first operator character in the expression decides, so `msg~a>b` is a regex and `x=a>b` is an exact match.

**Nested keys:** `req.user.id=42` walks nested objects. A literal top-level key that contains dots (`"req.user.id"`) wins over the nested path. A missing path behaves like a missing key. Arrays are not indexed.

**Examples:**
```bash
# Exact match
klog tail --field 'requestId=abc-123'

# Not equal (missing key counts as not equal)
klog tail --field 'status!=200'

# Regex match
klog tail --field 'host~^api\.prod'

# Numeric comparison
klog tail --field 'latency>500' --field 'status>=500'

# Nested key
klog tail --field 'req.user.id=42'

# Multiple filters (all must match)
klog tail --field 'status!=200' --field 'method=POST' --field 'latency~[0-9]{4,}'
```

### Text Search

| Flag | Type | Description |
|------|------|-------------|
| `--grep` | regex | Keep lines matching this regex |
| `--exclude` | regex | Drop lines matching this regex |

**Notes:**
- Indented stack-trace lines stay attached to the line before them
- Applied after level and field filters
- Regex syntax is Go's `regexp/syntax`

**Examples:**
```bash
klog tail --grep 'error|failure'
klog fetch --exclude 'health.*check'
klog tail --grep 'timeout' --exclude 'connect_timeout'  # All filters must match
```

---

## Incident debugging

Three flags for working out what happened around an error. `-A`, `-B`, `-C` and `--follow-id` work on both `tail` and `fetch`; `--stats` is `fetch` only (a tail never ends, so it has nothing to total, and `klog tail --stats` is a usage error).

### Context lines: `-A`, `-B`, `-C`

| Flag | Description |
|------|-------------|
| `-A N`, `--after N` | Also print the N lines after each match |
| `-B N`, `--before N` | Also print the N lines before each match |
| `-C N` | Both; the larger of `-C` and `-A`/`-B` wins |

A match is a line that passes every filter; `-A`/`-B`/`-C` need `--grep` and are a usage error without it. Context lines are printed even though they do not match, so `--level`, `--field` and `--exclude` do not apply to them.

- Context is per pod and container: lines of one pod are never used as context for a match in another.
- A line is printed once even when matches overlap. Stack-trace lines stay attached to their match and are not counted as context.
- There is no `--` separator between groups: in a merged multi-pod stream a separator has no meaningful position. Use `--format json` or the pod label to tell groups apart.
- With `tail`, after-context continues across the following lines as they arrive.

```bash
klog fetch -n shop -d checkout --since 1h --grep 'payment declined' -C 5
klog tail  -n shop -d checkout --grep OOMKilled -B 10
```

### Trace follow: `--follow-id FIELD`

Lines that pass all the other filters are seeds. Their value of the JSON field `FIELD` is remembered, and every line from any selected pod whose `FIELD` has a remembered value is printed too, even if it fails the other filters. Use it to see a whole request after finding its error.

- `FIELD` is a top-level JSON key (no dotted paths). Values that are strings, numbers or booleans count as IDs; lines without the field, and non-JSON lines, are never followed.
- `fetch` reads every pod first and then prints, in timestamp order, all lines that share a seed's ID. This includes lines logged before the seed. The matching lines are held in memory, so a very large fetch needs a narrower `--since`.
- `tail` is forward-only: the set grows as seeds arrive, so only lines logged after the seed is seen are followed. Earlier lines of the same request have already gone by and cannot be recovered; use `fetch` for that. Across pods, arrival order decides which line is seen first.
- A followed line counts as a match for `-A`/`-B`/`-C`.

```bash
klog fetch -n shop -d checkout --since 1h --level ERROR --follow-id traceId
klog tail  -n shop -l app=web --field 'status=500' --follow-id requestId
```

### Level counts: `fetch --stats`

Prints counts of the lines that remain after the filters (including `-A`/`-B`/`-C` and `--follow-id` lines) instead of the lines themselves: one row per pod (sorted), columns `TRACE DEBUG INFO WARN ERROR FATAL ?`, and `TOTAL` row and column. `?` counts lines with no recognised level, including non-JSON lines.

```
$ klog fetch -n shop -d checkout --since 1h --stats
SOURCE      TRACE  DEBUG  INFO  WARN  ERROR  FATAL  ?   TOTAL
checkout-1  0      0      812   14    3      0      9   838
checkout-2  0      0      790   2     0      0      4   796
TOTAL       0      0      1602  16    3      0      13  1634
```

With `--format json` it prints one object per pod, sorted by pod, with every level key present (no TOTAL row):

```json
{"source":"checkout-1","counts":{"?":9,"DEBUG":0,"ERROR":3,"FATAL":0,"INFO":812,"TRACE":0,"WARN":14},"total":838}
```

---

## Output Formats

| Flag | Value | Description |
|------|-------|-------------|
| `--format` | `pretty` | (default) Pretty-printed with colors, JSON flattened to `LEVEL msg key=val ...`. Terminal colors via `NO_COLOR` env var and theme file. |
| `--format` | `json` | One JSON object per line with keys: `source`, `time`, `raw`, `json` |
| `--format` | `raw` | Raw log lines with no parsing or filtering applied |
| `--format` | `template` | One line per entry rendered from `--template` (see below) |
| `--template` | string | Go [text/template](https://pkg.go.dev/text/template) for `--format template`. Required with it, and an error without it |

### Template Output

`--format template --template '...'` renders one line per log entry and appends the newline itself. Available fields:

| Field | Type | Meaning |
|-------|------|---------|
| `.Source` | string | `pod` or `pod/container` |
| `.Time` | time.Time | kubectl timestamp in the `--tz` zone (zero if absent; use `{{.Time.Format "15:04:05"}}`) |
| `.Raw` | string | The line without the kubectl timestamp |
| `.Msg` | string | `msg` or `message` of a JSON line, else empty |
| `.Level` | string | `TRACE` to `FATAL`, empty if unknown |
| `.JSON` | map | Parsed JSON object, `nil` for plain text (`{{.JSON.requestId}}`) |
| `.Repeats` | int | With `--dedupe`: identical lines folded into this one, else 0 |

```bash
klog fetch -n shop -d checkout --since 1h --format template \
  --template '{{.Time.Format "15:04:05"}} {{.Source}} {{.Level}} {{.Msg}}'
klog tail -n shop -d checkout --format template --template '{{.JSON.requestId}}' --field requestId~.
```

A template that does not parse is a usage error at startup. A field that is missing from a JSON line prints as `<no value>`.

### Collapsing Repeats

`--dedupe` (tail and fetch) collapses consecutive identical lines per pod, ignoring the kubectl timestamp. The first line is printed, followed by one `… repeated N more times` line when the run ends (a different line arrives, the stream ends or klog is interrupted). With `--format json` and `--format template` there is no summary line: the first line is printed once the run ends, with `"repeats": N` (`.Repeats`) when N is above 0, so in those formats a line can appear late on a quiet `tail`.

```bash
klog tail -n shop -d checkout --dedupe
klog fetch -n shop -d checkout --since 1h --dedupe --format json
```

### Pretty Output Options

| Flag | Type | Default | Description |
|------|------|---------|-------------|
| `--no-flatten` | bool | false | Print JSON lines as raw JSON objects instead of flattened `LEVEL msg key=val` format |
| `--theme` | string | config dir | Path to theme file (default: `~/.config/klog/theme.json` on Linux, `~/Library/Application Support/klog/theme.json` on macOS) |
| `--tz` | string | UTC | Timezone for timestamp display (e.g. America/New_York, Local, UTC). IANA timezone identifiers are supported. |
| `NO_COLOR` | env var | — | Set to any value to disable colors in pretty output |

**Examples:**
```bash
# Pretty with custom theme
klog tail --format pretty --theme ~/.klog-themes/dark.json

# Pretty without flattening (raw JSON values)
klog tail --format pretty --no-flatten

# JSON format for processing
klog fetch --format json | jq '.json.duration'

# Raw unprocessed logs
klog tail --format raw

# Custom timezone
klog tail --tz America/New_York

# Disable colors
NO_COLOR=1 klog tail
```

### Theme File Format

Theme file is JSON with SGR codes (ANSI Select Graphic Rendition):

```json
{
  "timestamp": "2",
  "levels": {
    "trace": "2",
    "debug": "2",
    "info": "",
    "warn": "33",
    "error": "1;31",
    "fatal": "1;97;41"
  },
  "trace": {
    "frame": "2",
    "caused_by": "31",
    "omitted": "2"
  },
  "keys": "36",
  "labels": ["36", "32", "33", "35", "34", "31"]
}
```

Common SGR codes:
- `""` — default (no formatting)
- `1` — bold
- `2` — dim
- `31` — red
- `32` — green
- `33` — yellow
- `36` — cyan
- `1;31` — bold red
- `1;97;41` — bold bright white on red background

---

## Environment Variables

| Variable | Description |
|----------|-------------|
| `KLOG_KUBECTL` | Path to kubectl binary. Defaults to `kubectl` on PATH. |
| `NO_COLOR` | Set to any value to disable ANSI colors in pretty output. |
| `KLOG_PROFILES` | Path to the profiles file. Defaults to `<user config dir>/klog/profiles.json`. |

**Examples:**
```bash
KLOG_KUBECTL=/opt/kubectl klog tail -n shop -d checkout
NO_COLOR=1 klog tail --format pretty
```

---

## Exit Codes

| Code | Meaning |
|------|---------|
| `0` | Success |
| `1` | Runtime failure (kubectl error, write error, etc.) |
| `2` | Usage error (invalid flags, missing required args) |
| `3` | No pods matched the selection criteria |

---

## Limits & Constraints

- **History**: Limited to what the kubelet keeps. Logs of deleted pods are gone.
- **Ordering**: `tail` output is in arrival order, so ordering across pods is approximate. `fetch` sorts by kubectl timestamp.
- **Field paths**: Dotted paths walk nested objects only, not arrays.
- **Stack traces**: Indented lines (stack frames) attach to the preceding log line.
- **Trace follow**: `tail --follow-id` only follows lines logged after the seed; `fetch --follow-id` holds the matching lines in memory.
- **Dedupe**: Only back-to-back identical lines collapse; a repeating multi-line stack trace is not collapsed as a unit.

---

## Time Format Reference

### RFC3339 Format
```
2026-09-30T10:00:00Z
2026-09-30T10:00:00+02:00
```

### Duration Format
```
ns (nanosecond)
us (microsecond)
ms (millisecond)
s  (second)
m  (minute)
h  (hour)
d  (day, exactly 24h)

Examples: 30m, 2h, 10s, 5m30s, 2d, 1d12h
```

**Examples:**
```bash
# Fetch from 2 hours ago
klog fetch --since 2h

# Fetch from specific time
klog fetch --since-time 2026-09-30T10:00:00Z

# Fetch a specific range
klog fetch --since-time 2026-09-30T09:00:00Z --until 1h
```

---

## Common Usage Patterns

### Follow errors only
```bash
klog tail -n prod -d api-service --level ERROR
```

### Fetch errors from the last 2 hours and save to file
```bash
klog fetch -n prod -d api-service --since 2h --level ERROR --out errors.log
```

### Filter by request ID
```bash
klog tail -n shop -l app=checkout --field 'requestId=abc-123'
```

### Find slow requests (>1000ms latency)
```bash
klog tail -d api --field 'latency~[0-9]{4,}' --format json
```

### Show the whole request behind an error
```bash
klog fetch -n prod -d api --since 1h --level ERROR --follow-id traceId
```

### See what happened around a message
```bash
klog fetch -n prod -d api --since 30m --grep 'connection reset' -C 3
```

### Which pods are erroring?
```bash
klog fetch -n prod -d api --since 1h --stats
```

### Get logs from previous container instance
```bash
klog fetch -n prod -p 'api-worker' --previous --since 30m
```

### Follow multiple pods by regex with custom timezone
```bash
klog tail -n prod -p '^worker-' --tz America/New_York
```

### Export JSON logs for analysis
```bash
klog fetch -n prod -d service --since 1h --format json > logs.jsonl
```

### Disable colors and save to file
```bash
NO_COLOR=1 klog tail -n prod -d service --format pretty > output.txt
```
