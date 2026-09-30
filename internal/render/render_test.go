package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"klog/internal/parse"
)

var ts = time.Date(2026, 9, 30, 12, 0, 1, 500_000_000, time.UTC)

func writeOpts(t *testing.T, o Options, ls ...parse.Line) string {
	t.Helper()
	var buf bytes.Buffer
	r := New(&buf, o)
	for _, l := range ls {
		if err := r.Write(l); err != nil {
			t.Fatal(err)
		}
	}
	return buf.String()
}

func write(t *testing.T, f Format, color bool, l parse.Line) string {
	t.Helper()
	return writeOpts(t, Options{Format: f, Color: color, TZ: time.UTC}, l)
}

func esc(sgr, s string) string { return "\x1b[" + sgr + "m" + s + "\x1b[0m" }

func TestParseFormat(t *testing.T) {
	for in, want := range map[string]Format{"pretty": Pretty, "json": JSON, "raw": Raw} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("ParseFormat(xml) should fail")
	}
}

func TestPretty(t *testing.T) {
	l := parse.Line{Label: "web-1/app", Time: ts, Raw: "hello"}
	if got := write(t, Pretty, false, l); got != "[web-1/app] 12:00:01.500 hello\n" {
		t.Fatalf("got %q", got)
	}
	if got := write(t, Pretty, false, parse.Line{Label: "p", Raw: "no time"}); got != "[p] no time\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrettyColorIsStablePerLabel(t *testing.T) {
	a1 := write(t, Pretty, true, parse.Line{Label: "a", Raw: "x"})
	a2 := write(t, Pretty, true, parse.Line{Label: "a", Raw: "y"})
	if !strings.HasPrefix(a1, "\x1b[") || !strings.Contains(a1, "\x1b[0m") {
		t.Fatalf("no colour codes in %q", a1)
	}
	if a1[:strings.Index(a1, "m")] != a2[:strings.Index(a2, "m")] {
		t.Fatal("colour for the same label changed")
	}
}

func TestRaw(t *testing.T) {
	if got := write(t, Raw, true, parse.Line{Label: "p", Time: ts, Raw: "just the line"}); got != "just the line\n" {
		t.Fatalf("got %q", got)
	}
}

func TestJSON(t *testing.T) {
	l := parse.Parse("web-1", "2026-09-30T12:00:01.5Z "+`{"level":"INFO","msg":"a<b"}`)
	var rec map[string]any
	if err := json.Unmarshal([]byte(write(t, JSON, false, l)), &rec); err != nil {
		t.Fatal(err)
	}
	if rec["source"] != "web-1" || rec["time"] != "2026-09-30T12:00:01.5Z" {
		t.Fatalf("bad record %v", rec)
	}
	if rec["json"].(map[string]any)["level"] != "INFO" {
		t.Fatalf("bad json %v", rec["json"])
	}
	if !strings.Contains(write(t, JSON, false, l), "a<b") {
		t.Fatal("HTML escaping should be off")
	}
	rec = nil
	_ = json.Unmarshal([]byte(write(t, JSON, false, parse.Line{Label: "p", Raw: "plain"})), &rec)
	if _, ok := rec["time"]; ok {
		t.Fatal("time should be omitted")
	}
	if _, ok := rec["json"]; ok {
		t.Fatal("json should be omitted")
	}
}

func TestPrettyTimestampIsStyled(t *testing.T) {
	got := write(t, Pretty, true, parse.Line{Label: "p", Time: ts, Raw: "hello"})
	if !strings.Contains(got, esc("2", "12:00:01.500")+" hello\n") {
		t.Fatalf("got %q", got)
	}
}

func TestPrettyLevelStyles(t *testing.T) {
	opts := Options{Format: Pretty, Color: true, NoFlatten: true}
	for lvl, sgr := range map[string]string{
		"TRACE": "2", "DEBUG": "2", "WARN": "33", "ERROR": "1;31", "FATAL": "1;97;41",
	} {
		raw := `{"level":"` + lvl + `","msg":"hi"}`
		if got := writeOpts(t, opts, parse.Parse("p", raw)); !strings.Contains(got, esc(sgr, raw)+"\n") {
			t.Errorf("%s: got %q", lvl, got)
		}
	}
	raw := `{"level":"INFO","msg":"hi"}`
	got := writeOpts(t, opts, parse.Parse("p", raw))
	if !strings.Contains(got, " "+raw+"\n") || strings.Contains(got, "\x1b[m") {
		t.Errorf("INFO should be unstyled, got %q", got)
	}
	raw = `{"msg":"no level"}`
	if got := writeOpts(t, opts, parse.Parse("p", raw)); !strings.Contains(got, " "+raw+"\n") {
		t.Errorf("no level should be unstyled, got %q", got)
	}
}

func TestPrettyStackTraceStyles(t *testing.T) {
	opts := Options{Format: Pretty, Color: true, NoFlatten: true}
	for raw, want := range map[string]string{
		"\tat Foo.bar(Foo.java:1)":       esc("2", "\tat Foo.bar(Foo.java:1)"),
		"Caused by: java.io.IOException": esc("31", "Caused by: java.io.IOException"),
		"\tSuppressed: X":                esc("31", "\tSuppressed: X"),
		"\t... 12 more":                  esc("2", "\t... 12 more"),
		"... 3 common frames omitted":    esc("2", "... 3 common frames omitted"),
		"java.lang.IOException: boom":    "java.lang.IOException: boom", // the header line is not a continuation
	} {
		got := writeOpts(t, opts, parse.Line{Label: "p", Raw: raw})
		if !strings.HasSuffix(got, " "+want+"\n") {
			t.Errorf("%q: got %q, want suffix %q", raw, got, want)
		}
	}
}

func TestPrettyStackTraceColourOffKeepsText(t *testing.T) {
	got := write(t, Pretty, false, parse.Line{Label: "p", Raw: "\tat Foo"})
	if got != "[p] \tat Foo\n" {
		t.Fatalf("got %q", got)
	}
}

func TestPrettyInterleavedTracesRenderLikeTheyDoAlone(t *testing.T) {
	opts := Options{Format: Pretty, Color: true, NoFlatten: true}
	a := []parse.Line{
		{Label: "a-1", Raw: "java.lang.IOException: boom"}, {Label: "a-1", Raw: "\tat A.one(A.java:1)"},
		{Label: "a-1", Raw: "Caused by: X"},
	}
	b := []parse.Line{
		{Label: "b-1", Raw: "plain b"}, {Label: "b-1", Raw: "\t... 2 more"}, {Label: "b-1", Raw: "\tat B.two(B.java:2)"},
	}
	mixed := writeOpts(t, opts, a[0], b[0], a[1], b[1], a[2], b[2])
	only := func(out, label string) []string {
		var keep []string
		for _, ln := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
			if strings.Contains(ln, label) {
				keep = append(keep, ln)
			}
		}
		return keep
	}
	for label, want := range map[string]string{"a-1": writeOpts(t, opts, a...), "b-1": writeOpts(t, opts, b...)} {
		if got, w := strings.Join(only(mixed, label), "\n"), strings.TrimSuffix(want, "\n"); got != w {
			t.Errorf("%s: interleaved\n%s\nalone\n%s", label, got, w)
		}
	}
}

func TestPrettyPadsLabelsAndNeverShrinks(t *testing.T) {
	got := writeOpts(t, Options{Format: Pretty}, // colour off
		parse.Line{Label: "ab", Raw: "x"}, parse.Line{Label: "abcd", Raw: "x"}, parse.Line{Label: "ab", Raw: "x"})
	if want := "[ab] x\n[abcd] x\n[ab]   x\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	coloured := writeOpts(t, Options{Format: Pretty, Color: true},
		parse.Line{Label: "abcd", Raw: "x"}, parse.Line{Label: "ab", Raw: "x"})
	if !strings.Contains(coloured, "\x1b[0m   x\n") { // padding sits outside the escape codes
		t.Fatalf("got %q", coloured)
	}
}

func TestNewZeroOptionsUsesDefaultTheme(t *testing.T) {
	got := writeOpts(t, Options{Format: Pretty, Color: true}, parse.Line{Label: "p", Raw: "x"})
	if !strings.HasPrefix(got, "\x1b[") || !strings.HasSuffix(got, " x\n") {
		t.Fatalf("got %q", got)
	}
}

func TestPrettyFlatten(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"level msg and sorted keys", `{"level":"INFO","msg":"hello","b":2,"a":"x"}`, `INFO hello  a=x b=2`},
		{"level alias is normalised", `{"severity":"warning","msg":"m"}`, `WARN m`},
		{"no msg", `{"level":"WARN","a":1}`, `WARN  a=1`},
		{"no level", `{"msg":"hi","a":1}`, `hi  a=1`},
		{"msg only", `{"msg":"hi"}`, `hi`},
		{"message alias", `{"message":"hi"}`, `hi`},
		{"msg wins and message is kept", `{"msg":"a","message":"b"}`, `a  message=b`},
		{"non-string msg is kept", `{"msg":5}`, `msg=5`},
		{"numeric level is kept", `{"level":30,"msg":"m"}`, `m  level=30`},
		{"empty object falls back to raw", `{}`, `{}`},
		{"empty string value is quoted", `{"msg":"m","e":""}`, `m  e=""`},
		{"spaces and quotes are quoted", `{"msg":"m","k":"a b","q":"say \"hi\""}`, `m  k="a b" q="say \"hi\""`},
		{"newline in value is escaped", `{"msg":"m","n":"l1\nl2"}`, `m  n="l1\nl2"`},
		{"newline in msg is escaped", `{"msg":"a\nb"}`, `a\nb`},
		{"big integer stays exact", `{"msg":"m","id":12345678901234567890}`, `m  id=12345678901234567890`},
		{"nested values are compact json", `{"msg":"m","o":{"b":1,"a":[1,"x<y"]},"t":true,"z":null}`, `m  o={"a":[1,"x<y"],"b":1} t=true z=null`},
		{"time keys are kept without a kubectl timestamp", `{"msg":"m","ts":"2026"}`, `m  ts=2026`},
		{"empty msg stays a pair", `{"msg":"","a":1}`, `a=1 msg=""`},
		{"empty msg falls through to message", `{"msg":"","message":"hi"}`, `hi  msg=""`},
		{"escape in msg is escaped", `{"msg":"x \u001b[31mRED"}`, `x \x1b[31mRED`},
		{"escape in value is quoted", `{"msg":"m","k":"v\u001b[2Jx"}`, `m  k="v\x1b[2Jx"`},
		{"escape in key is escaped", `{"msg":"m","a\u001b[0mb":1}`, `m  a\x1b[0mb=1`},
		{"bel in value is quoted", `{"msg":"m","k":"a\u0007b"}`, `m  k="a\ab"`},
		{"tab in msg is escaped", `{"msg":"a\tb"}`, `a\tb`},
		{"bidi override in value is quoted", `{"msg":"m","k":"a\u202eb"}`, `m  k="a\u202eb"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := writeOpts(t, Options{Format: Pretty}, parse.Parse("p", tc.in))
			if want := "[p] " + tc.want + "\n"; got != want {
				t.Fatalf("got %q, want %q", got, want)
			}
		})
	}
}

func TestPrettyFlattenDropsTimeKeysWhenKubectlTimeIsShown(t *testing.T) {
	l := parse.Parse("p", "2026-09-30T12:00:01.5Z "+`{"msg":"m","ts":"a","time":"b","timestamp":"c","a":1}`)
	got := writeOpts(t, Options{Format: Pretty, TZ: time.UTC}, l)
	if want := "[p] 12:00:01.500 m  a=1\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestPrettyFlattenColours(t *testing.T) {
	opts := Options{Format: Pretty, Color: true}
	for lvl, sgr := range map[string]string{
		"TRACE": "2", "DEBUG": "2", "WARN": "33", "ERROR": "1;31", "FATAL": "1;97;41",
	} {
		got := writeOpts(t, opts, parse.Parse("p", `{"level":"`+lvl+`","msg":"hi"}`))
		if !strings.HasSuffix(got, " "+esc(sgr, lvl)+" hi\n") {
			t.Errorf("%s: got %q", lvl, got)
		}
	}
	got := writeOpts(t, opts, parse.Parse("p", `{"level":"INFO","msg":"hi"}`))
	if !strings.HasSuffix(got, " INFO hi\n") || strings.Contains(got, "\x1b[m") {
		t.Errorf("INFO should be unstyled, got %q", got)
	}
	got = writeOpts(t, opts, parse.Parse("p", `{"level":"ERROR","msg":"boom","k":"v"}`))
	if !strings.HasSuffix(got, " "+esc("1;31", "ERROR")+" boom  "+esc("36", "k=")+"v\n") {
		t.Errorf("keys: got %q", got)
	}
}

func TestPrettyNoFlattenKeepsRawJSON(t *testing.T) {
	raw := `{"level":"ERROR","msg":"x","a":1}`
	if got := writeOpts(t, Options{Format: Pretty, NoFlatten: true}, parse.Parse("p", raw)); got != "[p] "+raw+"\n" {
		t.Fatalf("got %q", got)
	}
	got := writeOpts(t, Options{Format: Pretty, NoFlatten: true, Color: true}, parse.Parse("p", raw))
	if !strings.HasSuffix(got, " "+esc("1;31", raw)+"\n") {
		t.Fatalf("level style should still apply, got %q", got)
	}
}

func TestPrettyFlattenOnlyAffectsPretty(t *testing.T) {
	raw := `{"level":"INFO","msg":"x"}`
	if got := write(t, Raw, false, parse.Parse("p", raw)); got != raw+"\n" {
		t.Fatalf("raw changed: %q", got)
	}
	if got := write(t, JSON, false, parse.Parse("p", raw)); !strings.Contains(got, `"raw":"{\"level\":\"INFO\",\"msg\":\"x\"}"`) {
		t.Fatalf("json changed: %q", got)
	}
}
