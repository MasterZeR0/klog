package main

import (
	"errors"
	"flag"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"

	"klog/internal/filter"
	"klog/internal/parse"
)

// incidentFlags are the tail and fetch flags for digging into an incident:
// grep-style context and trace following.
type incidentFlags struct {
	after, before, around optInt
	followID              string
}

// optInt is an int flag that remembers whether it was given, so an explicit
// -A 0 can override -C.
type optInt struct {
	v   int
	set bool
}

func (o *optInt) String() string { return strconv.Itoa(o.v) }
func (o *optInt) Set(s string) error {
	v, err := strconv.Atoi(s)
	if err != nil {
		return errors.New("parse error")
	}
	o.v, o.set = v, true
	return nil
}

// maxBuffered caps the lines fetch --follow-id (or -A/-B/-C with it) holds in
// memory across all streams; a var so tests can lower it.
var maxBuffered int64 = 1_000_000

func addIncident(fs *flag.FlagSet) *incidentFlags {
	i := &incidentFlags{}
	fs.Var(&i.after, "A", "lines of context after each --grep match")
	fs.Var(&i.after, "after", "same as -A")
	fs.Var(&i.before, "B", "lines of context before each --grep match")
	fs.Var(&i.before, "before", "same as -B")
	fs.Var(&i.around, "C", "lines of context before and after each --grep match; -A/-B override it")
	fs.StringVar(&i.followID, "follow-id", "", "JSON field: also show every line, from any pod, sharing its value with a line that passes the filters")
	return i
}

// incident is the validated result of incidentFlags.
type incident struct {
	before, after int
	ids           *filter.IDSet // nil = no --follow-id
	buffered      *atomic.Int64 // lines held by all stages of one fetch
}

func (i *incidentFlags) build(c common) (incident, error) {
	if i.after.v < 0 || i.before.v < 0 || i.around.v < 0 {
		return incident{}, errors.New("-A, -B and -C must not be negative")
	}
	// grep semantics: -C sets both sides, an explicit -A or -B wins for its side.
	in := incident{before: i.around.v, after: i.around.v, buffered: new(atomic.Int64)}
	if i.before.set {
		in.before = i.before.v
	}
	if i.after.set {
		in.after = i.after.v
	}
	if (in.before > 0 || in.after > 0) && c.filter.Grep == nil {
		return incident{}, errors.New("-A, -B and -C need --grep")
	}
	if i.followID != "" {
		in.ids = filter.NewIDSet(i.followID)
	}
	return in, nil
}

// stage is the per-stream step after parsing: filter, then trace following,
// then context. Feed returns the lines to emit now. With a barrier (fetch
// --follow-id) Feed only buffers and Flush, called once per stream after the
// stream ends, waits for every stream and then emits: pass one collects the
// IDs from all pods, pass two replays with the full set.
type stage struct {
	f       *filter.Filter
	ids     *filter.IDSet
	ctx     *filter.Context // nil = no context
	barrier *sync.WaitGroup
	buf     []tagged
	n       *atomic.Int64 // shared line count, see incident.buffered
	tripped bool          // hit maxBuffered: stop buffering, Flush emits nothing
	cur     tagged        // the line being replayed
	replay  bool
}

type tagged struct {
	l     parse.Line
	match bool // passed the filters on its own
}

// newStage makes a stage for one stream. barrier is non-nil only for fetch
// with --follow-id, and must have been Add()ed once per stream.
func (in incident) newStage(cfg filter.Config, barrier *sync.WaitGroup) *stage {
	s := &stage{f: filter.New(cfg), ids: in.ids, barrier: barrier, n: in.buffered}
	if in.before > 0 || in.after > 0 {
		s.ctx = filter.NewContext(s, in.before, in.after)
	}
	return s
}

// Keep implements filter.Keeper: a match is a line that passes the filters
// (a seed, whose ID is recorded) or carries a followed ID.
func (s *stage) Keep(l parse.Line) bool {
	if s.replay {
		return s.cur.match || s.ids.Has(l)
	}
	m := s.f.Keep(l)
	if s.ids == nil {
		return m
	}
	if m {
		s.ids.Add(l)
		return true
	}
	return s.ids.Has(l)
}

func (s *stage) Feed(l parse.Line) []parse.Line {
	if s.barrier != nil {
		m := s.f.Keep(l)
		if m {
			s.ids.Add(l)
		}
		// Only lines that matched or carry an ID can ever be shown; with
		// context every line is a candidate.
		if s.tripped {
			return nil
		}
		if m || s.ctx != nil || s.ids.HasField(l) {
			if s.n.Add(1) > maxBuffered {
				s.tripped, s.buf = true, nil
				return nil
			}
			s.buf = append(s.buf, tagged{l, m})
		}
		return nil
	}
	return s.emit(l)
}

func (s *stage) emit(l parse.Line) []parse.Line {
	if s.ctx != nil {
		return s.ctx.Feed(l)
	}
	if s.Keep(l) {
		return []parse.Line{l}
	}
	return nil
}

// Flush returns what Feed held back. Call it exactly once per stream.
func (s *stage) Flush() []parse.Line {
	if s.barrier == nil {
		return nil
	}
	s.barrier.Done()
	s.barrier.Wait()
	s.replay = true
	if s.n.Load() > maxBuffered {
		s.buf = nil // some stream tripped the limit: fetch fails, don't replay
	}
	var out []parse.Line
	for _, t := range s.buf {
		s.cur = t
		out = append(out, s.emit(t.l)...)
	}
	s.buf = nil
	return out
}

// overflow reports whether the buffering limit was hit. Call it after every
// stream has flushed; a non-nil result means fetch must fail with exit 1.
func (in incident) overflow() error {
	if in.buffered.Load() > maxBuffered {
		return fmt.Errorf("more than %d lines buffered for --follow-id; narrow the window with a shorter --since or add --grep", maxBuffered)
	}
	return nil
}
