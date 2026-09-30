package filter

import (
	"regexp"
	"testing"

	"klog/internal/parse"
)

func jl(raw string) parse.Line { return parse.Parse("p", raw) } // no timestamp prefix

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"trace": Trace, "DEBUG": Debug, "Info": Info, "warn": Warn, "WARNING": Warn,
		"error": Error, "ERR": Error, "fatal": Fatal, "CRITICAL": Fatal, "panic": Fatal,
	}
	for in, want := range cases {
		got, ok := ParseLevel(in)
		if !ok || got != want {
			t.Errorf("ParseLevel(%q) = %v, %v; want %v", in, got, ok, want)
		}
	}
	if _, ok := ParseLevel("loud"); ok {
		t.Error("ParseLevel(loud) should fail")
	}
}

func TestLineLevel(t *testing.T) {
	tests := []struct {
		raw  string
		want Level
		ok   bool
	}{
		{`{"level":"ERROR"}`, Error, true},
		{`{"severity":"warning"}`, Warn, true},
		{`{"lvl":"debug"}`, Debug, true},
		{`{"severity":"ERROR","level":"INFO"}`, Info, true}, // level wins over severity
		{`{"level":30}`, 0, false},                          // numeric levels unsupported
		{`{"level":"LOUD"}`, 0, false},
		{`{"msg":"x"}`, 0, false},
		{`plain`, 0, false},
	}
	for _, tc := range tests {
		got, ok := LineLevel(jl(tc.raw))
		if ok != tc.ok || got != tc.want {
			t.Errorf("LineLevel(%s) = %v, %v; want %v, %v", tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestParseField(t *testing.T) {
	good := []struct {
		in    string
		key   string
		op    Op
		value string
	}{
		{"requestId=abc", "requestId", OpEq, "abc"},
		{"status!=200", "status", OpNe, "200"},
		{"msg~time(d)? out", "msg", OpRe, "time(d)? out"},
		{"expr=a=b", "expr", OpEq, "a=b"},
		{"re=a~b", "re", OpEq, "a~b"},
		{"k=", "k", OpEq, ""},
	}
	for _, tc := range good {
		f, err := ParseField(tc.in)
		if err != nil || f.Key != tc.key || f.Op != tc.op || f.Value != tc.value {
			t.Errorf("ParseField(%q) = %+v, %v", tc.in, f, err)
		}
	}
	for _, bad := range []string{"novalue", "=v", "!=v", "a~(", ""} {
		if _, err := ParseField(bad); err == nil {
			t.Errorf("ParseField(%q) should fail", bad)
		}
	}
}

func TestFieldMatchMissingKey(t *testing.T) {
	obj := map[string]any{"a": "1"}
	must := func(s string) Field {
		f, err := ParseField(s)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if !must("missing!=x").Match(obj) {
		t.Error("!= on a missing key should match")
	}
	if must("missing=x").Match(obj) {
		t.Error("= on a missing key should not match")
	}
	if must("missing~x").Match(obj) {
		t.Error("~ on a missing key should not match")
	}
}

func TestFilterBigNumberField(t *testing.T) {
	f, _ := ParseField("orderId=12345678901234567890")
	if !f.Match(jl(`{"orderId":12345678901234567890}`).JSON) {
		t.Fatal("big integer field should match exactly")
	}
}

func TestFieldValueTypes(t *testing.T) {
	l := jl(`{"n":500,"ok":true,"nil":null,"obj":{"a":1},"s":"str"}`)
	for _, expr := range []string{"n=500", "ok=true", "nil=null", `obj={"a":1}`, "s=str", "s~^st"} {
		f, err := ParseField(expr)
		if err != nil || !f.Match(l.JSON) {
			t.Errorf("%s should match (err=%v)", expr, err)
		}
	}
}

func keep(t *testing.T, cfg Config, raws ...string) []bool {
	t.Helper()
	f := New(cfg)
	out := make([]bool, len(raws))
	for i, r := range raws {
		out[i] = f.Keep(jl(r))
	}
	return out
}

func eq(a, b []bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFilterLevel(t *testing.T) {
	got := keep(t, Config{MinLevel: Warn},
		`{"level":"warn"}`, `{"level":"ERROR"}`, `{"level":"INFO"}`, `{"msg":"no level"}`, `plain text`)
	want := []bool{true, true, false, false, false}
	if !eq(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFilterFieldsAreAnded(t *testing.T) {
	ne, _ := ParseField("status!=200")
	re, _ := ParseField("msg~timeout")
	got := keep(t, Config{Fields: []Field{ne, re}},
		`{"status":500,"msg":"upstream timeout"}`,
		`{"status":200,"msg":"upstream timeout"}`,
		`{"status":500,"msg":"boom"}`,
		`raw line`)
	want := []bool{true, false, false, false}
	if !eq(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFilterGrepExclude(t *testing.T) {
	cfg := Config{Grep: regexp.MustCompile("user"), Exclude: regexp.MustCompile("healthz")}
	got := keep(t, cfg, `{"msg":"user login"}`, "user raw", `{"msg":"user healthz"}`, "other")
	want := []bool{true, true, false, false}
	if !eq(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFilterStackTraceAttach(t *testing.T) {
	cfg := Config{Grep: regexp.MustCompile("ERROR")}
	got := keep(t, cfg,
		"ERROR boom",                      // matches
		"\tat com.acme.Foo.bar(Foo.java)", // continuation, attaches
		"Caused by: java.io.IOException",  // continuation, attaches
		"\t... 12 more",                   // continuation, attaches
		"INFO fine",                       // not a continuation, dropped
		"\tat com.acme.Baz.qux(Baz.java)", // continuation of a dropped line, dropped
		"ERROR again",
		"\tat x",
	)
	want := []bool{true, true, true, true, false, false, true, true}
	if !eq(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFilterEmptyConfigKeepsEverything(t *testing.T) {
	got := keep(t, Config{}, "\tat first line is a continuation", `{"a":1}`, "plain")
	if !eq(got, []bool{true, true, true}) {
		t.Fatalf("got %v", got)
	}
}
