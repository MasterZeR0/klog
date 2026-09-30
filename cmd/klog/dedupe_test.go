package main

import (
	"strings"
	"testing"
	"time"

	"klog/internal/testutil"
)

const (
	d1 = "2026-09-30T12:00:01.000000000Z"
	d2 = "2026-09-30T12:00:02.000000000Z"
	d3 = "2026-09-30T12:00:03.000000000Z"
	d4 = "2026-09-30T12:00:04.000000000Z"
	d5 = "2026-09-30T12:00:05.000000000Z"
)

// setupDupes gives a-1 a run of three identical lines then a different one,
// and b-1 a run that only ends with the stream.
func setupDupes(t *testing.T) *testutil.Fake {
	f := setup(t)
	f.SetLogs("a-1", "app", d1+" retry\n"+d2+" retry\n"+d3+" retry\n"+d4+" done\n")
	f.SetLogs("b-1", "app", d2+" ping\n"+d3+" ping\n"+d5+" ping\n")
	return f
}

func TestFetchDedupeRaw(t *testing.T) {
	setupDupes(t)
	code, out, errs := klog(t, with("--format", "raw", "--dedupe")...)
	want := []string{
		"retry",
		"ping",
		"… repeated 2 more times", // a-1, time of its last repeat (12:00:03), sorts before b-1's
		"done",
		"… repeated 2 more times", // b-1, flushed when the stream ends
	}
	if code != 0 || !equal(lines(out), want) {
		t.Fatalf("code %d, stderr %s, got\n%s\nwant\n%v", code, errs, out, want)
	}
}

func TestFetchDedupePretty(t *testing.T) {
	setupDupes(t)
	_, out, _ := klog(t, with("--dedupe", "--grep", "retry|done")...)
	want := []string{
		"[a-1] 12:00:01.000 retry",
		"[a-1] 12:00:03.000 … repeated 2 more times",
		"[a-1] 12:00:04.000 done",
	}
	if !equal(lines(out), want) {
		t.Fatalf("got\n%s", out)
	}
}

func TestFetchDedupeJSONAddsRepeats(t *testing.T) {
	setupDupes(t)
	_, out, _ := klog(t, with("--format", "json", "--dedupe", "--grep", "retry|done")...)
	ls := lines(out)
	if len(ls) != 2 || !strings.Contains(ls[0], `"raw":"retry"`) || !strings.Contains(ls[0], `"repeats":2`) ||
		strings.Contains(ls[1], "repeats") {
		t.Fatalf("got\n%s", out)
	}
}

func TestFetchDedupeTemplateRepeats(t *testing.T) {
	setupDupes(t)
	_, out, _ := klog(t, with("--format", "template", "--template", "{{.Raw}} x{{.Repeats}}", "--dedupe", "--grep", "ping")...)
	if want := []string{"ping x2"}; !equal(lines(out), want) {
		t.Fatalf("got\n%s", out)
	}
}

func TestFetchWithoutDedupeKeepsEverything(t *testing.T) {
	setupDupes(t)
	_, out, _ := klog(t, with("--format", "raw", "--grep", "retry|done")...)
	if n := len(lines(out)); n != 4 {
		t.Fatalf("got %d lines\n%s", n, out)
	}
}

const summary2 = "… repeated 2 more times"

func TestTailDedupeFlushesSummaryOnStreamEnd(t *testing.T) {
	setupDupes(t) // no Hang: the fake ends both streams after their lines
	out, _, _ := startTail(t, append([]string{"--dedupe"}, tailBase...)...)
	waitFor(t, "both summaries", func() bool { return strings.Count(out.String(), summary2) == 2 })
	if n := strings.Count(out.String(), "retry\n"); n != 1 {
		t.Fatalf("retry printed %d times:\n%s", n, out.String())
	}
}

func TestTailDedupeFlushesPendingSummaryOnCancel(t *testing.T) {
	setupDupes(t).Hang("b-1") // b-1's run is still open when we stop; a-1's ends with "done"
	out, _, stop := startTail(t, append([]string{"--dedupe"}, tailBase...)...)
	waitFor(t, "a-1's summary", func() bool { return strings.Contains(out.String(), "done") })
	waitFor(t, "b-1's first line", func() bool { return strings.Contains(out.String(), "ping") })
	time.Sleep(300 * time.Millisecond) // let b-1's two repeats be read
	if n := strings.Count(out.String(), summary2); n != 1 {
		t.Fatalf("want only a-1's summary before cancel, got %d:\n%s", n, out.String())
	}
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if n := strings.Count(out.String(), summary2); n != 2 {
		t.Fatalf("b-1's summary was not flushed on cancel:\n%s", out.String())
	}
}
