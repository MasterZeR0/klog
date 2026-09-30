package render

import (
	"os"
	"strings"
	"testing"
	"time"

	"klog/internal/parse"
)

func tmplOpts(t *testing.T, src string, tz *time.Location) Options {
	t.Helper()
	o := Options{Format: Template, TZ: tz}
	if err := o.SetTemplate(src); err != nil {
		t.Fatal(err)
	}
	return o
}

func TestTemplateFields(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip(err)
	}
	o := tmplOpts(t, `{{.Source}}|{{.Time.Format "15:04:05"}}|{{.Level}}|{{.Msg}}|{{.JSON.requestId}}|{{.Raw}}`, ny)
	got := writeOpts(t, o,
		parse.Line{Label: "a-1", Time: ts, Raw: `{"level":"warn","msg":"hi","requestId":"r1"}`,
			JSON: map[string]any{"level": "warn", "msg": "hi", "requestId": "r1"}},
		parse.Line{Label: "b-1", Raw: "plain text"},
	)
	want := `a-1|08:00:01|WARN|hi|r1|{"level":"warn","msg":"hi","requestId":"r1"}` + "\n" +
		"b-1|00:00:00|||<no value>|plain text\n" // text/template prints a missing map key as <no value>
	if got != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestTemplateMessageKeyAndNilJSON(t *testing.T) {
	o := tmplOpts(t, `{{.Msg}}|{{if .JSON}}json{{else}}text{{end}}|{{.Time.IsZero}}`, nil)
	got := writeOpts(t, o,
		parse.Line{Label: "a", Raw: "{}", JSON: map[string]any{"message": "m"}},
		parse.Line{Label: "a", Raw: "x"},
	)
	if want := "m|json|true\n|text|true\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSetTemplateErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		f   Format
		src string
	}{
		"parse error":          {Template, "{{.Msg"},
		"template, no format":  {Raw, "{{.Msg}}"},
		"format, no template":  {Template, ""},
		"unknown function":     {Template, "{{nope .Msg}}"},
		"pretty, no template":  {Pretty, ""},
		"json with a template": {JSON, "x"},
	} {
		o := Options{Format: tc.f}
		err := o.SetTemplate(tc.src)
		if (name == "pretty, no template") != (err == nil) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestTemplateExecErrorIsPerLineAndWarnsOnce(t *testing.T) {
	var warn strings.Builder
	warnOut = &warn
	warned.Store(false)
	t.Cleanup(func() { warnOut = os.Stderr })

	o := tmplOpts(t, `{{.Raw}}|{{.JSON.req.id}}`, nil)
	var sb strings.Builder
	r := New(&sb, o)
	for _, raw := range []string{"one", "two"} {
		if err := r.Write(parse.Line{Label: "a", Raw: raw, JSON: map[string]any{"req": nil}}); err != nil {
			t.Fatalf("exec error must not be fatal: %v", err)
		}
	}
	if got, want := sb.String(), "one|\ntwo|\n"; got != want {
		t.Fatalf("output %q, want %q", got, want)
	}
	if n := strings.Count(warn.String(), "\n"); n != 1 || !strings.Contains(warn.String(), "--template") {
		t.Fatalf("want exactly one warning, got %q", warn.String())
	}
}

func TestRepeatsInJSONAndTemplate(t *testing.T) {
	l := parse.Line{Label: "a", Raw: "x", Repeats: 3}
	if got := write(t, JSON, false, l); !strings.Contains(got, `"repeats":3`) {
		t.Errorf("json: %q", got)
	}
	if got := write(t, JSON, false, parse.Line{Label: "a", Raw: "x"}); strings.Contains(got, "repeats") {
		t.Errorf("json without repeats: %q", got)
	}
	if got := writeOpts(t, tmplOpts(t, `{{.Raw}} {{.Repeats}}`, nil), l); got != "x 3\n" {
		t.Errorf("template: %q", got)
	}
}

func TestParseFormatTemplate(t *testing.T) {
	if got, err := ParseFormat("template"); err != nil || got != Template {
		t.Fatalf("got %v, %v", got, err)
	}
}
