package main

import (
	"context"
	"strings"
	"testing"
)

func ts(sec int) string {
	return "2026-09-30T12:00:" + string(rune('0'+sec/10)) + string(rune('0'+sec%10)) + ".000000000Z "
}

// logs renders kubectl output: one line per entry, seconds[i] is its timestamp.
func logs(seconds []int, lines ...string) string {
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(ts(seconds[i]) + l + "\n")
	}
	return b.String()
}

func TestFetchContextIsPerPod(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", logs([]int{1, 3, 5, 7}, "a-pre", "a-boom", "a-post", "a-far"))
	f.SetLogs("b-1", "app", logs([]int{2, 4, 6, 8}, "b-pre", "b-x", "b-boom", "b-post"))
	code, out, errs := klog(t, with("--format", "raw", "--grep", "boom", "-B", "1", "--after", "1")...)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := []string{"a-pre", "a-boom", "b-x", "a-post", "b-boom", "b-post"}
	if !equal(lines(out), want) {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestFetchContextAroundAndOverlap(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", logs([]int{1, 2, 3, 4, 5, 6, 7}, "x1", "boom 1", "x2", "boom 2", "x3", "x4", "x5"))
	f.SetLogs("b-1", "app", "")
	_, out, _ := klog(t, with("--format", "raw", "--grep", "boom", "-C", "1")...)
	want := []string{"x1", "boom 1", "x2", "boom 2", "x3"}
	if !equal(lines(out), want) {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestFetchContextKeepsStackTraceAttached(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", logs([]int{1, 2, 3, 4, 5}, "before", "boom", "\tat a.B(B.java:1)", "after", "later"))
	f.SetLogs("b-1", "app", "")
	_, out, _ := klog(t, with("--format", "raw", "--grep", "boom", "-A", "1")...)
	want := []string{"boom", "\tat a.B(B.java:1)", "after"}
	if !equal(strings.Split(strings.TrimRight(out, "\n"), "\n"), want) {
		t.Fatalf("got\n%s", out)
	}
}

func TestTailContext(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", logs([]int{1, 2, 3, 4}, "x1", "boom", "y1", "y2"))
	f.SetLogs("b-1", "app", "")
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, _ := startTail(t, append([]string{"--grep", "boom", "-B", "1", "-A", "1"}, tailBase...)...)
	waitFor(t, "context lines", func() bool { return strings.Contains(out.String(), "y1") })
	if !equal(lines(out.String()), []string{"x1", "boom", "y1"}) {
		t.Fatalf("got %q", out.String())
	}
}

func TestFetchFollowIDShowsLinesBeforeAndAfterSeedFromAnyPod(t *testing.T) {
	f := setup(t)
	a := []string{
		`{"level":"INFO","msg":"start","traceId":"t1"}`,
		`{"level":"INFO","msg":"noid"}`,
		`{"level":"ERROR","msg":"fail","traceId":"t1"}`,
		`plain text`,
	}
	b := []string{
		`{"level":"DEBUG","msg":"early","traceId":"t1"}`,
		`{"level":"INFO","msg":"other","traceId":"t9"}`,
		`{"level":"INFO","msg":"late","traceId":"t1"}`,
	}
	f.SetLogs("a-1", "app", logs([]int{1, 3, 5, 7}, a...))
	f.SetLogs("b-1", "app", logs([]int{2, 4, 6}, b...))
	code, out, errs := klog(t, with("--format", "raw", "--level", "ERROR", "--follow-id", "traceId")...)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := []string{a[0], b[0], a[2], b[2]}
	if !equal(lines(out), want) {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestFetchFollowIDWithContextAndStats(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", logs([]int{1, 2, 3}, `{"level":"INFO","msg":"x"}`, `{"level":"ERROR","msg":"boom","traceId":"t1"}`, `{"level":"INFO","msg":"y"}`))
	f.SetLogs("b-1", "app", logs([]int{4}, `{"level":"WARN","msg":"mate","traceId":"t1"}`))
	code, out, errs := klog(t, with("--format", "raw", "--grep", "boom", "-B", "1", "--follow-id", "traceId")...)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := []string{`{"level":"INFO","msg":"x"}`, `{"level":"ERROR","msg":"boom","traceId":"t1"}`, `{"level":"WARN","msg":"mate","traceId":"t1"}`}
	if !equal(lines(out), want) {
		t.Fatalf("got\n%s", out)
	}
	_, out, _ = klog(t, with("--format", "json", "--stats", "--level", "ERROR", "--follow-id", "traceId")...)
	if !strings.Contains(out, `"source":"a-1"`) || !strings.Contains(out, `"source":"b-1"`) || !strings.Contains(out, `"WARN":1`) {
		t.Fatalf("stats should count followed lines, got\n%s", out)
	}
}

func TestTailFollowIDIsForwardOnly(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", logs([]int{1, 2, 3, 4},
		`{"level":"INFO","msg":"early","traceId":"t1"}`,
		`{"level":"ERROR","msg":"seed","traceId":"t1"}`,
		`{"level":"INFO","msg":"late","traceId":"t1"}`,
		`{"level":"INFO","msg":"other","traceId":"t2"}`))
	f.SetLogs("b-1", "app", "")
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, _ := startTail(t, append([]string{"--level", "ERROR", "--follow-id", "traceId"}, tailBase...)...)
	waitFor(t, "followed line", func() bool { return strings.Contains(out.String(), "late") })
	got := out.String()
	if !strings.Contains(got, "seed") || strings.Contains(got, "early") || strings.Contains(got, "other") {
		t.Fatalf("got %q", got)
	}
}

func TestIncidentUsageErrorsNeverCallKubectl(t *testing.T) {
	f := setup(t)
	cases := map[string][]string{
		"after without grep":  {"-A", "2"},
		"before without grep": {"--before", "2"},
		"around without grep": {"-C", "1"},
		"negative":            {"--grep", "x", "-A", "-1"},
	}
	for name, extra := range cases {
		t.Run("fetch "+name, func(t *testing.T) {
			if code, _, errs := klog(t, with(extra...)...); code != 2 {
				t.Fatalf("code %d, stderr %s", code, errs)
			}
		})
		t.Run("tail "+name, func(t *testing.T) {
			out, errs := &safeBuf{}, &safeBuf{}
			if code := execute(context.Background(), append([]string{"tail"}, append(extra, tailBase...)...), out, errs); code != 2 {
				t.Fatalf("code %d, stderr %s", code, errs.String())
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}
