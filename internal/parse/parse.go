// Package parse turns one kubectl log line into a Line.
package parse

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

// Line is one parsed log line.
type Line struct {
	Label string         // "pod" or "pod/container"
	Time  time.Time      // kubectl timestamp; zero when absent
	Raw   string         // the line without the kubectl timestamp prefix
	JSON  map[string]any // non-nil only when Raw is exactly one JSON object
}

// SplitTimestamp splits the kubectl --timestamps prefix off line.
// It returns the zero time and the whole line when the prefix is absent.
func SplitTimestamp(line string) (time.Time, string) {
	tok, rest, _ := strings.Cut(line, " ")
	t, err := time.Parse(time.RFC3339Nano, tok)
	if err != nil {
		return time.Time{}, line
	}
	return t, rest
}

// Parse reads the timestamp prefix and tries JSON. It never fails: a line
// that is not a JSON object is returned with JSON == nil.
func Parse(label, kubectlLine string) Line {
	ts, raw := SplitTimestamp(kubectlLine)
	return Line{Label: label, Time: ts, Raw: raw, JSON: decodeObject(raw)}
}

func decodeObject(s string) map[string]any {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber() // keep big integers exact
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil
	}
	if _, err := dec.Token(); err != io.EOF { // trailing data after the object
		return nil
	}
	return m
}
