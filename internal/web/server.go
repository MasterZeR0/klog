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
// DNS rebinding. extra lists further allowed Host values, such as the bound
// address when it is a loopback IP other than 127.0.0.1. There are no write
// endpoints.
func Handler(hub *Hub, port string, extra ...string) http.Handler {
	ping := pingEvery
	allowed := map[string]bool{}
	for _, h := range []string{"127.0.0.1", "localhost", "::1"} {
		allowed[net.JoinHostPort(h, port)] = true
	}
	for _, h := range extra {
		allowed[h] = true
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
	if err := requireLoopback(ln); err != nil {
		return nil, err
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	s := &Server{hub: hub, ln: ln, srv: &http.Server{Handler: Handler(hub, port, ln.Addr().String())}}
	go s.srv.Serve(ln)
	return s, nil
}

// requireLoopback closes ln and fails when it is not bound to a loopback IP. A
// name such as localhost is resolved by the system and might not be loopback.
func requireLoopback(ln net.Listener) error {
	if a, ok := ln.Addr().(*net.TCPAddr); !ok || !a.IP.IsLoopback() {
		ln.Close()
		return fmt.Errorf("listener bound to non-loopback address %s", ln.Addr())
	}
	return nil
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
