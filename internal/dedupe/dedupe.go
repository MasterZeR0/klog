// Package dedupe collapses consecutive identical lines of one stream.
package dedupe

import (
	"fmt"

	"klog/internal/parse"
)

// Stage collapses runs of consecutive lines with the same Raw text (the
// kubectl timestamp is already split off). Use one Stage per stream; it is
// not safe for concurrent use.
//
// The first line of a run always goes out at once, so a lone line is never
// delayed. When a run of repeats ends, one more item follows: in pretty
// output a "… repeated N more times" summary line; with structured set
// (json and template output, which have no room for a summary line) a copy
// of the last repeat (its source, time, raw and JSON) with Repeats = N, the
// number of identical lines after the first.
type Stage struct {
	structured bool
	emit       func(parse.Line)
	first      parse.Line
	last       parse.Line // latest repeat of first
	have       bool
	n          int // repeats of first seen so far
}

func New(structured bool, emit func(parse.Line)) *Stage {
	return &Stage{structured: structured, emit: emit}
}

// Push feeds the next line of the stream.
func (s *Stage) Push(l parse.Line) {
	if s.have && l.Raw == s.first.Raw {
		s.n++
		s.last = l
		return
	}
	s.Flush()
	s.first, s.have = l, true
	s.emit(l)
}

// Flush ends the current run. Call it when the stream ends or is cancelled.
func (s *Stage) Flush() {
	if s.have && s.n > 0 {
		if s.structured {
			s.last.Repeats = s.n
			s.emit(s.last)
		} else {
			s.emit(parse.Line{Label: s.first.Label, Time: s.last.Time, Raw: fmt.Sprintf("… repeated %d more times", s.n)})
		}
	}
	s.have, s.n = false, 0
}
