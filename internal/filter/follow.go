package filter

import (
	"encoding/json"
	"strconv"
	"sync"

	"klog/internal/parse"
)

// IDSet collects the values of one JSON field (--follow-id) from seed lines
// so every line sharing a value can be shown. It is safe for concurrent use:
// tail streams share one.
type IDSet struct {
	key string
	mu  sync.Mutex
	ids map[string]struct{}
}

// NewIDSet follows key, a literal top-level JSON key.
// ponytail: no dotted paths; add them here if --field gains nested keys.
func NewIDSet(key string) *IDSet { return &IDSet{key: key, ids: map[string]struct{}{}} }

// id reads l's value of the field. Only strings, numbers and bools count as IDs.
func (s *IDSet) id(l parse.Line) (string, bool) {
	switch x := l.JSON[s.key].(type) {
	case string:
		return x, true
	case json.Number:
		return x.String(), true
	case bool:
		return strconv.FormatBool(x), true
	}
	return "", false
}

// Add records l's ID and reports whether l had one.
func (s *IDSet) Add(l parse.Line) bool {
	id, ok := s.id(l)
	if ok {
		s.mu.Lock()
		s.ids[id] = struct{}{}
		s.mu.Unlock()
	}
	return ok
}

// Has reports whether l's ID was added.
func (s *IDSet) Has(l parse.Line) bool {
	id, ok := s.id(l)
	if !ok {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, in := s.ids[id]
	return in
}

// HasField reports whether l carries an ID at all, seed or not.
func (s *IDSet) HasField(l parse.Line) bool {
	_, ok := s.id(l)
	return ok
}
