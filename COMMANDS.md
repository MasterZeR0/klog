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

**Example:**
```bash
klog tail -n shop -d checkout --level WARN --since 30m
klog tail -n shop -l app=web --poll 10s --wait
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

**Example:**
```bash
klog fetch -n shop -d checkout --since 2h --level ERROR
klog fetch -n shop -l app=web --since-time 2026-09-30T10:00:00Z --until 30m
klog fetch -n prod -p '^api-' --since 1h --out errors.log --format json
```

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
| `--field` | string | Filter on top-level JSON keys. Repeatable. Non-JSON lines are dropped. |

**Syntax:**
- `key=value` — exact match
- `key!=value` — not equal (also matches missing keys)
- `key~regex` — regex match on the value

**Examples:**
```bash
# Exact match
klog tail --field 'requestId=abc-123'

# Not equal (missing key counts as not equal)
klog tail --field 'status!=200'

# Regex match
klog tail --field 'host~^api\.prod'

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

## Output Formats

| Flag | Value | Description |
|------|-------|-------------|
| `--format` | `pretty` | (default) Pretty-printed with colors, JSON flattened to `LEVEL msg key=val ...`. Terminal colors via `NO_COLOR` env var and theme file. |
| `--format` | `json` | One JSON object per line with keys: `source`, `time`, `raw`, `json` |
| `--format` | `raw` | Raw log lines with no parsing or filtering applied |

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
- **Stack traces**: Indented lines (stack frames) attach to the preceding log line.

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
