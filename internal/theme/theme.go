// Package theme holds the colours of the pretty renderer and loads them from
// a JSON file. Every value is an SGR parameter string such as "1;31".
package theme

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
)

type Levels struct {
	Trace string `json:"trace"`
	Debug string `json:"debug"`
	Info  string `json:"info"`
	Warn  string `json:"warn"`
	Error string `json:"error"`
	Fatal string `json:"fatal"`
}

type Trace struct {
	Frame    string `json:"frame"`
	CausedBy string `json:"caused_by"`
	Omitted  string `json:"omitted"`
}

type Theme struct {
	Timestamp string   `json:"timestamp"`
	Levels    Levels   `json:"levels"`
	Trace     Trace    `json:"trace"`
	Keys      string   `json:"keys"`
	Labels    []string `json:"labels"`
}

// Default is the built-in theme. Labels is a fresh slice on every call.
func Default() Theme {
	return Theme{
		Timestamp: "2",
		Levels:    Levels{Trace: "2", Debug: "2", Info: "", Warn: "33", Error: "1;31", Fatal: "1;97;41"},
		Trace:     Trace{Frame: "2", CausedBy: "31", Omitted: "2"},
		Keys:      "36",
		Labels:    []string{"36", "32", "33", "35", "34", "31"},
	}
}

// Paint wraps s in the SGR sequence sgr. An empty sgr leaves s untouched.
func Paint(sgr, s string) string {
	if sgr == "" {
		return s
	}
	return "\x1b[" + sgr + "m" + s + "\x1b[0m"
}

// Load reads a theme file over Default(): fields the file omits keep their
// default. Every error names the file and wraps its cause.
func Load(path string) (Theme, error) {
	t, err := load(path)
	if err != nil {
		return Theme{}, fmt.Errorf("invalid theme %s: %w", path, err)
	}
	return t, nil
}

func load(path string) (Theme, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, err
	}
	t := Default()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&t); err != nil {
		return Theme{}, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return Theme{}, errors.New("unexpected data after the theme object")
	}
	if err := t.validate(); err != nil {
		return Theme{}, err
	}
	return t, nil
}

var sgrRe = regexp.MustCompile(`^[0-9;]*$`)

func (t Theme) validate() error {
	fields := []struct{ name, val string }{
		{"timestamp", t.Timestamp},
		{"levels.trace", t.Levels.Trace}, {"levels.debug", t.Levels.Debug},
		{"levels.info", t.Levels.Info}, {"levels.warn", t.Levels.Warn},
		{"levels.error", t.Levels.Error}, {"levels.fatal", t.Levels.Fatal},
		{"trace.frame", t.Trace.Frame}, {"trace.caused_by", t.Trace.CausedBy},
		{"trace.omitted", t.Trace.Omitted},
		{"keys", t.Keys},
	}
	for i, l := range t.Labels {
		fields = append(fields, struct{ name, val string }{fmt.Sprintf("labels[%d]", i), l})
	}
	for _, f := range fields {
		if !sgrRe.MatchString(f.val) {
			return fmt.Errorf("%s: %q is not an SGR code (digits and ';' only)", f.name, f.val)
		}
	}
	if len(t.Labels) == 0 {
		return errors.New("labels must not be empty")
	}
	return nil
}
