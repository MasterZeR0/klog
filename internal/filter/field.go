package filter

import (
	"encoding/json"
	"fmt"
	"math"
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
	OpGt           // key>number
	OpGe           // key>=number
	OpLt           // key<number
	OpLe           // key<=number
)

// Field is one parsed --field expression. Key is a top-level JSON key or a
// dotted path into nested objects.
type Field struct {
	Key   string
	Op    Op
	Value string
	re    *regexp.Regexp
	num   float64 // Value as a number, for OpGt..OpLe
}

// ParseField parses key=value, key!=value, key~regex or key>number (also
// >=, <, <=). The first operator character decides, so values may contain any.
func ParseField(s string) (Field, error) {
	bad := fmt.Errorf("invalid --field %q: want key=value, key!=value, key~regex or key>number (>, >=, <, <=)", s)
	i := strings.IndexAny(s, "=~<>")
	if i <= 0 {
		return Field{}, bad
	}
	var f Field
	switch {
	case s[i] == '~':
		f = Field{Key: s[:i], Op: OpRe, Value: s[i+1:]}
	case s[i] == '<' || s[i] == '>':
		rest, eq := strings.CutPrefix(s[i+1:], "=")
		op := OpGt
		switch {
		case s[i] == '>' && eq:
			op = OpGe
		case s[i] == '<' && eq:
			op = OpLe
		case s[i] == '<':
			op = OpLt
		}
		f = Field{Key: s[:i], Op: op, Value: rest}
		n, err := strconv.ParseFloat(rest, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
			return Field{}, fmt.Errorf("invalid --field %q: %q is not a number", s, rest)
		}
		f.num = n
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

// lookup finds a dotted path in obj. A literal key containing dots wins over
// walking into nested objects, at every level. Arrays are not walked.
func lookup(obj map[string]any, path string) (any, bool) {
	if v, ok := obj[path]; ok {
		return v, true
	}
	for i, c := range path {
		if c != '.' {
			continue
		}
		if sub, ok := obj[path[:i]].(map[string]any); ok {
			if v, ok := lookup(sub, path[i+1:]); ok {
				return v, true
			}
		}
	}
	return nil, false
}

// Match reports whether obj satisfies the field. A missing key fails = and ~
// and satisfies !=. The numeric operators are false unless the value is a
// JSON number.
func (f Field) Match(obj map[string]any) bool {
	v, ok := lookup(obj, f.Key)
	if !ok {
		return f.Op == OpNe
	}
	if f.Op >= OpGt {
		n, ok := v.(json.Number)
		if !ok {
			return false
		}
		x, err := n.Float64()
		if err != nil {
			return false
		}
		switch f.Op {
		case OpGt:
			return x > f.num
		case OpGe:
			return x >= f.num
		case OpLt:
			return x < f.num
		default:
			return x <= f.num
		}
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
