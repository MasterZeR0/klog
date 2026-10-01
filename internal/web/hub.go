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

// Rebase sets the sequence number of the next record. Call it before the first
// Write only: it exists so a restarted process issues ids above every id an
// earlier run gave a browser, which then sees a gap instead of losing records.
func (h *Hub) Rebase(base uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next = base
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
