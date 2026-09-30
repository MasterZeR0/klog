package filter

import "klog/internal/parse"

// Keeper decides whether a line is a match. *Filter is one.
type Keeper interface{ Keep(parse.Line) bool }

// Context adds grep-style -B/-A context around the lines a Keeper matches.
// Context is counted in log records, not physical lines: a record is a head
// line plus the continuation lines (stack-trace frames) attached to it, and
// they always travel together. Use one per stream, so context never mixes
// pods; it is not safe for concurrent use.
type Context struct {
	k             Keeper
	before, after int
	ring          [][]parse.Line // unmatched records waiting as before-context, oldest first
	left          int            // after-context records still to emit
	open          bool           // the current record is being emitted: its continuations follow it
}

func NewContext(k Keeper, before, after int) *Context {
	return &Context{k: k, before: before, after: after}
}

// Feed takes the next line of the stream and returns the lines to emit now,
// in order: the buffered before-context records and the match, or one
// after-context line. A line is emitted at most once: emitting clears the
// ring, and after-context lines never enter it.
func (c *Context) Feed(l parse.Line) []parse.Line {
	cont := l.JSON == nil && IsContinuation(l.Raw)
	switch {
	case c.k.Keep(l):
		var out []parse.Line
		for _, r := range c.ring {
			out = append(out, r...)
		}
		c.ring, c.left, c.open = nil, c.after, true
		return append(out, l)
	case cont && c.open:
		return []parse.Line{l}
	case cont:
		if n := len(c.ring); n > 0 {
			c.ring[n-1] = append(c.ring[n-1], l)
		} else if c.before > 0 {
			c.ring = [][]parse.Line{{l}} // orphan frame at the start of the stream
		}
	case c.left > 0:
		c.left--
		c.open = true
		return []parse.Line{l}
	default:
		c.open = false
		if c.before > 0 {
			if c.ring = append(c.ring, []parse.Line{l}); len(c.ring) > c.before {
				c.ring = c.ring[1:]
			}
		}
	}
	return nil
}
