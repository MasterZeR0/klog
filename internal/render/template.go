package render

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"
	"text/template"
	"time"

	"klog/internal/filter"
	"klog/internal/parse"
)

// SetTemplate parses src into o.Template. --format template and --template
// go together: one without the other is an error.
func (o *Options) SetTemplate(src string) error {
	switch {
	case o.Format == Template && src == "":
		return errors.New("--format template needs --template")
	case o.Format != Template && src != "":
		return errors.New("--template needs --format template")
	case src == "":
		return nil
	}
	t, err := template.New("line").Parse(src)
	if err != nil {
		return fmt.Errorf("invalid --template: %w", err)
	}
	o.Template = t
	return nil
}

// entry is what a --template sees.
type entry struct {
	Source string
	Time   time.Time      // in the --tz location; zero when kubectl gave none
	Raw    string         // the line without the kubectl timestamp
	Msg    string         // "msg" or "message" of a JSON line, else ""
	Level  string         // TRACE..FATAL, "" when unknown
	JSON   map[string]any // nil for plain text

	Repeats int // identical lines --dedupe folded into this one
}

// A template that fails on one line (say {{.JSON.req.id}} on plain text) must
// not end the run: that line keeps the output rendered before the failure and
// the first failure is reported once, here.
var (
	warnOut io.Writer = os.Stderr
	warned  atomic.Bool
)

func (r *Renderer) writeTemplate(l parse.Line) error {
	e := entry{Source: l.Label, Raw: l.Raw, JSON: l.JSON, Repeats: l.Repeats}
	if !l.Time.IsZero() {
		e.Time = l.Time.In(r.o.TZ)
	}
	for _, k := range msgKeys {
		if s, ok := l.JSON[k].(string); ok && s != "" {
			e.Msg = s
			break
		}
	}
	if lv, ok := filter.LineLevel(l); ok {
		e.Level = levelNames[lv]
	}
	var buf bytes.Buffer // render fully first so the line is written in one piece
	if err := r.o.Template.Execute(&buf, e); err != nil && warned.CompareAndSwap(false, true) {
		fmt.Fprintf(warnOut, "klog: --template failed on a line (later failures are not reported): %v\n", err)
	}
	buf.WriteByte('\n')
	_, err := r.w.Write(buf.Bytes())
	return err
}
