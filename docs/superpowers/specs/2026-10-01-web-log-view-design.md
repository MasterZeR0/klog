# klog web log view: design

Status: approved in conversation, 2026-10-01. Next step: implementation plan.

## Goal

Make live logs easier to search by showing `klog tail` output in a browser.

`klog tail ... --web` serves a page on the loopback interface. The page shows
the lines `tail` produces and lets the user search them. `tail` still picks
pods and applies filters exactly as it does today.

Scope decisions (from the conversation):

- Search is **client-side only**: the browser searches the lines already
  streamed. Changing pods, level or time range means restarting klog.
- The feature attaches as a **`--web` flag on `tail`**, not a new command and
  not a pipe.
- `fetch` is out of scope.

## Non-goals

- `fetch --web`.
- Authentication, or binding to a non-loopback address.
- History beyond the in-memory ring (see Hub).
- Changing filters or targets from the browser (no control API).
- Opening the browser automatically.
- Virtual scrolling in the page.
- Persisting anything to disk.

## Usage

```
klog tail -n shop -d checkout --level WARN --web
klog: web UI at http://127.0.0.1:41873
```

New flags on `tail` only:

| Flag | Default | Meaning |
|------|---------|---------|
| `--web` | off | serve the browser view; stdout stays empty |
| `--web-addr` | `127.0.0.1:0` | listen address; port `0` picks a free port |

Rules, all checked before any kubectl call (exit 2 on violation):

- `--web-addr` without `--web` is a usage error.
- `--web-addr` must be a loopback address (`127.0.0.1`, `::1` or `localhost`).
  Logs are sensitive and there is no auth.
- `--web` with `--out` is a usage error. `--web` with `--format` or
  `--template` is a usage error (the browser always receives the JSON
  record).
- `--tz`, `--theme` and `--no-flatten` have no effect with `--web`: the
  browser formats time in local time and styles rows itself. They are not
  errors, so a saved profile keeps working.
- `--dedupe` works. Folded lines arrive with `repeats` set and the page shows
  `… repeated N more times` (`1 more time` for one), as the terminal does.

The URL is printed to stderr after pods resolve and the listener is bound. A
run that exits 3 (no pods) or 1 (kubectl failure) prints no URL. All other
exit codes and `--wait` behave as today. Ctrl-C stops the server and the
streams, exit 0.

## Architecture

```
resolve -> run -> parse -> filter -> out chan -> Renderer(JSON) -> Hub -> SSE -> browser
```

Only the last three stages are new. `runTail`'s loop, restarts, polling and
filters are unchanged.

### `internal/web` (new)

One job: hold recent records and serve them to browsers. Stdlib only
(`net/http`, `embed`).

**`Hub`** implements `io.Writer`.

- The renderer in `Format: JSON` writes one record per `Write` call
  (`json.Encoder.Encode` issues a single `Write`, newline included). `Hub.Write`
  copies the bytes, assigns the next sequence number and stores the record in a
  ring of the last **20,000** records, and of at most **64 MiB** of record
  bytes: the oldest records are evicted first, but the newest is always kept, so
  one very long line is still delivered once.
- Sequence numbers (the SSE ids) start at the process start time in
  nanoseconds, so a restarted klog always issues ids above any earlier run's. A
  page that reconnects with an id from the old run then sees a gap and is
  replayed everything the new run holds, instead of silently skipping records.
  (Added after QA found records lost across a restart.)
- `Hub.Write` never returns an error and never blocks on a browser.
- Subscribers each own a bounded queue (256 records). If a subscriber's queue
  is full, the hub closes that subscriber. This is a deliberate difference from
  the terminal path, where a slow reader slows the runners: a stalled browser
  tab must not stall kubectl.
- `Hub.Subscribe(afterSeq)` returns the ring records with `seq > afterSeq`,
  then live records. Replay and live handoff happen under one lock so no
  record is lost or duplicated.

**`Server`** wraps the hub in an `http.Handler`.

- `GET /` serves one embedded `index.html` (`go:embed`).
- `GET /events` is a Server-Sent Events stream. Each event is
  `id: <seq>` and `data: <JSON record>`. A comment line (`: ping`) goes out
  every 15 s so proxies and the browser notice a dead connection.
- `EventSource` reconnects on its own after a drop and sends `Last-Event-ID`,
  so a reload or a slow-client disconnect resumes from the ring without a gap,
  as long as the gap is within 20,000 records. If the ring has already dropped
  records the client asked for, the stream starts at the oldest record held and
  sends an `event: gap` first so the page can show "some lines were skipped".
- Host check: any request whose `Host` header is not the bound address,
  `localhost:<port>`, `127.0.0.1:<port>` or `[::1]:<port>` gets 403.
- The listener must really be bound to a loopback IP: after `Listen`, a
  non-loopback address (for example `localhost` resolving elsewhere) is an
  error and the listener is closed. This blocks DNS
  rebinding. There are no write endpoints, so no CSRF surface.
- `Server.Shutdown(ctx)` on exit; open SSE streams are closed.

### `cmd/klog/tail.go`

When `--web` is set:

1. Build the hub, bind the listener, start serving in a goroutine.
2. `view.Format = render.JSON`, `renderer = render.New(hub, view)`.
3. Print the URL to stderr.
4. The existing loop runs unchanged. On exit, shut the server down after
   `wg.Wait()`.

The flag and validation code lives next to the other `tail` flags. The
listener is bound after `resolve.Resolve` succeeds, so a failing run never
prints a URL. A bind failure is exit 1 with `klog: --web: <error>`.

## Browser page

One file, vanilla JS, no build step, no external requests (works offline).

- **Search box.** Substring match by default; toggles for regex and case.
  Non-matching rows are hidden, not just highlighted. An invalid regex shows
  an inline error and keeps the previous results.
- **Stack traces.** A continuation line (leading whitespace, `Caused by:`,
  `Suppressed:`, `... N more` or `... N common frames omitted`, the same rule
  as `filter.IsContinuation`) joins the record of the previous line from the
  same source. A record (a line plus its continuation lines) is shown when any
  of its lines matches the search, so a trace is never split from its parent
  and searching for text inside a trace finds the whole record. (Revised after
  approval: the first draft made a continuation inherit its parent's match,
  which could never find text that appears only in a trace.)
- **Level colour** from `json.level`, `json.severity` or `json.lvl`
  (case-insensitive). Rows without a level are uncoloured.
- **Pod chips.** One chip per source seen; click to hide or show it.
- **Row detail.** Click a row to expand its JSON.
- **Controls.** Pause/resume auto-scroll (auto-pauses when the user scrolls up)
  and clear.
- **Limit.** The page keeps at most 20,000 rows and drops the oldest.
- **Status.** A small indicator shows connected, reconnecting, or "lines
  skipped" (the `gap` event).
- Time is shown in the browser's local time from the record's `time` field.
  Rows without a timestamp show none.

## Error handling

| Situation | Behaviour |
|-----------|-----------|
| Bad flag combination | exit 2, one-line message, zero kubectl calls |
| Port in use / bind failure | exit 1, `klog: --web: ...` |
| No pods, no `--wait` | exit 3, no URL (as today) |
| Browser tab slow or gone | hub drops that subscriber; `tail` unaffected |
| Browser reconnects | resumes via `Last-Event-ID`; `gap` event if records were lost |
| Wrong `Host` header | 403 |
| Ctrl-C | streams stop, server shuts down, exit 0 |

## Testing

Table-driven, stdlib `testing`, run under `-race`, in the existing style.

- `internal/web` Hub: ring eviction at capacity; replay after `afterSeq`;
  replay-to-live handoff loses and duplicates nothing under concurrent
  writes; a full subscriber queue closes that subscriber and does not block
  `Write`.
- `internal/web` Server (`httptest`): `/` serves the page; `/events` frames
  (`id:`, `data:`); `Last-Event-ID` resume; `gap` event; keep-alive comment;
  Host check (allowed and rejected hosts).
- `cmd/klog`: `tail --web` against the fake kubectl in `internal/testutil`:
  the SSE stream carries filtered records and stdout stays empty. Usage-error
  cases (`--web-addr` alone, non-loopback address, `--out`, `--format`) exit 2
  with zero kubectl calls. `--dedupe` surfaces `repeats`.
- Browser JS: manual check against a live `tail --web` (search, regex toggle,
  trace attachment, reconnect). No JS unit tests.

## Docs

- `README.md`: one example and the two flags.
- `COMMANDS.md`: `--web` and `--web-addr` in the `tail` reference.

## Limits (to state in the docs)

- Search covers only what the page holds: the last 20,000 lines (or 64 MiB)
  the server kept, and at most 20,000 rows in the tab. Older lines need a restart with a
  larger `--since`.
- Constants (ring size, queue size, row limit) are not flags. Add flags if
  real use shows a need.
- One klog process, one user, loopback only.
