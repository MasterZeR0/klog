package web

import (
	"bytes"
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

func TestHubRebaseMakesOldIDsAGap(t *testing.T) {
	h := NewHub(5, 4)
	h.Rebase(1000)
	for i := 1; i <= 3; i++ {
		h.Write(rec(i))
	}
	for _, tc := range []struct {
		name  string
		after uint64
		gap   bool
	}{
		{"id from an earlier run", 5, true},
		{"fresh connection", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := h.Subscribe(tc.after)
			defer s.Close()
			if got, want := seqs(s.Replay), []uint64{1000, 1001, 1002}; !reflect.DeepEqual(got, want) {
				t.Fatalf("replay %v, want %v", got, want)
			}
			if s.Gap != tc.gap {
				t.Fatalf("gap %v, want %v", s.Gap, tc.gap)
			}
		})
	}
}

// heldBytes is a test hook: the sum of the records the ring holds.
func (h *Hub) heldBytes() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	sum := 0
	for i := 0; i < h.n; i++ {
		sum += len(h.buf[(h.start+i)%len(h.buf)].Data)
	}
	return sum
}

func TestHubByteBudgetEvictsOldest(t *testing.T) {
	h := NewHub(100, 4)
	h.budget = 100
	line := append(bytes.Repeat([]byte("x"), 39), '\n') // 39 bytes of data
	for i := 0; i < 5; i++ {
		h.Write(line)
	}
	s := h.Subscribe(1)
	defer s.Close()
	if got, want := seqs(s.Replay), []uint64{4, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replay %v, want %v", got, want)
	}
	if !s.Gap {
		t.Fatal("asking for evicted records must report a gap")
	}
	if h.bytes != h.heldBytes() || h.bytes > 100 {
		t.Fatalf("bytes %d, held %d", h.bytes, h.heldBytes())
	}
}

func TestHubKeepsOneRecordBiggerThanBudget(t *testing.T) {
	h := NewHub(10, 4)
	h.budget = 10
	h.Write(append(bytes.Repeat([]byte("x"), 500), '\n'))
	s := h.Subscribe(0)
	defer s.Close()
	if len(s.Replay) != 1 || len(s.Replay[0].Data) != 500 {
		t.Fatalf("replay %d records, want the one oversized record", len(s.Replay))
	}
}

func TestHubByteSumSurvivesCountEviction(t *testing.T) {
	h := NewHub(4, 4)
	for i := 1; i <= 11; i++ {
		h.Write(rec(i * 1000)) // varying lengths, ring overwrites
	}
	if h.bytes != h.heldBytes() {
		t.Fatalf("bytes %d, held %d", h.bytes, h.heldBytes())
	}
}
