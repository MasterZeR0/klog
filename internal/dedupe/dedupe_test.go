package dedupe

import (
	"strings"
	"testing"
	"time"

	"klog/internal/parse"
)

var t0 = time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)

func line(raw string, sec int) parse.Line {
	return parse.Line{Label: "a", Raw: raw, Time: t0.Add(time.Duration(sec) * time.Second)}
}

func collect(hold bool) (*Stage, *[]parse.Line) {
	var got []parse.Line
	return New(hold, func(l parse.Line) { got = append(got, l) }), &got
}

func summary(got []parse.Line) string {
	var b strings.Builder
	for _, l := range got {
		b.WriteString(l.Raw)
		if l.Repeats > 0 {
			b.WriteString(" x")
			b.WriteByte(byte('0' + l.Repeats))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func TestCollapsesRunAndFlushesSummaryOnDifferentLine(t *testing.T) {
	s, got := collect(false)
	for i, r := range []string{"a", "a", "a", "b"} {
		s.Push(line(r, i))
	}
	if want := "a\n… repeated 2 more times\nb\n"; summary(*got) != want {
		t.Fatalf("got %q, want %q", summary(*got), want)
	}
	if l := (*got)[1]; l.Label != "a" || !l.Time.Equal(t0.Add(2*time.Second)) || l.JSON != nil {
		t.Fatalf("summary line %+v", l) // carries the time of the last repeat
	}
}

func TestFlushOnEnd(t *testing.T) {
	s, got := collect(false)
	s.Push(line("a", 0))
	s.Push(line("a", 1))
	if summary(*got) != "a\n" {
		t.Fatalf("summary emitted early: %q", summary(*got))
	}
	s.Flush()
	s.Flush() // idempotent
	if want := "a\n… repeated 1 more times\n"; summary(*got) != want {
		t.Fatalf("got %q, want %q", summary(*got), want)
	}
}

func TestNoRepeatsNoSummary(t *testing.T) {
	s, got := collect(false)
	for _, r := range []string{"a", "b", "a"} {
		s.Push(line(r, 0))
	}
	s.Flush()
	if want := "a\nb\na\n"; summary(*got) != want {
		t.Fatalf("got %q", summary(*got))
	}
}

func TestRunsAreIndependent(t *testing.T) {
	s, got := collect(false)
	for _, r := range []string{"a", "a", "b", "b", "b"} {
		s.Push(line(r, 0))
	}
	s.Flush()
	if want := "a\n… repeated 1 more times\nb\n… repeated 2 more times\n"; summary(*got) != want {
		t.Fatalf("got %q", summary(*got))
	}
}

func TestHoldEmitsFirstLineWithRepeats(t *testing.T) {
	s, got := collect(true)
	s.Push(line("a", 0))
	s.Push(line("a", 1))
	s.Push(line("a", 2))
	if len(*got) != 0 {
		t.Fatalf("emitted before the run ended: %q", summary(*got))
	}
	s.Push(line("b", 3))
	s.Flush()
	if want := "a x2\nb\n"; summary(*got) != want {
		t.Fatalf("got %q, want %q", summary(*got), want)
	}
	if !(*got)[0].Time.Equal(t0) {
		t.Fatalf("first line lost its time: %v", (*got)[0].Time)
	}
}

func TestCompareIgnoresTimestamp(t *testing.T) {
	// Parse strips the kubectl prefix, so equal text at different times is a repeat.
	s, got := collect(false)
	s.Push(parse.Parse("a", "2026-09-30T12:00:01.000000000Z same"))
	s.Push(parse.Parse("a", "2026-09-30T12:00:09.000000000Z same"))
	s.Flush()
	if want := "same\n… repeated 1 more times\n"; summary(*got) != want {
		t.Fatalf("got %q", summary(*got))
	}
}
