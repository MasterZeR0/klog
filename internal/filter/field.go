package filter

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Op is a --field comparison.
type Op int

const (
	OpEq Op = iota // key=value
	OpNe           // key!=value
	OpRe           // key~regex
)

// Field is one parsed --field expression. Keys are top-level JSON keys.
type Field struct {
	Key   string
	Op    Op
	Value string
	re    *regexp.Regexp
}

// ParseField parses key=value, key!=value or key~regex.
func ParseField(s string) (Field, error) {
	bad := fmt.Errorf("invalid --field %q: want key=value, key!=value or key~regex", s)
	i := strings.IndexAny(s, "=~")
	if i <= 0 {
		return Field{}, bad
	}
	var f Field
	switch {
	case s[i] == '~':
		f = Field{Key: s[:i], Op: OpRe, Value: s[i+1:]}
	case s[i-1] == '!':
		f = Field{Key: s[:i-1], Op: OpNe, Value: s[i+1:]}
	default:
		f = Field{Key: s[:i], Op: OpEq, Value: s[i+1:]}
	}
	if f.Key == "" {
		return Field{}, bad
	}
	if f.Op == OpRe {
		re, err := regexp.Compile(f.Value)
		if err != nil {
			return Field{}, fmt.Errorf("invalid --field %q: %w", s, err)
		}
		f.re = re
	}
	return f, nil
}

// Match reports whether obj satisfies the field. A missing key fails = and ~
// and satisfies !=.
func (f Field) Match(obj map[string]any) bool {
	v, ok := obj[f.Key]
	if !ok {
		return f.Op == OpNe
	}
	s := fieldString(v)
	switch f.Op {
	case OpEq:
		return s == f.Value
	case OpNe:
		return s != f.Value
	default:
		return f.re.MatchString(s)
	}
}

func fieldString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	case nil:
		return "null"
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}
