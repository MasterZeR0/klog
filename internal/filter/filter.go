package filter

import (
	"regexp"

	"klog/internal/parse"
)

// Config is the immutable filter chain. All set filters must pass (AND).
type Config struct {
	MinLevel Level // 0 = no level filter
	Fields   []Field
	Grep     *regexp.Regexp // nil = off
	Exclude  *regexp.Regexp // nil = off
}

func (c Config) active() bool {
	return c.MinLevel > 0 || len(c.Fields) > 0 || c.Grep != nil || c.Exclude != nil
}

// Filter applies a Config to one stream. It remembers whether the previous
// line was kept so stack-trace continuation lines stay attached. Use one
// Filter per stream; it is not safe for concurrent use.
type Filter struct {
	cfg      Config
	active   bool
	prevKept bool
}

func New(cfg Config) *Filter { return &Filter{cfg: cfg, active: cfg.active()} }

// Keep reports whether l passes the chain.
func (f *Filter) Keep(l parse.Line) bool {
	if !f.active {
		return true
	}
	if l.JSON == nil && continuation.MatchString(l.Raw) {
		return f.prevKept
	}
	f.prevKept = f.match(l)
	return f.prevKept
}

// continuation matches the start of a stack-trace continuation line.
var continuation = regexp.MustCompile(`^(\s|Caused by:|Suppressed:|\.\.\. \d+ (more|common frames omitted))`)

func (f *Filter) match(l parse.Line) bool {
	c := f.cfg
	if l.JSON == nil && (c.MinLevel > 0 || len(c.Fields) > 0) {
		return false
	}
	if c.MinLevel > 0 {
		lv, ok := LineLevel(l)
		if !ok || lv < c.MinLevel {
			return false
		}
	}
	for _, fl := range c.Fields {
		if !fl.Match(l.JSON) {
			return false
		}
	}
	if c.Grep != nil && !c.Grep.MatchString(l.Raw) {
		return false
	}
	if c.Exclude != nil && c.Exclude.MatchString(l.Raw) {
		return false
	}
	return true
}
