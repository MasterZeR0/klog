# klog web log view Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `klog tail --web` serves the live log lines to a browser page with client-side search.

**Architecture:** A new `internal/web` package holds a `Hub` (an `io.Writer` with a 20,000-record ring and bounded per-subscriber queues) and an HTTP handler that serves one embedded page plus a Server-Sent Events stream. `tail` swaps its renderer's writer for the hub and forces the JSON record format; the existing tail loop, filters and retries do not change.

**Tech Stack:** Go 1.22, stdlib only (`net/http`, `embed`), vanilla JS in one HTML file.

**Spec:** `docs/superpowers/specs/2026-10-01-web-log-view-design.md`

## Global Constraints

- Go 1.22, stdlib only, no new dependencies (`go.mod` stays `go 1.22` with no `require`).
- Binds loopback addresses only (`127.0.0.1`, `::1`, `localhost`); default `--web-addr` is `127.0.0.1:0`.
- No write endpoints; `Host` header must be the bound address, `localhost:<port>` or `127.0.0.1:<port>` (also `[::1]:<port>`), else 403.
- Hub ring: 20,000 records. Per-subscriber queue: 256 records. Browser row limit: 20,000. These are constants, not flags.
- A slow or stalled browser must never block kubectl streams: the hub drops that subscriber.
- `--web` is a flag on `tail` only; `fetch` is untouched.
- `--web` with `--out`, `--format` or `--template` is a usage error (exit 2); `--web-addr` without `--web` is a usage error; non-loopback `--web-addr` is a usage error. All checked before any kubectl call.
- Exit codes unchanged: 0 ok / Ctrl-C, 1 runtime failure (bind failure included), 2 usage, 3 no pods. No URL is printed on exit 3 or on a kubectl failure.
- Log content is untrusted: the page must render it with `textContent` only.
- Table-driven stdlib `testing`, run with `-race`, in the existing style. Comments only where the WHY is not obvious.
- Commit messages: follow the `commit` skill conventions; the repo's history uses plain Conventional Commits (`feat: ...`, `docs: ...`). End each with `Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **A very long log line** (200 KiB+ in one record): must reach the browser whole, not truncated. Pinned by a server test that reads with `bufio.Reader`, not `bufio.Scanner`.
2. **Log content containing HTML** (`<script>`, `<img onerror=...>`): must display as text and never execute. Pinned by a test that forbids `innerHTML`-style APIs in `index.html`, plus a CSP header test and a manual check.
3. **A garbage `Last-Event-ID`** (`abc`, `-1`, a 25-digit number): must behave like a fresh connection (replay from the oldest record, no crash, no `gap`). Pinned in the server tests.
4. **The listen port is already in use**: exit 1 with `klog: --web: ...`, no hang, no URL. Pinned in the tail tests.
5. **Ctrl-C while a browser stream is open**: tail must exit 0 promptly instead of waiting on the open connection. Pinned in the tail tests.

---

## File Structure

| File | Responsibility |
|------|----------------|
| `internal/web/hub.go` (new) | `Hub`: ring buffer, sequence numbers, subscribers, drop-slow-subscriber. No HTTP. |
| `internal/web/hub_test.go` (new) | Hub behaviour. |
| `internal/web/server.go` (new) | `CheckAddr`, `Handler`, `Start`/`Server`, SSE framing, Host check, embeds `index.html`. |
| `internal/web/server_test.go` (new) | HTTP behaviour via `httptest`. |
| `internal/web/index.html` (new) | The browser page. Task 2 adds a one-line stub so the package compiles; Task 4 replaces it. |
| `cmd/klog/web.go` (new) | `checkWeb` flag validation and `startWeb` for `tail`. |
| `cmd/klog/tail.go` (modify) | Two flags, one validation call, a small block that swaps the renderer's writer. |
| `cmd/klog/tail_web_test.go` (new) | End-to-end `tail --web` tests with the fake kubectl. |
| `README.md`, `COMMANDS.md` (modify) | Document the flags and the limits. |

---

### Task 1: Hub

**Files:**
- Create: `internal/web/hub.go`
- Test: `internal/web/hub_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const RingSize = 20000`, `const QueueSize = 256`
  - `type Event struct { Seq uint64; Data []byte }` (`Data` is one JSON record, no trailing newline)
  - `func NewHub(ring, queue int) *Hub`
  - `func (h *Hub) Write(p []byte) (int, error)` (one record per call; never errors, never blocks)
  - `func (h *Hub) Subscribe(after uint64) *Sub`
  - `type Sub struct { Replay []Event; Gap bool; Live <-chan Event }`, `func (s *Sub) Close()`
  - `func (h *Hub) Close()`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/hub_test.go`:

```go
package web

import (
	"fmt"
	"reflect"
	"testing"
	"time"
)

func rec(i int) []byte { return []byte(fmt.Sprintf("{\"n\":%d}\n", i)) }

func seqs(es []Event) []uint64 {
	var out []uint64
	for _, e := range es {
		out = append(out, e.Seq)
	}
	return out
}

// subscribers is a test hook: how many subscribers the hub still serves.
func (h *Hub) subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func TestHubReplayAfter(t *testing.T) {
	h := NewHub(5, 4)
	for i := 1; i <= 3; i++ {
		h.Write(rec(i))
	}
	for _, tc := range []struct {
		name  string
		after uint64
		want  []uint64
	}{
		{"fresh", 0, []uint64{1, 2, 3}},
		{"resume", 1, []uint64{2, 3}},
		{"up to date", 3, nil},
		{"id from another run", 99, []uint64{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := h.Subscribe(tc.after)
			defer s.Close()
			if got := seqs(s.Replay); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("replay %v, want %v", got, tc.want)
			}
			if s.Gap {
				t.Fatal("unexpected gap")
			}
		})
	}
}

func TestHubEvictsOldestAndReportsGap(t *testing.T) {
	h := NewHub(3, 4)
	for i := 1; i <= 5; i++ {
		h.Write(rec(i)) // the ring now holds 3, 4, 5
	}
	for _, tc := range []struct {
		name  string
		after uint64
		want  []uint64
		gap   bool
	}{
		{"fresh connection is never a gap", 0, []uint64{3, 4, 5}, false},
		{"asked for evicted records", 1, []uint64{3, 4, 5}, true},
		{"next record is the oldest held", 2, []uint64{3, 4, 5}, false},
		{"resume inside the ring", 4, []uint64{5}, false},
		{"up to date", 5, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := h.Subscribe(tc.after)
			defer s.Close()
			if got := seqs(s.Replay); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("replay %v, want %v", got, tc.want)
			}
			if s.Gap != tc.gap {
				t.Fatalf("gap %v, want %v", s.Gap, tc.gap)
			}
		})
	}
}

func TestHubWriteCopiesAndTrimsNewline(t *testing.T) {
	h := NewHub(3, 4)
	p := []byte("{\"a\":1}\n")
	h.Write(p)
	copy(p, "XXXXXXXX") // the caller reuses its buffer
	s := h.Subscribe(0)
	defer s.Close()
	if got := string(s.Replay[0].Data); got != `{"a":1}` {
		t.Fatalf("data %q", got)
	}
}

func TestHubReplayThenLiveIsContiguous(t *testing.T) {
	const n = 3000
	h := NewHub(n, n)
	half := make(chan struct{})
	go func() {
		for i := 1; i <= n; i++ {
			h.Write(rec(i))
			if i == n/2 {
				close(half)
			}
		}
	}()
	<-half
	s := h.Subscribe(0) // writes keep landing while we subscribe
	defer s.Close()
	got := seqs(s.Replay)
	timeout := time.After(5 * time.Second)
	for len(got) < n {
		select {
		case e, ok := <-s.Live:
			if !ok {
				t.Fatal("live channel closed")
			}
			got = append(got, e.Seq)
		case <-timeout:
			t.Fatalf("got %d of %d records", len(got), n)
		}
	}
	if len(got) != n {
		t.Fatalf("got %d records, want %d", len(got), n)
	}
	for i, seq := range got {
		if seq != uint64(i+1) {
			t.Fatalf("position %d has seq %d: lost or duplicated record", i, seq)
		}
	}
}

func TestHubDropsSlowSubscriber(t *testing.T) {
	h := NewHub(10, 2)
	s := h.Subscribe(0)
	for i := 1; i <= 3; i++ {
		h.Write(rec(i)) // the third overflows the queue of 2 and must not block
	}
	if h.subscribers() != 0 {
		t.Fatal("slow subscriber is still registered")
	}
	var got []uint64
	for e := range s.Live { // closed after its two queued records
		got = append(got, e.Seq)
	}
	if want := []uint64{1, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("queued %v, want %v", got, want)
	}
	h.Write(rec(4)) // the hub keeps working and keeps the record
	s2 := h.Subscribe(0)
	defer s2.Close()
	if len(s2.Replay) != 4 {
		t.Fatalf("replay has %d records, want 4", len(s2.Replay))
	}
}

func TestHubSubCloseIsIdempotent(t *testing.T) {
	h := NewHub(3, 2)
	s := h.Subscribe(0)
	if h.subscribers() != 1 {
		t.Fatal("subscriber not registered")
	}
	s.Close()
	s.Close()
	if h.subscribers() != 0 {
		t.Fatal("subscriber still registered after Close")
	}
}

func TestHubClose(t *testing.T) {
	h := NewHub(3, 2)
	h.Write(rec(1))
	s := h.Subscribe(0)
	h.Close()
	if _, ok := <-s.Live; ok {
		t.Fatal("live channel still open after Hub.Close")
	}
	s.Close() // must not panic or double-close
	s2 := h.Subscribe(0)
	if len(s2.Replay) != 1 {
		t.Fatalf("replay %d, want 1", len(s2.Replay))
	}
	if _, ok := <-s2.Live; ok {
		t.Fatal("subscriber of a closed hub must get a closed channel")
	}
	h.Write(rec(2)) // writing after Close is harmless
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/web/ -race`
Expected: FAIL to build (`undefined: NewHub`, `undefined: Event`).

- [ ] **Step 3: Write the implementation**

Create `internal/web/hub.go`:

```go
// Package web serves the live log stream to a browser over Server-Sent Events.
package web

import (
	"bytes"
	"sync"
)

const (
	RingSize  = 20000 // records kept for replay
	QueueSize = 256   // live records buffered per subscriber
)

// Event is one record. Seq is its SSE id; Data is the JSON record without a
// trailing newline.
type Event struct {
	Seq  uint64
	Data []byte
}

// Hub keeps the last records written to it and fans them out to subscribers.
// It is an io.Writer: each Write call must be exactly one JSON record, which is
// what render.Renderer produces in JSON format.
type Hub struct {
	mu     sync.Mutex
	buf    []Event // ring of len(buf) slots
	start  int     // slot of the oldest record
	n      int     // records held
	next   uint64  // sequence number of the next record; the first is 1
	queue  int
	subs   map[*Sub]struct{}
	closed bool
}

func NewHub(ring, queue int) *Hub {
	return &Hub{buf: make([]Event, ring), next: 1, queue: queue, subs: map[*Sub]struct{}{}}
}

// Write stores one record and hands it to every subscriber. It never blocks:
// a subscriber whose queue is full is dropped and must reconnect, so a stalled
// browser tab cannot stall kubectl.
func (h *Hub) Write(p []byte) (int, error) {
	data := bytes.TrimSuffix(p, []byte("\n"))
	if len(data) == 0 {
		return len(p), nil
	}
	data = append([]byte(nil), data...) // the caller reuses p
	h.mu.Lock()
	defer h.mu.Unlock()
	e := Event{Seq: h.next, Data: data}
	h.next++
	if h.n < len(h.buf) {
		h.buf[(h.start+h.n)%len(h.buf)] = e
		h.n++
	} else {
		h.buf[h.start] = e
		h.start = (h.start + 1) % len(h.buf)
	}
	for s := range h.subs {
		select {
		case s.ch <- e:
		default:
			h.drop(s)
		}
	}
	return len(p), nil
}

// Sub is one browser's view: the records it missed, then the live ones.
type Sub struct {
	Replay []Event      // records after the requested id, oldest first
	Gap    bool         // the client asked for records the ring already evicted
	Live   <-chan Event // closed when the hub drops or closes this subscriber
	ch     chan Event
	h      *Hub
}

// Subscribe returns the records with Seq > after plus a channel of later ones.
// Replay and registration happen under one lock, so no record is lost or
// repeated between them. after == 0 is a fresh connection; an id this hub never
// issued (a page from an earlier run) is treated the same way.
func (h *Hub) Subscribe(after uint64) *Sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	if after >= h.next {
		after = 0
	}
	s := &Sub{h: h, ch: make(chan Event, h.queue)}
	s.Live = s.ch
	oldest := h.next - uint64(h.n)
	s.Gap = after > 0 && after+1 < oldest
	skip := 0
	if after >= oldest {
		skip = int(after - oldest + 1)
	}
	s.Replay = make([]Event, 0, h.n-skip)
	for i := skip; i < h.n; i++ {
		s.Replay = append(s.Replay, h.buf[(h.start+i)%len(h.buf)])
	}
	if h.closed {
		close(s.ch)
		return s
	}
	h.subs[s] = struct{}{}
	return s
}

// Close unsubscribes. It is safe to call more than once and after the hub
// dropped or closed this subscriber.
func (s *Sub) Close() {
	s.h.mu.Lock()
	defer s.h.mu.Unlock()
	if _, ok := s.h.subs[s]; ok {
		s.h.drop(s)
	}
}

// Close ends every subscription so open event streams return. The hub still
// accepts writes afterwards.
func (h *Hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.subs {
		h.drop(s)
	}
}

// drop removes s and closes its channel; the caller holds h.mu and s is registered.
func (h *Hub) drop(s *Sub) {
	delete(h.subs, s)
	close(s.ch)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/web/ -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web/hub.go internal/web/hub_test.go docs/superpowers/specs/2026-10-01-web-log-view-design.md docs/superpowers/plans/2026-10-01-web-log-view.md
git commit -m "feat: add web hub with replay ring and slow-subscriber drop" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 2: HTTP server and SSE stream

**Files:**
- Create: `internal/web/server.go`
- Create: `internal/web/index.html` (one-line stub; Task 4 replaces it)
- Test: `internal/web/server_test.go`

**Interfaces:**
- Consumes: `Hub`, `Sub`, `Event`, `NewHub`, `Hub.Subscribe`, `Hub.Close` from Task 1.
- Produces:
  - `func CheckAddr(addr string) error` (loopback `host:port` validation; the error text is user-facing)
  - `func Handler(hub *Hub, port string) http.Handler`
  - `func Start(addr string, hub *Hub) (*Server, error)`
  - `func (s *Server) URL() string` (for example `http://127.0.0.1:41873`)
  - `func (s *Server) Close()` (closes the hub's subscriptions, then shuts the HTTP server down within 2 s)
  - package var `pingEvery = 15 * time.Second` (read once when `Handler` is built)
  - `var indexHTML []byte` (embedded `index.html`)

- [ ] **Step 1: Create the stub page**

Create `internal/web/index.html` with exactly:

```html
<!doctype html><meta charset="utf-8"><title>klog</title>
```

- [ ] **Step 2: Write the failing tests**

Create `internal/web/server_test.go`:

```go
package web

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T, hub *Hub) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	ts.Config.Handler = Handler(hub, port)
	ts.Start()
	t.Cleanup(func() {
		hub.Close() // ends open event streams; ts.Close waits for them
		ts.Close()
	})
	return ts
}

type stream struct {
	resp *http.Response
	r    *bufio.Reader
}

// open connects to /events. Do returns once the server flushed the headers.
func open(t *testing.T, ts *httptest.Server, lastID string) *stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/events", nil)
	if lastID != "" {
		req.Header.Set("Last-Event-ID", lastID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return &stream{resp, bufio.NewReader(resp.Body)}
}

// next returns the next frame: its lines up to the blank line, joined by "\n".
// It reads with a Reader, not a Scanner, so long lines are not truncated.
func (s *stream) next(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for {
		line, err := s.r.ReadString('\n')
		if err != nil {
			t.Fatalf("stream ended: %v (partial frame %q)", err, b.String())
		}
		line = strings.TrimSuffix(line, "\n")
		if line == "" {
			return b.String()
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
}

func waitSubs(t *testing.T, h *Hub, n int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if h.subscribers() == n {
			return
		}
	}
	t.Fatalf("subscribers = %d, want %d", h.subscribers(), n)
}

func TestCheckAddr(t *testing.T) {
	for addr, ok := range map[string]bool{
		"127.0.0.1:0":      true,
		"127.0.0.1:8080":   true,
		"localhost:8080":   true,
		"[::1]:9000":       true,
		"0.0.0.0:0":        false,
		":8080":            false, // empty host means every interface
		"192.168.1.5:80":   false,
		"example.com:80":   false,
		"8080":             false,
		"127.0.0.1":        false,
		"127.0.0.1:99999":  false,
		"127.0.0.1:x":      false,
		"127.0.0.1:-1":     false,
		"":                 false,
	} {
		if err := CheckAddr(addr); (err == nil) != ok {
			t.Errorf("CheckAddr(%q) = %v, want ok=%v", addr, err, ok)
		}
	}
}

func TestServerServesPage(t *testing.T) {
	ts := newTestServer(t, NewHub(3, 2))
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !bytes.Equal(body, indexHTML) {
		t.Fatalf("status %d, body %d bytes", resp.StatusCode, len(body))
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content type %q", ct)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "connect-src 'self'") {
		t.Fatalf("content security policy %q", csp)
	}
	resp2, err := http.Get(ts.URL + "/nope")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("/nope status %d, want 404", resp2.StatusCode)
	}
}

func TestServerEventFramesReplayThenLive(t *testing.T) {
	hub := NewHub(10, 4)
	hub.Write(rec(1))
	hub.Write(rec(2))
	ts := newTestServer(t, hub)
	s := open(t, ts, "")
	if got, want := s.next(t), "id: 1\ndata: {\"n\":1}"; got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
	if got, want := s.next(t), "id: 2\ndata: {\"n\":2}"; got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
	hub.Write(rec(3))
	if got, want := s.next(t), "id: 3\ndata: {\"n\":3}"; got != want {
		t.Fatalf("live frame %q, want %q", got, want)
	}
	if ct := s.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
}

func TestServerLastEventIDResumes(t *testing.T) {
	hub := NewHub(10, 4)
	for i := 1; i <= 3; i++ {
		hub.Write(rec(i))
	}
	s := open(t, newTestServer(t, hub), "2")
	if got, want := s.next(t), "id: 3\ndata: {\"n\":3}"; got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
}

func TestServerGapEvent(t *testing.T) {
	hub := NewHub(2, 4)
	for i := 1; i <= 5; i++ {
		hub.Write(rec(i)) // the ring holds 4 and 5
	}
	s := open(t, newTestServer(t, hub), "1")
	if got, want := s.next(t), "event: gap\ndata: {}"; got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
	if got, want := s.next(t), "id: 4\ndata: {\"n\":4}"; got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
}

func TestServerBadLastEventIDActsLikeFreshConnection(t *testing.T) {
	hub := NewHub(10, 4)
	hub.Write(rec(1))
	ts := newTestServer(t, hub)
	for _, id := range []string{"abc", "-1", "1.5", "99999999999999999999999", "18446744073709551615"} {
		t.Run(id, func(t *testing.T) {
			s := open(t, ts, id)
			if got, want := s.next(t), "id: 1\ndata: {\"n\":1}"; got != want {
				t.Fatalf("frame %q, want %q", got, want)
			}
		})
	}
}

func TestServerSendsKeepAlivePings(t *testing.T) {
	old := pingEvery
	pingEvery = 20 * time.Millisecond
	t.Cleanup(func() { pingEvery = old })
	s := open(t, newTestServer(t, NewHub(3, 2)), "")
	if got := s.next(t); got != ": ping" {
		t.Fatalf("frame %q, want a ping comment", got)
	}
}

func TestServerRefusesForeignHost(t *testing.T) {
	ts := newTestServer(t, NewHub(3, 2))
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(ts.URL, "http://"))
	for host, want := range map[string]int{
		"127.0.0.1:" + port:    200,
		"localhost:" + port:    200,
		"[::1]:" + port:        200,
		"evil.example:" + port: 403, // DNS rebinding
		"127.0.0.1":            403, // wrong port
		"127.0.0.1:1":          403,
	} {
		req, _ := http.NewRequest("GET", ts.URL+"/", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("Host %q: status %d, want %d", host, resp.StatusCode, want)
		}
	}
}

func TestServerForwardsVeryLongRecordWhole(t *testing.T) {
	big := strings.Repeat("x", 200<<10)
	hub := NewHub(3, 2)
	hub.Write([]byte(`{"raw":"` + big + `"}` + "\n"))
	s := open(t, newTestServer(t, hub), "")
	if got, want := s.next(t), "id: 1\ndata: {\"raw\":\""+big+"\"}"; got != want {
		t.Fatalf("frame is %d bytes, want %d", len(got), len(want))
	}
}

func TestServerTwoStreamsAndDisconnect(t *testing.T) {
	hub := NewHub(10, 4)
	ts := newTestServer(t, hub)
	a, b := open(t, ts, ""), open(t, ts, "")
	waitSubs(t, hub, 2)
	hub.Write(rec(1))
	for _, s := range []*stream{a, b} {
		if got, want := s.next(t), "id: 1\ndata: {\"n\":1}"; got != want {
			t.Fatalf("frame %q, want %q", got, want)
		}
	}
	a.resp.Body.Close() // the tab goes away
	waitSubs(t, hub, 1) // the handler must notice and unsubscribe
}

func TestServerCloseEndsStreams(t *testing.T) {
	hub := NewHub(3, 2)
	s := open(t, newTestServer(t, hub), "")
	hub.Close()
	if _, err := s.r.ReadString('\n'); err == nil {
		t.Fatal("stream still open after Hub.Close")
	}
}

func TestPageNeverInjectsHTML(t *testing.T) {
	// Log lines are untrusted: the page may only use textContent.
	for _, bad := range []string{"innerHTML", "outerHTML", "insertAdjacentHTML", "document.write", "eval(", "new Function"} {
		if strings.Contains(string(indexHTML), bad) {
			t.Errorf("index.html uses %s", bad)
		}
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/web/ -race`
Expected: FAIL to build (`undefined: CheckAddr`, `Handler`, `indexHTML`, `pingEvery`).

- [ ] **Step 4: Write the implementation**

Create `internal/web/server.go`:

```go
package web

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

//go:embed index.html
var indexHTML []byte

var pingEvery = 15 * time.Second

// CheckAddr reports whether addr is a host:port whose host is loopback. Logs
// are served without authentication, so nothing else is allowed.
func CheckAddr(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid --web-addr %q: want host:port such as 127.0.0.1:8080", addr)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 0 || n > 65535 {
		return fmt.Errorf("invalid --web-addr %q: bad port", addr)
	}
	if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("--web-addr %q is not a loopback address (127.0.0.1, ::1 or localhost): logs are served without authentication", addr)
	}
	return nil
}

// Handler serves the page and the event stream for hub. port is the port the
// listener is bound to: a request for any other Host is refused, which blocks
// DNS rebinding. There are no write endpoints.
func Handler(hub *Hub, port string) http.Handler {
	ping := pingEvery
	allowed := map[string]bool{}
	for _, h := range []string{"127.0.0.1", "localhost", "::1"} {
		allowed[net.JoinHostPort(h, port)] = true
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		w.Write(indexHTML)
	})
	mux.HandleFunc("GET /events", func(w http.ResponseWriter, r *http.Request) { events(w, r, hub, ping) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func events(w http.ResponseWriter, r *http.Request, hub *Hub, ping time.Duration) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	after, err := strconv.ParseUint(r.Header.Get("Last-Event-ID"), 10, 64)
	if err != nil {
		after = 0 // empty or garbage: a fresh connection
	}
	sub := hub.Subscribe(after)
	defer sub.Close()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if sub.Gap {
		io.WriteString(w, "event: gap\ndata: {}\n\n")
	}
	for _, e := range sub.Replay {
		if writeEvent(w, e) != nil {
			return
		}
	}
	fl.Flush() // headers reach the browser even when there is nothing to replay
	tick := time.NewTicker(ping)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-sub.Live:
			if !ok {
				return // dropped as too slow, or the hub closed; the browser reconnects
			}
			if writeEvent(w, e) != nil {
				return
			}
			for more := true; more; { // batch whatever is already queued
				select {
				case e, ok := <-sub.Live:
					if !ok {
						return
					}
					if writeEvent(w, e) != nil {
						return
					}
				default:
					more = false
				}
			}
			fl.Flush()
		case <-tick.C:
			io.WriteString(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func writeEvent(w io.Writer, e Event) error {
	_, err := fmt.Fprintf(w, "id: %d\ndata: %s\n\n", e.Seq, e.Data)
	return err
}

// Server is a running listener serving one Hub.
type Server struct {
	hub *Hub
	ln  net.Listener
	srv *http.Server
}

// Start listens on addr and serves hub in the background. Validate addr with
// CheckAddr first.
func Start(addr string, hub *Hub) (*Server, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	s := &Server{hub: hub, ln: ln, srv: &http.Server{Handler: Handler(hub, port)}}
	go s.srv.Serve(ln)
	return s, nil
}

// URL is where a browser reaches the server.
func (s *Server) URL() string { return "http://" + s.ln.Addr().String() }

// Close ends the open event streams, then shuts the server down.
func (s *Server) Close() {
	s.hub.Close() // Shutdown would otherwise wait on streams that never end
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if s.srv.Shutdown(ctx) != nil {
		s.srv.Close()
	}
}
```

- [ ] **Step 5: Format, vet and run the tests**

Run: `gofmt -l internal/web && go vet ./internal/web/ && go test ./internal/web/ -race`
Expected: `gofmt -l` prints nothing (if it lists the test file, run `gofmt -w internal/web` and re-run), vet clean, tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/web/server.go internal/web/server_test.go internal/web/index.html
git commit -m "feat: add web server with SSE stream and host check" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Wire `--web` into `tail`, with docs

**Files:**
- Create: `cmd/klog/web.go`
- Modify: `cmd/klog/tail.go` (imports; flags near line 32; validation near line 36; writer swap at lines 98-100)
- Test: `cmd/klog/tail_web_test.go`
- Modify: `README.md`, `COMMANDS.md`

**Interfaces:**
- Consumes: `web.NewHub`, `web.RingSize`, `web.QueueSize`, `web.Start`, `Server.URL`, `Server.Close`, `web.CheckAddr` (Tasks 1-2); existing `flagSet(fs, name)`, `usageError`, `render.JSON`, `render.New`.
- Produces: `func checkWeb(fs *flag.FlagSet, on bool, addr, out string) error` and `func startWeb(addr string, stderr io.Writer) (*web.Hub, func(), error)` in `cmd/klog/web.go`. Task 4 does not call them.

- [ ] **Step 1: Write the failing tests**

Create `cmd/klog/tail_web_test.go`:

```go
package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

var webBase = []string{"-n", "shop", "-l", "app=web", "--poll", "50ms", "--web"}

// webURL waits for the "web UI at" line on stderr and returns the URL.
func webURL(t *testing.T, errs *safeBuf) string {
	t.Helper()
	const marker = "web UI at "
	var url string
	waitFor(t, "web UI URL", func() bool {
		s := errs.String()
		i := strings.Index(s, marker)
		if i < 0 {
			return false
		}
		rest := s[i+len(marker):]
		j := strings.IndexByte(rest, '\n')
		if j < 0 {
			return false
		}
		url = rest[:j]
		return true
	})
	return url
}

// sseData reads /events until every want substring has appeared in the data
// lines, and returns all data lines read.
func sseData(t *testing.T, url string, want ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body) // a Reader: records can be long
	var got strings.Builder
	for {
		line, err := r.ReadString('\n')
		if strings.HasPrefix(line, "data: ") {
			got.WriteString(line)
		}
		all := true
		for _, w := range want {
			all = all && strings.Contains(got.String(), w)
		}
		if all {
			return got.String()
		}
		if err != nil {
			t.Fatalf("stream ended before %q: %v\ngot: %s", want, err, got.String())
		}
	}
}

func TestTailWebServesFilteredRecordsAndKeepsStdoutEmpty(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	out, errs, stop := startTail(t, append(append([]string{}, webBase...), "--level", "ERROR")...)
	url := webURL(t, errs)
	got := sseData(t, url, "a-three")
	for _, dropped := range []string{"a-one", "b-two", "b-four"} {
		if strings.Contains(got, dropped) {
			t.Errorf("record %q passed --level ERROR: %s", dropped, got)
		}
	}
	if !strings.Contains(got, `"raw":`) {
		t.Errorf("not a JSON record: %s", got)
	}
	if s := out.String(); s != "" {
		t.Errorf("stdout must stay empty with --web, got %q", s)
	}
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

func TestTailWebSurfacesDedupeRepeats(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", a1+" same\n"+a1+" same\n"+a1+" same\n"+a3+" other\n")
	f.Hang("a-1")
	f.Hang("b-1")
	_, errs, _ := startTail(t, append(append([]string{}, webBase...), "--dedupe")...)
	sseData(t, webURL(t, errs), `"repeats":2`)
}

func TestTailWebCtrlCWithOpenStreamExitsCleanly(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	_, errs, stop := startTail(t, webBase...)
	url := webURL(t, errs)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/events", nil)
	resp, err := http.DefaultClient.Do(req) // a browser tab that stays connected
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("tail took %v to exit with an open stream", d)
	}
}

func TestTailWebPortInUseExits1(t *testing.T) {
	setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	out, errs := &safeBuf{}, &safeBuf{}
	code := execute(context.Background(), append(append([]string{"tail"}, webBase...), "--web-addr", ln.Addr().String()), out, errs)
	if code != 1 || !strings.Contains(errs.String(), "klog: --web:") || strings.Contains(errs.String(), "web UI at") {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
}

func TestTailWebZeroPodsExits3WithoutURL(t *testing.T) {
	f := setup(t)
	f.SetGet("pods", `{"items":[]}`)
	out, errs := &safeBuf{}, &safeBuf{}
	code := execute(context.Background(), append([]string{"tail"}, webBase...), out, errs)
	if code != 3 || strings.Contains(errs.String(), "web UI at") {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
}

func TestTailWebUsageErrorsNeverCallKubectl(t *testing.T) {
	f := setup(t)
	for name, args := range map[string][]string{
		"web-addr without web": {"-l", "a=b", "--web-addr", "127.0.0.1:0"},
		"non-loopback address": {"-l", "a=b", "--web", "--web-addr", "0.0.0.0:0"},
		"malformed address":    {"-l", "a=b", "--web", "--web-addr", "nope"},
		"with out":             {"-l", "a=b", "--web", "--out", "x.log"},
		"with format":          {"-l", "a=b", "--web", "--format", "json"},
		"with template":        {"-l", "a=b", "--web", "--template", "{{.Raw}}"},
	} {
		t.Run(name, func(t *testing.T) {
			out, errs := &safeBuf{}, &safeBuf{}
			if code := execute(context.Background(), append([]string{"tail"}, args...), out, errs); code != 2 {
				t.Fatalf("code %d, stderr %s", code, errs.String())
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}
```

Note: `a1`, `a3` and `setup` come from `fetch_test.go` (same package); `safeBuf`, `waitFor` and `startTail` come from `tail_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/klog/ -race -run 'TestTailWeb'`
Expected: FAIL: `flag provided but not defined: -web` (exit 2 where the test expects 0/1/3) or timeouts waiting for the URL.

- [ ] **Step 3: Write `cmd/klog/web.go`**

```go
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"klog/internal/web"
)

// checkWeb validates the --web flags. It runs before any kubectl call.
func checkWeb(fs *flag.FlagSet, on bool, addr, out string) error {
	if !on {
		if addr != "" {
			return errors.New("--web-addr needs --web")
		}
		return nil
	}
	if out != "" {
		return errors.New("--web cannot be combined with --out")
	}
	if flagSet(fs, "format") || flagSet(fs, "template") {
		return errors.New("--web cannot be combined with --format or --template (the browser always gets JSON records)")
	}
	if addr == "" {
		return nil
	}
	return web.CheckAddr(addr)
}

// startWeb serves a new hub on addr (a free loopback port when empty) and
// prints its URL. stop shuts the server down.
func startWeb(addr string, stderr io.Writer) (hub *web.Hub, stop func(), err error) {
	if addr == "" {
		addr = "127.0.0.1:0"
	}
	hub = web.NewHub(web.RingSize, web.QueueSize)
	srv, err := web.Start(addr, hub)
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(stderr, "klog: web UI at %s\n", srv.URL())
	return hub, srv.Close, nil
}
```

- [ ] **Step 4: Modify `cmd/klog/tail.go`**

`tail.go` needs no new import: it only calls `startWeb` from `web.go`.

Add the two flags after the `outPath` flag line:

```go
	webOn := fs.Bool("web", false, "serve the logs in a browser (search, filter by pod) on a loopback address; stdout stays empty")
	webAddr := fs.String("web-addr", "", "with --web: listen address, must be loopback (default 127.0.0.1:0, a free port)")
```

Add the validation right after the `*stats` check (before `cf.build()`):

```go
	if err := checkWeb(fs, *webOn, *webAddr, *outPath); err != nil {
		return usageError(stderr, err)
	}
```

Replace

```go
	view := c.view
	view.Color = *outPath == "" && useColor(stdout)
	renderer := render.New(w, view)
```

with

```go
	view := c.view
	view.Color = *outPath == "" && useColor(stdout)
	if *webOn {
		// The hub is the writer, so each rendered JSON record reaches the browser whole.
		hub, stopWeb, err := startWeb(*webAddr, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "klog: --web: %v\n", err)
			return 1
		}
		defer stopWeb()
		w = hub
		view.Format = render.JSON
	}
	renderer := render.New(w, view)
```

`stopWeb` is deferred, so it runs after the loop, `wg.Wait()` and the final drain, and before `cancel` (LIFO).

- [ ] **Step 5: Run the web tests, then the whole suite**

Run: `gofmt -l cmd internal && go vet ./... && go test ./cmd/klog/ -race -run 'TestTailWeb' -v`
Expected: all `TestTailWeb*` PASS.

Run: `go test ./... -race`
Expected: PASS (existing tail and fetch tests unchanged).

- [ ] **Step 6: Update `README.md`**

Insert this paragraph immediately before the line starting `Save flags you use often as a profile in`:

```markdown
Browser view (`tail` only): add `--web` to read and search the lines in a browser instead of the terminal. klog serves a page on a free loopback port and prints its URL; stdout stays empty. `--web-addr 127.0.0.1:8080` picks the address (loopback only, no auth). It cannot be combined with `--out`, `--format` or `--template`.

```
klog tail -n shop -d checkout --level WARN --web
klog: web UI at http://127.0.0.1:41873
```

The page searches the lines it has already received (substring, or regex and case toggles), hides non-matching lines, keeps a stack trace with its parent line, and can hide pods by clicking their chip. Click a line to see its JSON.
```

Add this bullet to the `## Limits` list in `README.md`:

```markdown
- `--web` search covers the last 20,000 lines klog kept and at most 20,000 rows in the tab. For more history, restart with a larger `--since`.
```

- [ ] **Step 7: Update `COMMANDS.md`**

In the `klog tail` flag table, add these two rows directly after the `--wait` row:

```markdown
| `--web` | bool | false | Serve the logs in a browser on a loopback address and print the URL to stderr; stdout stays empty. The page searches the lines it has received (substring, regex, case), hides non-matching lines, keeps stack traces with their parent line and can hide pods. Cannot be combined with `--out`, `--format` or `--template` |
| `--web-addr` | string | `127.0.0.1:0` | With `--web`: listen address, loopback only (`127.0.0.1`, `::1` or `localhost`); port `0` picks a free port. A usage error without `--web` |
```

Add this line to the `klog tail` example block, after the `--out warnings.log` example:

```bash
klog tail -n shop -d checkout --level WARN --web
```

- [ ] **Step 8: Commit**

```bash
git add cmd/klog/web.go cmd/klog/tail.go cmd/klog/tail_web_test.go README.md COMMANDS.md
git commit -m "feat: add --web flag to tail" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

### Task 4: The browser page

**Files:**
- Modify (replace entirely): `internal/web/index.html`

**Interfaces:**
- Consumes: the SSE contract from Task 2: `GET events` (relative URL), default `message` events whose `data` is the JSON record `{source, time?, raw, json?, repeats?}` (the format `render.Renderer` writes in JSON mode), and `event: gap`.
- Produces: the final page. `TestPageNeverInjectsHTML` (Task 2) keeps guarding it.

- [ ] **Step 1: Replace `internal/web/index.html` with the full page**

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>klog</title>
<style>
:root { color-scheme: light dark; }
body { margin: 0; height: 100vh; display: flex; flex-direction: column; font: 13px/1.4 ui-monospace, Menlo, Consolas, monospace; }
header { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; padding: 6px 8px; border-bottom: 1px solid #8884; }
#q { flex: 1; min-width: 220px; font: inherit; padding: 2px 6px; }
#q.bad { outline: 2px solid #d33; }
#status { margin-left: auto; opacity: .7; }
#chips { display: flex; flex-wrap: wrap; gap: 4px; padding: 4px 8px; border-bottom: 1px solid #8884; }
#chips:empty { display: none; }
.chip { border: 1px solid; border-radius: 10px; padding: 0 8px; cursor: pointer; user-select: none; }
.chip.off { opacity: .4; text-decoration: line-through; }
#log { flex: 1; overflow: auto; }
.row.hide { display: none; }
.line { display: flex; gap: 8px; padding: 0 8px; cursor: pointer; }
.line:hover { background: #8882; }
.src { flex: none; font-weight: bold; }
.ts { flex: none; opacity: .6; }
.body { white-space: pre-wrap; word-break: break-all; }
.rep { opacity: .6; }
.lvl-trace, .lvl-debug { opacity: .6; }
.lvl-warn { color: #b80; }
.lvl-error, .lvl-fatal { color: #d33; }
.detail { margin: 0 8px 4px 24px; padding: 4px 6px; background: #8882; white-space: pre-wrap; word-break: break-all; }
</style>
</head>
<body>
<header>
  <input id="q" type="search" placeholder="search" autofocus>
  <label><input id="re" type="checkbox"> regex</label>
  <label><input id="cs" type="checkbox"> case</label>
  <button id="pause">pause</button>
  <button id="clear">clear</button>
  <span id="count"></span>
  <span id="status">connecting…</span>
</header>
<div id="chips"></div>
<div id="log"></div>
<script>
"use strict";
// Log lines are untrusted: every piece of log text goes in through textContent.
const MAX_ROWS = 20000;
// Same rule as filter.IsContinuation on the server: a stack-trace line.
const CONT = /^(\s|Caused by:|Suppressed:|\.\.\. \d+ (more|common frames omitted))/;
const NUMERIC_LEVELS = { 10: "trace", 20: "debug", 30: "info", 40: "warn", 50: "error", 60: "fatal" };
const LEVEL_ALIASES = { warning: "warn", err: "error", critical: "error", panic: "fatal" };
const KNOWN_LEVELS = new Set(["trace", "debug", "info", "warn", "error", "fatal"]);

const $ = id => document.getElementById(id);
const logEl = $("log"), q = $("q"), reBox = $("re"), csBox = $("cs");
const rows = [];          // oldest first: {el, src, raw, cont, group, shown}
const lastBySrc = new Map(); // newest row of each source: a stack-trace line joins its group
const off = new Set();    // sources hidden by their chip
const chips = new Map();  // source -> chip element
let test = () => true;    // current search predicate over a row's raw text
let vis = 0;              // rows currently shown
let follow = true;        // keep the view pinned to the newest row
let state = "connecting…", skipped = false;

function paintStatus() {
  $("status").textContent = state + (skipped ? " · some lines were skipped" : "");
}
function setFollow(v) {
  follow = v;
  $("pause").textContent = v ? "pause" : "resume";
}
function atBottom() {
  return logEl.scrollHeight - logEl.scrollTop - logEl.clientHeight < 4;
}
logEl.addEventListener("scroll", () => setFollow(atBottom()));
$("pause").onclick = () => {
  if (follow) { setFollow(false); return; }
  logEl.scrollTop = logEl.scrollHeight;
  setFollow(true);
};

let queued = false;
function schedule() { // one count and scroll update per frame, however many rows arrived
  if (queued) return;
  queued = true;
  requestAnimationFrame(() => {
    queued = false;
    updateCount();
    if (follow) logEl.scrollTop = logEl.scrollHeight;
  });
}
function updateCount() { $("count").textContent = vis + " / " + rows.length; }

function colorFor(s) {
  let h = 0;
  for (const c of s) h = (h * 31 + c.codePointAt(0)) >>> 0;
  return "hsl(" + (h % 360) + ",60%,55%)";
}

function level(j) {
  if (!j) return "";
  const keys = Object.keys(j);
  for (const want of ["level", "severity", "lvl"]) { // first present key wins, like the server
    const k = keys.find(k => k.toLowerCase() === want);
    if (k === undefined) continue;
    const v = j[k];
    let s = typeof v === "number" ? (NUMERIC_LEVELS[v] || "") : String(v).toLowerCase();
    s = LEVEL_ALIASES[s] || s;
    return KNOWN_LEVELS.has(s) ? s : "";
  }
  return "";
}

function timeText(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (isNaN(d)) return "";
  return d.toLocaleTimeString("en-GB") + "." + String(d.getMilliseconds()).padStart(3, "0");
}

function detailText(rec) {
  const meta = { source: rec.source };
  if (rec.time) meta.time = rec.time;
  if (rec.repeats) meta.repeats = rec.repeats;
  let s = JSON.stringify(meta) + "\n" + (rec.json ? JSON.stringify(rec.json, null, 2) : rec.raw);
  if (rec.json && /\d{16,}/.test(rec.raw)) {
    s += "\n(numbers over 2^53 are rounded here; the line above keeps the original)";
  }
  return s;
}

// A record is a line plus the stack-trace lines after it; it is shown when any of its lines matches.
function setShown(r) {
  const s = r.group.hit && !off.has(r.src);
  if (s === r.shown) return;
  r.shown = s;
  r.el.classList.toggle("hide", !s);
  vis += s ? 1 : -1;
}

function noteSource(src) {
  if (chips.has(src)) return;
  const c = document.createElement("span");
  c.className = "chip";
  c.textContent = src;
  c.style.borderColor = colorFor(src);
  c.onclick = () => {
    if (off.has(src)) off.delete(src); else off.add(src);
    c.classList.toggle("off", off.has(src));
    refilter();
  };
  chips.set(src, c);
  $("chips").appendChild(c);
}

function addRow(rec) {
  const src = rec.source, raw = rec.raw;
  noteSource(src);
  const cont = !rec.json && CONT.test(raw);
  const prev = lastBySrc.get(src);
  const group = cont && prev ? prev.group : { hit: false, rows: [] }; // a trace never splits from its parent
  const wasHit = group.hit;
  group.hit = group.hit || test(raw);

  const el = document.createElement("div");
  el.className = "row hide"; // setShown reveals it below
  const line = document.createElement("div");
  const lv = level(rec.json);
  line.className = "line" + (lv ? " lvl-" + lv : "");
  const s = document.createElement("span");
  s.className = "src";
  s.textContent = "[" + src + "]";
  s.style.color = colorFor(src);
  const t = document.createElement("span");
  t.className = "ts";
  t.textContent = timeText(rec.time);
  const b = document.createElement("span");
  b.className = "body";
  b.textContent = raw;
  line.append(s, t, b);
  if (rec.repeats) {
    const r = document.createElement("span");
    r.className = "rep";
    r.textContent = "… repeated " + rec.repeats + " more time" + (rec.repeats === 1 ? "" : "s");
    line.append(r);
  }
  line.onclick = () => {
    if (String(getSelection())) return; // the user is selecting text, not asking for detail
    const open = el.querySelector(".detail");
    if (open) { open.remove(); return; }
    const d = document.createElement("pre");
    d.className = "detail";
    d.textContent = detailText(rec);
    el.append(d);
  };
  el.append(line);

  const row = { el, src, raw, cont, group, shown: false };
  rows.push(row);
  group.rows.push(row);
  lastBySrc.set(src, row);
  logEl.append(el);
  if (group.hit !== wasHit) group.rows.forEach(setShown); // a later trace line can reveal its whole record
  else setShown(row);
  while (rows.length > MAX_ROWS) {
    const old = rows.shift();
    old.group.rows.shift(); // the oldest row is the first of its group
    if (old.shown) vis--;
    if (lastBySrc.get(old.src) === old) lastBySrc.delete(old.src);
    old.el.remove();
  }
  schedule();
}

function buildTest() { // null when the regex does not compile
  const text = q.value;
  if (text === "") return () => true;
  if (reBox.checked) {
    try {
      const rx = new RegExp(text, csBox.checked ? "" : "i");
      return s => rx.test(s);
    } catch (e) {
      return null;
    }
  }
  if (csBox.checked) return s => s.includes(text);
  const low = text.toLowerCase();
  return s => s.toLowerCase().includes(low);
}

function refilter() {
  const t = buildTest();
  q.classList.toggle("bad", t === null); // keep the previous results while the regex is invalid
  if (t !== null) test = t;
  const seen = new Set();
  for (const r of rows) {
    if (!seen.has(r.group)) { seen.add(r.group); r.group.hit = false; }
    r.group.hit = r.group.hit || test(r.raw);
  }
  vis = 0;
  for (const r of rows) {
    r.shown = r.group.hit && !off.has(r.src);
    r.el.classList.toggle("hide", !r.shown);
    if (r.shown) vis++;
  }
  updateCount();
  if (follow) logEl.scrollTop = logEl.scrollHeight;
}

q.oninput = refilter;
reBox.onchange = refilter;
csBox.onchange = refilter;
$("clear").onclick = () => {
  logEl.replaceChildren();
  rows.length = 0;
  lastBySrc.clear();
  vis = 0;
  updateCount();
};

const es = new EventSource("events"); // reconnects by itself and resumes from Last-Event-ID
es.onopen = () => { state = "connected"; paintStatus(); };
es.onerror = () => { state = "reconnecting…"; paintStatus(); };
es.onmessage = ev => {
  let rec;
  try { rec = JSON.parse(ev.data); } catch (e) { return; }
  addRow(rec);
};
es.addEventListener("gap", () => { skipped = true; paintStatus(); });
updateCount();
</script>
</body>
</html>
```

- [ ] **Step 2: Run the Go tests (the page guard and the page-served check)**

Run: `gofmt -l internal cmd && go vet ./... && go test ./... -race`
Expected: PASS, including `TestPageNeverInjectsHTML` and `TestServerServesPage` (the page is embedded by byte comparison).

- [ ] **Step 3: Manual check against a fake kubectl**

The page has no unit tests; check it by hand. Create a throwaway fake kubectl and demo logs:

```bash
mkdir -p /tmp/klog-demo && cd /tmp/klog-demo
cat > pods.json <<'EOF'
{"items":[
 {"metadata":{"name":"web-1"},"spec":{"containers":[{"name":"app"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","restartCount":0}]}},
 {"metadata":{"name":"web-2"},"spec":{"containers":[{"name":"app"}]},"status":{"phase":"Running","containerStatuses":[{"name":"app","restartCount":0}]}}]}
EOF
cat > logs-web-1.txt <<'EOF'
2026-10-01T12:00:01.000000000Z {"level":"INFO","msg":"started","port":8080}
2026-10-01T12:00:02.000000000Z {"level":"ERROR","msg":"db timeout","orderId":12345678901234567890}
2026-10-01T12:00:02.100000000Z java.io.IOException: boom
2026-10-01T12:00:02.200000000Z 	at com.acme.Db.query(Db.java:42)
2026-10-01T12:00:02.300000000Z Caused by: java.net.SocketTimeoutException
2026-10-01T12:00:02.400000000Z 	... 12 more
2026-10-01T12:00:03.000000000Z {"level":"WARN","msg":"<img src=x onerror=alert(1)> <script>alert(2)</script>"}
EOF
cat > logs-web-2.txt <<'EOF'
2026-10-01T12:00:01.500000000Z {"level":"INFO","msg":"healthy"}
2026-10-01T12:00:04.000000000Z plain text line from web-2
EOF
cat > kubectl <<'EOF'
#!/bin/sh
D=/tmp/klog-demo
case "$1" in
get) cat "$D/pods.json"; exit 0;;
logs)
  shift
  pod=
  while [ $# -gt 0 ]; do
    case "$1" in
      -n) shift 2;;
      -c) shift 2;;
      -*) shift;;
      *) pod="$1"; shift;;
    esac
  done
  cat "$D/logs-$pod.txt"
  exec sleep 3600;;
esac
exit 1
EOF
chmod +x kubectl
cd - >/dev/null
KLOG_KUBECTL=/tmp/klog-demo/kubectl go run ./cmd/klog tail -n shop -l app=web --web
```

Open the printed URL in a browser and confirm each of these:

- [ ] Lines from `web-1` and `web-2` appear, level colours show (ERROR red, WARN amber), the status reads `connected`, the count reads `N / N`.
- [ ] The line containing `<img src=x onerror=alert(1)>` shows as plain text and no alert fires.
- [ ] Search `timeout`: the `db timeout` line and the whole `java.io.IOException: boom` record (that line, `at com.acme...`, `Caused by: ...SocketTimeoutException`, `... 12 more`) stay, because the `Caused by` line matches; every other line hides.
- [ ] Search `Db.query` (matches only the `at` line): the `IOException: boom` line and all its trace lines show, not the `at` line alone. Search `boom`: the same four lines show.
- [ ] Tick `regex` and type `(`: the box turns red and the previous results stay. Type `db|healthy`: both lines show.
- [ ] Tick `case` and search `DB`: nothing matches; untick: matches.
- [ ] Click the `web-1` chip: its lines hide and the chip strikes through; click again: they return.
- [ ] Click a line: its JSON expands (the `orderId` row shows the rounding note); click again: it collapses. Selecting text with the mouse does not toggle it.
- [ ] Reload the page: all lines reappear (replayed from the hub ring).
- [ ] `pause` freezes the scroll position; `resume` jumps to the end. Scrolling up by hand flips the button to `resume`.
- [ ] `clear` empties the list and chips stay.
- [ ] Stop klog with Ctrl-C: the terminal returns promptly with exit 0, and the page status flips to `reconnecting…`.

- [ ] **Step 4: Clean up and commit**

```bash
rm -rf /tmp/klog-demo
git add internal/web/index.html
git commit -m "feat: add browser log page with client-side search" -m "Co-Authored-By: Claude Sonnet 5.5 <noreply@anthropic.com>"
```

---

## Self-Review Notes

- **Spec coverage:** flags and validation (Task 3), URL printed after resolve and no URL on exit 1/3 (Task 3 tests), Hub ring/queue/drop/replay/gap/ids (Task 1), SSE frames, `gap` event, ping, Host check, CSP, shutdown (Task 2), page features (search, regex/case, hide, trace inheritance, level colour, chips, row detail, pause/clear, 20,000-row cap, status, local time) (Task 4), dedupe `repeats` (Tasks 3 and 4), docs and limits (Task 3). Non-goals are untouched.
- **Interfaces:** `NewHub(ring, queue)`, `Subscribe(after) *Sub` with `Replay`/`Gap`/`Live`, `Sub.Close`, `Hub.Close`, `Handler(hub, port)`, `Start(addr, hub)`, `Server.URL/Close`, `CheckAddr`, `checkWeb`, `startWeb` are named identically wherever they are used.
- **Known ceilings (deliberate):** a browser that needs more than the 256-record queue during a very busy replay can be dropped repeatedly; it reconnects and resumes, so it converges once replay outpaces new lines. The ring and row caps are constants.
