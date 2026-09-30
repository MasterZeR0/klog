# klog

Tail and fetch logs from many Kubernetes pods at once, with level, JSON field and text filters. It shells out to `kubectl`, so it uses your kubeconfig and login as they are.

## Install

```
go install klog/cmd/klog@latest   # or download a release archive
```

Needs `kubectl` on the PATH. Set `KLOG_KUBECTL` to use another binary.

## Configuration

[CONFIG.md](CONFIG.md) covers themes, environment variables, kubeconfig setup, and customization.

## Usage

Pick pods with exactly one of `-l <label-selector>`, `-d <deployment>` or `-p <pod-regex>`. Add `-n <namespace>`, `--context <ctx>` and `-c <container-regex>` as needed.

**For a complete reference of all commands, flags and features, see [COMMANDS.md](COMMANDS.md).**

```
klog tail  -n shop -d checkout --level WARN
klog tail  -n shop -l app=web --field 'requestId=abc-123' --grep timeout
klog fetch -n shop -d checkout --since 2h --level ERROR --out errors.log
klog fetch -n shop -p '^web-' --since-time 2026-09-30T10:00:00Z --until 30m --format json
```

Filters (all must match):

- `--level WARN`: WARN and above. Reads `level`, `severity` or `lvl` from JSON lines. Lines without a recognised level are dropped.
- `--field key=value`, `key!=value`, `key~regex`: top-level JSON keys, repeatable. Non-JSON lines are dropped. A missing key matches `!=` only.
- `--grep`, `--exclude`: regexes on the raw line. Indented stack-trace lines stay attached to the line before them.

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

Exit codes: 0 ok, 1 runtime failure, 2 usage error, 3 no pods matched.

## Limits

- History is whatever the kubelet still keeps. Logs of deleted pods are gone.
- `tail` output is in arrival order, so ordering across pods is approximate. `fetch` sorts by kubectl timestamp.
- Durations accept Go units up to hours (`48h`, not `2d`).
- Numeric log levels (for example pino `30`) are not supported.

## Test

```
go test ./...
KLOG_IT_CONTEXT=kind-klog-it go test -tags integration ./integration/   # needs a cluster
```
