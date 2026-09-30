package stats

import (
	"bytes"
	"strings"
	"testing"

	"klog/internal/parse"
)

func add(t *Table, label, raw string) { t.Add(parse.Parse(label, raw)) }

func sample() *Table {
	t := New()
	add(t, "b-1", `{"level":"ERROR","msg":"x"}`)
	add(t, "a-1", `{"level":"INFO","msg":"x"}`)
	add(t, "a-1", `{"severity":"warning"}`)
	add(t, "a-1", `{"level":"info"}`)
	add(t, "a-1", `plain text`)
	add(t, "a-1", `{"level":30}`) // numeric: unrecognised
	return t
}

func TestWriteText(t *testing.T) {
	var b bytes.Buffer
	if err := sample().WriteText(&b); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"SOURCE  TRACE  DEBUG  INFO  WARN  ERROR  FATAL  ?  TOTAL",
		"a-1     0      0      2     1     0      0      2  5",
		"b-1     0      0      0     0     1      0      0  1",
		"TOTAL   0      0      2     1     1      0      2  6",
	}
	got := strings.Split(strings.TrimRight(b.String(), "\n"), "\n")
	for i := range got {
		got[i] = strings.TrimRight(got[i], " ")
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWriteJSONSortedBySource(t *testing.T) {
	var b bytes.Buffer
	if err := sample().WriteJSON(&b); err != nil {
		t.Fatal(err)
	}
	want := `{"source":"a-1","counts":{"?":2,"DEBUG":0,"ERROR":0,"FATAL":0,"INFO":2,"TRACE":0,"WARN":1},"total":5}
{"source":"b-1","counts":{"?":0,"DEBUG":0,"ERROR":1,"FATAL":0,"INFO":0,"TRACE":0,"WARN":0},"total":1}
`
	if b.String() != want {
		t.Fatalf("got\n%s\nwant\n%s", b.String(), want)
	}
}

func TestEmpty(t *testing.T) {
	var b bytes.Buffer
	New().WriteJSON(&b)
	if b.Len() != 0 {
		t.Fatalf("json for no lines = %q", b.String())
	}
	New().WriteText(&b)
	if !strings.Contains(b.String(), "TOTAL") {
		t.Fatalf("text for no lines = %q", b.String())
	}
}
