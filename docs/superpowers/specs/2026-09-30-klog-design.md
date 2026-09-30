# klog: kubectl log collector design

Date: 2026-09-30
Status: draft v2 (review gaps folded in), awaiting approval

## Goal

A terminal tool that tails and fetches logs from many Kubernetes pods at once, with filters for level, JSON fields and text. It works across services, not one project.

## Decisions

| Topic | Decision |
|---|---|
| Scope | Live tail, historical fetch to file, JSON field filtering |
| Form factor | Standalone repo, Go single static binary |
| Log source | `kubectl` only. No Loki/ELK/CloudWatch in v1 |
| Approach | Go CLI that shells out to `kubectl` (not `client-go`, not `stern` + `jq`) |
| Auth | Reuses the user's kubeconfig and exec auth plugins through `kubectl` |

Rejected:
- `stern` + `jq` wrapper: no timestamp-merged history fetch to file, awkward JSON field filters, not a testable tool.
- `client-go`: heavy dependencies and auth edge cases, no benefit while `kubectl` is the only source.

## Assumptions

- `kubectl` is on the PATH and the user is logged in.
- Services log JSON lines to stdout with fields such as `level`, `requestId`, `correlationId`.
- Fetch history is limited by kubelet and node log retention. Logs of deleted pods are gone.
- Users run the tool in their own terminal.

## CLI

```
klog tail  <target> [filters] [--since 5m] [--poll 5s] [--wait] [--format pretty|json|raw]
klog fetch <target> [filters] (--since 2h | --since-time <RFC3339>) [--until <RFC3339|duration-ago>] [--previous] [--out file] [--format pretty|json|raw]
```

Target: `-n <namespace>` plus one of `-l <label-selector>`, a deployment name (resolved to its selector), or a pod-name regex. Also `--context` and `-c <container>`.

Containers: without `-c`, every regular container of every matched pod is streamed (init and ephemeral containers are not). The output prefix is `pod` for single-container pods and `pod/container` otherwise. `-c` is a regex on container names.

Flags:
- `--since`: duration. `tail` default 5m of backlog before following. `fetch` requires `--since` or `--since-time` (mutually exclusive).
- `--since-time`, `--until`: absolute RFC3339 time. `--until` also accepts a duration meaning "that long ago". Applied client-side for `--until`.
- `--poll`: pod re-resolve interval for `tail`, default 5s.
- `--wait`: `tail` keeps polling when zero pods match instead of exiting.
- `--out`: `fetch` only. Writes to file atomically (temp file + rename).
- `--format`: `pretty` (default on a TTY), `json` (one object per line), `raw` (line as emitted by the pod, no prefix).

Filters (combined with AND):
- `--level WARN`: WARN and above. See "Level detection".
- `--field key=value`, `key!=value`, `key~regex`: repeatable, matches JSON fields.
- `--grep`, `--exclude`: regexes on the raw line.

Non-JSON lines pass through as raw text. Only `--grep` and `--exclude` apply to them, and any `--level` or `--field` filter drops them. A stack-trace continuation line stays attached to the preceding matching line.

### Level detection

- Key lookup order: `level`, `severity`, `lvl`. First present key wins. Values are matched case-insensitively.
- Order: `TRACE` < `DEBUG` < `INFO` < `WARN` < `ERROR` < `FATAL`. Aliases: `WARNING` = `WARN`, `ERR` = `ERROR`, `CRITICAL` and `PANIC` = `FATAL`.
- Numeric levels (for example pino 10..60) are not supported in v1.
- Under `--level`, a JSON line with no recognised level key or an unknown value is dropped. The tool cannot prove it meets the threshold. `--field` can match such lines explicitly.
- `--level` with an unknown name is a usage error.

### Timestamps

The kubectl `--timestamps` prefix is the only timestamp the tool uses: for `fetch` sorting, `--until`, and the retention-gap warning. A JSON `time` or `timestamp` field is ordinary data. `--field` can filter on it, and the tool never reads it for ordering.

## Components

Each is a Go package under `internal/` with one job:

1. `resolve`: target to pod and container list via `kubectl get -o json`.
2. `run`: spawns `kubectl logs --timestamps` per pod and container and emits lines. The kubectl command is injectable.
3. `parse`: reads the kubectl timestamp prefix, tries JSON, falls back to raw.
4. `filter`: pure functions over parsed lines.
5. `merge`: `tail` emits in arrival order, `fetch` sorts by timestamp.
6. `render`: pretty or JSON output with a colored pod prefix.

Layout: `cmd/klog/`, `internal/{resolve,run,parse,filter,merge,render}/`, GoReleaser config for mac and linux.

## Data flow

```
resolve -> pod/container list -> run (1 goroutine + 1 kubectl process each)
  -> raw lines channel -> parse -> filter -> merge -> render -> stdout / --out
```

`tail`:
- The resolver re-polls pods every 5s (`--poll`). New pods get a runner, deleted pods lose theirs.
- Runners use `kubectl logs -f --since=<n>`.
- Output is in arrival order, so cross-pod ordering is approximate.

`fetch`:
- Runners use `--since` / `--since-time` and optionally `--previous`. `--until` is applied client-side.
- A bounded heap-merge sorts by kubectl timestamp, so memory is bounded by pod count, not log size.
- Output goes to stdout or `--out`, written atomically.

Backpressure: channels are bounded. A slow terminal slows the runners and nothing is dropped.

Ctrl-C: context cancel kills all child `kubectl` processes and exits cleanly.

## Error handling

- `kubectl` missing or not logged in: fail fast before streaming, print the kubectl stderr line and a hint.
- Zero pods matched: exit code 3 and echo the selector. `tail --wait` keeps polling instead.
- One pod stream dies: print `[pod-x] stream ended: <reason>`. Retry `tail` runners with backoff, up to 3 times. Other pods continue.
- Bad filter syntax: usage error before any kubectl call.
- Retention gap: `fetch` warns when the earliest returned line is later than `--since`.
- The tool never logs kubeconfig contents or tokens.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Success, including a clean Ctrl-C of `tail` |
| 1 | Runtime failure: `kubectl` missing or failing, all streams dead after retries, `--out` write error |
| 2 | Usage error: bad flag, bad filter syntax, mutually exclusive flags |
| 3 | Zero pods matched (without `--wait`) |

A single dead pod stream does not change the exit code when other streams finish normally.

## Testing

- Unit, table-driven: parser, filter chain, merger ordering, stack-trace attach rule.
- Fake `kubectl` binary through the injectable command: stream death, slow consumer, Ctrl-C cleanup.
- Optional integration test against `kind`, behind a build tag, not in default CI.

## Out of scope for v1

Pluggable log backend, config files and presets, TUI, metrics.

## Clarifications from planning

Decisions made while writing the implementation plan. They refine the sections above and change no settled decision.

- Target flags: `-l <selector>`, `-d <deployment>`, `-p <pod-regex>`. Exactly one is required. `-n` is optional and defaults to the kubeconfig context's namespace. `-c` is a regex.
- `--format` defaults to `pretty` everywhere. Colour is used only on a TTY with `NO_COLOR` unset, and never with `--out`.
- `--format json` writes one object per line: `{"source","time","raw","json"}`. `time` and `json` are omitted when absent.
- `KLOG_KUBECTL` overrides the kubectl binary path.
- Durations use Go syntax, so the largest unit is `h`.
- Field filters read top-level JSON keys only. A missing key fails `=` and `~` and satisfies `!=`.
- A stack-trace continuation line starts with whitespace, `Caused by:`, `Suppressed:` or `... N more`. With no filter set, every line is kept.
- `tail` retries a stream only when kubectl exits with an error: up to 3 times with 1s, 2s and 4s backoff. The counter resets when an attempt yields lines. Retries resume with `--since-time` and drop lines at or before the last timestamp seen. A clean EOF (container exited) is not retried. The stream restarts only when the container's `restartCount` rises.
- `tail` warns and keeps going when a poll fails after startup. The first resolve failing exits 1.
- `fetch` exits 1 when every stream failed or when it is interrupted. An interrupted `fetch` discards the `--out` temp file.
- The retention-gap warning is per stream: it fires when the stream's first line is later than the requested start.
