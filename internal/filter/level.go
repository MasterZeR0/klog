// Package filter holds pure line filters: level, JSON field, regex.
package filter

import (
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

// LineLevel reads a JSON line's level. The first present key wins.
// Numeric or unknown values report false.
func LineLevel(l parse.Line) (Level, bool) {
	for _, k := range levelKeys {
		v, present := l.JSON[k]
		if !present {
			continue
		}
		s, isString := v.(string)
		if !isString {
			return 0, false
		}
		return ParseLevel(s)
	}
	return 0, false
}
