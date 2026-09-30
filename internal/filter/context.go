package filter

import "klog/internal/parse"

// Keeper decides whether a line is a match. *Filter is one.
type Keeper interface{ Keep(parse.Line) bool }

// Context adds grep-style -B/-A context around the lines a Keeper matches.
// Use one per stream, so context never mixes pods; it is not safe for
// concurrent use.
type Context struct {
	k             Keeper
	before, after int
	ring          []parse.Line // unmatched lines waiting as before-context, oldest first
	left          int          // after-context lines still to emit
}

func NewContext(k Keeper, before, after int) *Context {
	return &Context{k: k, before: before, after: after}
}

// Feed takes the next line of the stream and returns the lines to emit now,
// in order: the buffered before-context and the match, or one after-context
// line. A line is emitted at most once: emitting clears the ring, and
// after-context lines never enter it.
func (c *Context) Feed(l parse.Line) []parse.Line {
	switch {
	case c.k.Keep(l):
		out := append(c.ring, l)
		c.ring = nil
		c.left = c.after
		return out
	case c.left > 0:
		c.left--
		return []parse.Line{l}
	case c.before > 0:
		if c.ring = append(c.ring, l); len(c.ring) > c.before {
			c.ring = c.ring[1:]
		}
	}
	return nil
}
