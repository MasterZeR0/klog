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
