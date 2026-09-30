// Package filter holds pure line filters: level, JSON field, regex.
package filter

import (
	"encoding/json"
	"strings"

	"klog/internal/parse"
)

// Level is a log severity. The zero value means "no level".
type Level int

const (
	Trace Level = iota + 1
	Debug
	Info
	Warn
	Error
	Fatal
)

var levelNames = map[string]Level{
	"TRACE": Trace, "DEBUG": Debug, "INFO": Info,
	"WARN": Warn, "WARNING": Warn,
	"ERROR": Error, "ERR": Error,
	"FATAL": Fatal, "CRITICAL": Fatal, "PANIC": Fatal,
}

// ParseLevel maps a level name (case-insensitive, with aliases) to a Level.
func ParseLevel(s string) (Level, bool) {
	l, ok := levelNames[strings.ToUpper(strings.TrimSpace(s))]
	return l, ok
}

var levelKeys = []string{"level", "severity", "lvl"}

// numericLevel maps a pino/bunyan level (10=TRACE ... 60=FATAL) to a Level.
// Values between steps round down (35 is INFO); 60 and above are FATAL.
func numericLevel(n json.Number) (Level, bool) {
	f, err := n.Float64()
	if err != nil || f < 10 {
		return 0, false
	}
	if f >= 60 {
		return Fatal, true
	}
	return Level(f / 10), true
}

// LineLevel reads a JSON line's level. The first present key wins.
// Unknown names and numbers below 10 report false.
func LineLevel(l parse.Line) (Level, bool) {
	for _, k := range levelKeys {
		v, present := l.JSON[k]
		if !present {
			continue
		}
		switch x := v.(type) {
		case string:
			return ParseLevel(x)
		case json.Number:
			return numericLevel(x)
		}
		return 0, false
	}
	return 0, false
}
