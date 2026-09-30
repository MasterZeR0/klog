package filter

import (
	"regexp"
	"strings"
	"testing"

	"klog/internal/parse"
)

// feed pushes raw lines through a Context and returns everything emitted.
func feed(c *Context, raws ...string) []string {
	var out []string
	for _, r := range raws {
		for _, l := range c.Feed(parse.Parse("p", r)) {
			out = append(out, l.Raw)
		}
	}
	return out
}

func grepCtx(expr string, before, after int) *Context {
	return NewContext(New(Config{Grep: regexp.MustCompile(expr)}), before, after)
}

func TestContextBeforeAndAfter(t *testing.T) {
	c := grepCtx("HIT", 2, 1)
	got := feed(c, "1", "2", "3", "HIT a", "4", "5", "6", "7", "HIT b", "8")
	want := "2 3 HIT a 4 6 7 HIT b 8" // 5 is neither after "HIT a" nor within 2 of "HIT b"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestContextOverlapEmitsEachLineOnce(t *testing.T) {
	c := grepCtx("HIT", 2, 2)
	got := feed(c, "1", "HIT a", "2", "HIT b", "3", "4", "5")
	want := "1 HIT a 2 HIT b 3 4"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, " "), want)
	}
}

func TestContextZeroIsPlainFilter(t *testing.T) {
	got := feed(grepCtx("HIT", 0, 0), "1", "HIT a", "2")
	if strings.Join(got, " ") != "HIT a" {
		t.Fatalf("got %q", got)
	}
}

func TestContextKeepsStackTraceAttached(t *testing.T) {
	c := grepCtx("boom", 1, 1)
	got := feed(c, "before", "boom happened", "\tat a.B(B.java:1)", "\tat c.D(D.java:2)", "after1", "after2")
	// the trace lines are matches (they follow a kept line); after1 is the one after-context line
	want := "before|boom happened|\tat a.B(B.java:1)|\tat c.D(D.java:2)|after1"
	if strings.Join(got, "|") != want {
		t.Fatalf("got %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestContextBeforeExcludesUnmatchedTrace(t *testing.T) {
	// A trace under a non-matching line is not a match; it is ordinary context material.
	c := grepCtx("HIT", 2, 0)
	got := feed(c, "x", "\tat frame", "HIT")
	if strings.Join(got, "|") != "x|\tat frame|HIT" {
		t.Fatalf("got %q", got)
	}
}

func TestContextPerStreamState(t *testing.T) {
	a, b := grepCtx("HIT", 1, 1), grepCtx("HIT", 1, 1)
	got := feed(a, "a1")
	got = append(got, feed(b, "HIT b")...)
	got = append(got, feed(a, "a2")...) // a has no match: b's hit must not leak context into a
	if strings.Join(got, "|") != "HIT b" {
		t.Fatalf("got %q", got)
	}
}

// Context counts log records: a head line plus its frames travel together.
var traceThenPino = []string{
	"java.lang.Exception: x",
	"   at Foo.bar(Foo.java:1)",
	"   at Foo.baz(Foo.java:2)",
	`{"level":30,"msg":"pino info"}`,
	`{"level":30,"msg":"pino35"}`,
	`{"level":100,"msg":"pino100"}`,
	`{"level":50,"msg":"pino err"}`,
}

func TestContextBeforeCountsRecordsNotLines(t *testing.T) {
	for _, tc := range []struct {
		before int
		want   []string // records kept before "pino err"
	}{
		{1, traceThenPino[5:6]},
		{2, traceThenPino[4:6]},
		{4, traceThenPino[0:6]}, // the 3-line trace is one record: whole, never orphaned
	} {
		got := feed(grepCtx("pino err", tc.before, 0), traceThenPino...)
		want := append(append([]string{}, tc.want...), traceThenPino[6])
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Fatalf("-B %d: got %q, want %q", tc.before, got, want)
		}
	}
}

func TestContextBeforeWholeTraceOrNothing(t *testing.T) {
	// -B 1 over a trace record right before the match keeps all of its frames.
	got := feed(grepCtx("HIT", 1, 0), "old", "Exception: x", "  at a", "  at b", "HIT")
	if want := "Exception: x|  at a|  at b|HIT"; strings.Join(got, "|") != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestContextAfterCountsRecordsAndKeepsFrames(t *testing.T) {
	got := feed(grepCtx("HIT", 0, 1), "HIT", "Exception: x", "  at a", "  at b", "next", "  at c")
	if want := "HIT|Exception: x|  at a|  at b"; strings.Join(got, "|") != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestContextJSONThenIndentedText(t *testing.T) {
	// An indented line after a JSON line is a continuation of that record.
	got := feed(grepCtx("HIT", 1, 0), `{"msg":"j"}`, "  indented", "plain", "HIT")
	if want := "plain|HIT"; strings.Join(got, "|") != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	got = feed(grepCtx("HIT", 1, 0), `{"msg":"j"}`, "  indented", "HIT")
	if want := `{"msg":"j"}|  indented|HIT`; strings.Join(got, "|") != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
