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

func write(t *testing.T, f Format, color bool, l parse.Line) string {
	t.Helper()
	var buf bytes.Buffer
	if err := New(&buf, f, color, time.UTC).Write(l); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

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
