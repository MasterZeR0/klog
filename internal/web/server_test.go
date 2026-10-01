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
		"127.0.0.1:0":     true,
		"127.0.0.1:8080":  true,
		"localhost:8080":  true,
		"[::1]:9000":      true,
		"0.0.0.0:0":       false,
		":8080":           false, // empty host means every interface
		"192.168.1.5:80":  false,
		"example.com:80":  false,
		"8080":            false,
		"127.0.0.1":       false,
		"127.0.0.1:99999": false,
		"127.0.0.1:x":     false,
		"127.0.0.1:-1":    false,
		"":                false,
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

func TestServerOldRunIDGetsGapThenEverything(t *testing.T) {
	hub := NewHub(10, 4)
	hub.Rebase(1000)
	hub.Write(rec(1))
	s := open(t, newTestServer(t, hub), "5") // the browser's id from before a restart
	if got, want := s.next(t), "event: gap\ndata: {}"; got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
	if got, want := s.next(t), "id: 1000\ndata: {\"n\":1}"; got != want {
		t.Fatalf("frame %q, want %q", got, want)
	}
}

func TestHandlerAllowsExtraHost(t *testing.T) {
	ts := httptest.NewUnstartedServer(nil)
	_, port, _ := net.SplitHostPort(ts.Listener.Addr().String())
	ts.Config.Handler = Handler(NewHub(3, 2), port, "127.0.0.2:"+port)
	ts.Start()
	defer ts.Close()
	for host, want := range map[string]int{
		"127.0.0.2:" + port: 200,
		"127.0.0.3:" + port: 403,
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

func TestStartServesOnItsBoundLoopbackAddress(t *testing.T) {
	hub := NewHub(3, 2)
	s, err := Start("127.0.0.2:0", hub)
	if err != nil {
		t.Skipf("127.0.0.2 unavailable: %v", err)
	}
	defer s.Close()
	for host, want := range map[string]int{
		s.ln.Addr().String(): 200,
		"evil.example:80":    403,
	} {
		req, _ := http.NewRequest("GET", s.URL()+"/", nil)
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

type fakeListener struct {
	net.Listener
	addr net.Addr
	shut bool
}

func (f *fakeListener) Addr() net.Addr { return f.addr }
func (f *fakeListener) Close() error   { f.shut = true; return nil }

func TestRequireLoopback(t *testing.T) {
	bad := &fakeListener{addr: &net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 80}}
	if err := requireLoopback(bad); err == nil || !strings.Contains(err.Error(), "non-loopback") || !bad.shut {
		t.Fatalf("err %v, closed %v: want an error and a closed listener", err, bad.shut)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if err := requireLoopback(ln); err != nil {
		t.Fatalf("loopback listener refused: %v", err)
	}
}
