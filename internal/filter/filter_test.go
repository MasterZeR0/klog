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
		{`{"level":10}`, Trace, true},
		{`{"level":20}`, Debug, true},
		{`{"level":30}`, Info, true},
		{`{"level":40}`, Warn, true},
		{`{"level":50}`, Error, true},
		{`{"level":60}`, Fatal, true},
		{`{"level":35}`, Info, true}, // ranges round down to the nearest step
		{`{"level":30.5}`, Info, true},
		{`{"lvl":45}`, Warn, true},
		{`{"severity":50}`, Error, true},
		{`{"level":99}`, Fatal, true},
		{`{"level":9}`, 0, false},
		{`{"level":0}`, 0, false},
		{`{"level":-30}`, 0, false},
		{`{"level":true}`, 0, false},
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
		{"latency>500", "latency", OpGt, "500"},
		{"status>=500", "status", OpGe, "500"},
		{"latency<1.5", "latency", OpLt, "1.5"},
		{"n<=-3", "n", OpLe, "-3"},
		{"req.user.id=42", "req.user.id", OpEq, "42"},
		{"a=b>c", "a", OpEq, "b>c"},   // the first operator char decides
		{"a~x>=1", "a", OpRe, "x>=1"}, // so a regex may contain > and <
		{"a!=5", "a", OpNe, "5"},
		{"a=>5", "a", OpEq, ">5"},
	}
	for _, tc := range good {
		f, err := ParseField(tc.in)
		if err != nil || f.Key != tc.key || f.Op != tc.op || f.Value != tc.value {
			t.Errorf("ParseField(%q) = %+v, %v", tc.in, f, err)
		}
	}
	for _, bad := range []string{"novalue", "=v", "!=v", "a~(", "", ">5", "a>", "a>x", "a>=", "a<=x", "a>NaN", "a<inf", "a>=b=1"} {
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

func TestFieldMatchNested(t *testing.T) {
	obj := jl(`{"req":{"user":{"id":42,"name":"ann"},"list":[1]},"a.b":"top","a":{"b":"nested","c.d":"deep"},"n":null}`).JSON
	tests := []struct {
		expr string
		want bool
	}{
		{"req.user.id=42", true},
		{"req.user.id=43", false},
		{"req.user.name~^an", true},
		{"req.user.id>40", true},
		{"req.user.id!=42", false},
		{"req.user.missing=1", false},
		{"req.user.missing!=1", true}, // missing path behaves like a missing key
		{"req.user.missing~x", false},
		{"req.nope.id!=42", true},
		{"req.user.id.deeper=1", false}, // walking through a non-object is a miss
		{"req.user.id.deeper!=1", true},
		{"req.user=x", false}, // an object is not an intermediate hit
		{"a.b=top", true},     // a literal dotted top-level key wins
		{"a.b!=nested", true},
		{"a.c.d=deep", true},    // and so does one at a nested level
		{"req.list.0=1", false}, // arrays are not walked
		{"n.x=1", false},
	}
	for _, tc := range tests {
		f, err := ParseField(tc.expr)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Match(obj); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
	}
}

func TestFieldMatchNumeric(t *testing.T) {
	obj := jl(`{"n":500,"f":1.5,"neg":-2,"big":12345678901234567890,"s":"500","b":true,"z":null,"o":{"a":1}}`).JSON
	tests := []struct {
		expr string
		want bool
	}{
		{"n>499", true}, {"n>500", false}, {"n>=500", true}, {"n<501", true}, {"n<500", false}, {"n<=500", true},
		{"f>1", true}, {"f<2", true}, {"f>=1.5", true}, {"n>4.99e2", true},
		{"neg<0", true}, {"neg>-3", true},
		{"big>1e19", true}, {"big<1e20", true},
		{"s>1", false}, // strings are not numeric
		{"b>0", false},
		{"z<1", false},
		{"o>0", false},
		{"missing>0", false},
		{"missing<=0", false},
	}
	for _, tc := range tests {
		f, err := ParseField(tc.expr)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.Match(obj); got != tc.want {
			t.Errorf("%s = %v, want %v", tc.expr, got, tc.want)
		}
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

func TestFilterNumericLevel(t *testing.T) {
	got := keep(t, Config{MinLevel: Warn}, `{"level":30}`, `{"level":40}`, `{"level":50}`, `{"level":"warn"}`)
	if want := []bool{false, true, true, true}; !eq(got, want) {
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

func TestIsContinuation(t *testing.T) {
	cases := map[string]bool{
		"\tat Foo.bar(Foo.java:1)":       true,
		"  at Foo":                       true,
		"Caused by: java.io.IOException": true,
		"Suppressed: X":                  true,
		"... 12 more":                    true,
		"... 3 common frames omitted":    true,
		"":                               false,
		"plain":                          false,
		"java.lang.IOException: boom":    false,
		"Caused by":                      false,
		"caused by: x":                   false,
		"...12 more":                     false,
	}
	for raw, want := range cases {
		if got := IsContinuation(raw); got != want {
			t.Errorf("IsContinuation(%q) = %v, want %v", raw, got, want)
		}
	}
}
