package main

import (
	"errors"
	"flag"
	"sync"

	"klog/internal/filter"
	"klog/internal/parse"
)

// incidentFlags are the tail and fetch flags for digging into an incident:
// grep-style context and trace following.
type incidentFlags struct {
	after, before, around int
	followID              string
}

func addIncident(fs *flag.FlagSet) *incidentFlags {
	i := &incidentFlags{}
	fs.IntVar(&i.after, "A", 0, "lines of context after each --grep match")
	fs.IntVar(&i.after, "after", 0, "same as -A")
	fs.IntVar(&i.before, "B", 0, "lines of context before each --grep match")
	fs.IntVar(&i.before, "before", 0, "same as -B")
	fs.IntVar(&i.around, "C", 0, "lines of context before and after each --grep match")
	fs.StringVar(&i.followID, "follow-id", "", "JSON field: also show every line, from any pod, sharing its value with a line that passes the filters")
	return i
}

// incident is the validated result of incidentFlags.
type incident struct {
	before, after int
	ids           *filter.IDSet // nil = no --follow-id
}

func (i *incidentFlags) build(c common) (incident, error) {
	if i.after < 0 || i.before < 0 || i.around < 0 {
		return incident{}, errors.New("-A, -B and -C must not be negative")
	}
	in := incident{before: max(i.before, i.around), after: max(i.after, i.around)}
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
	cur     tagged // the line being replayed
	replay  bool
}

type tagged struct {
	l     parse.Line
	match bool // passed the filters on its own
}

// newStage makes a stage for one stream. barrier is non-nil only for fetch
// with --follow-id, and must have been Add()ed once per stream.
func (in incident) newStage(cfg filter.Config, barrier *sync.WaitGroup) *stage {
	s := &stage{f: filter.New(cfg), ids: in.ids, barrier: barrier}
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
		if m || s.ctx != nil || s.ids.HasField(l) {
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
	var out []parse.Line
	for _, t := range s.buf {
		s.cur = t
		out = append(out, s.emit(t.l)...)
	}
	s.buf = nil
	return out
}
