// Package dedupe collapses consecutive identical lines of one stream.
package dedupe

import (
	"fmt"
	"time"

	"klog/internal/parse"
)

// Stage collapses runs of consecutive lines with the same Raw text (the
// kubectl timestamp is already split off). Use one Stage per stream; it is
// not safe for concurrent use.
//
// By default the first line of a run goes out at once and a summary line
// follows when the run ends. With hold, output is structured and cannot carry
// a summary line: the first line is held until the run ends and then emitted
// once with Repeats set.
type Stage struct {
	hold     bool
	emit     func(parse.Line)
	first    parse.Line
	have     bool
	n        int       // repeats of first seen so far
	lastTime time.Time // time of the latest repeat
}

func New(hold bool, emit func(parse.Line)) *Stage { return &Stage{hold: hold, emit: emit} }

// Push feeds the next line of the stream.
func (s *Stage) Push(l parse.Line) {
	if s.have && l.Raw == s.first.Raw {
		s.n++
		s.lastTime = l.Time
		return
	}
	s.Flush()
	s.first, s.have = l, true
	if !s.hold {
		s.emit(l)
	}
}

// Flush ends the current run. Call it when the stream ends or is cancelled.
func (s *Stage) Flush() {
	if !s.have {
		return
	}
	switch {
	case s.hold:
		s.first.Repeats = s.n
		s.emit(s.first)
	case s.n > 0:
		s.emit(parse.Line{Label: s.first.Label, Time: s.lastTime, Raw: fmt.Sprintf("… repeated %d more times", s.n)})
	}
	s.have, s.n = false, 0
}
