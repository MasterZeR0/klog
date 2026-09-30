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

func TestDedupeRejectsRawFormat(t *testing.T) {
	f := setupDupes(t)
	for _, cmd := range [][]string{with("--format", "raw", "--dedupe"), append([]string{"tail", "--dedupe"}, tailBase...)} {
		code, out, errs := klog(t, cmd...)
		if code != 2 || out != "" || !strings.Contains(errs, "--dedupe needs --format pretty, json or template") {
			t.Fatalf("%s: code %d, stdout %q, stderr %q", cmd[0], code, out, errs)
		}
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}

func TestFetchStatsRejectsDedupe(t *testing.T) {
	f := setupDupes(t)
	code, out, errs := klog(t, with("--stats", "--dedupe")...)
	if code != 2 || out != "" || !strings.Contains(errs, "--stats cannot be combined with --dedupe") {
		t.Fatalf("code %d, stdout %q, stderr %q", code, out, errs)
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
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

func TestFetchDedupeJSONAddsRepeatRecord(t *testing.T) {
	setupDupes(t)
	_, out, _ := klog(t, with("--format", "json", "--dedupe", "--grep", "retry|done")...)
	ls := lines(out)
	// the first occurrence, a record for the two repeats (time of the last), then the next line
	if len(ls) != 3 || !strings.Contains(ls[0], `"raw":"retry"`) || strings.Contains(ls[0], "repeats") ||
		!strings.Contains(ls[1], `"raw":"retry"`) || !strings.Contains(ls[1], `"repeats":2`) || !strings.Contains(ls[1], "12:00:03") ||
		!strings.Contains(ls[2], `"raw":"done"`) || strings.Contains(ls[2], "repeats") {
		t.Fatalf("got\n%s", out)
	}
}

func TestFetchDedupeTemplateRepeats(t *testing.T) {
	setupDupes(t)
	_, out, _ := klog(t, with("--format", "template", "--template", "{{.Raw}} x{{.Repeats}}", "--dedupe", "--grep", "ping")...)
	if want := []string{"ping x0", "ping x2"}; !equal(lines(out), want) {
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

// tailDedupe is tailBase with pretty output, which --dedupe needs.
var tailDedupe = []string{"-n", "shop", "-l", "app=web", "--poll", "50ms", "--dedupe", "--format", "pretty"}

func TestTailDedupeFlushesSummaryOnStreamEnd(t *testing.T) {
	setupDupes(t) // no Hang: the fake ends both streams after their lines
	out, _, _ := startTail(t, tailDedupe...)
	waitFor(t, "both summaries", func() bool { return strings.Count(out.String(), summary2) == 2 })
	if n := strings.Count(out.String(), "retry\n"); n != 1 {
		t.Fatalf("retry printed %d times:\n%s", n, out.String())
	}
}

func TestTailDedupeFlushesPendingSummaryOnCancel(t *testing.T) {
	setupDupes(t).Hang("b-1") // b-1's run is still open when we stop; a-1's ends with "done"
	out, _, stop := startTail(t, tailDedupe...)
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

// A lone "connection lost" followed by silence must show at once in json
// output, and the repeat record of a still-open run arrives when tail is stopped.
func TestTailDedupeJSONShowsFirstLineAtOnce(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", d1+" connection lost\n")
	f.SetLogs("b-1", "app", d2+" ping\n"+d3+" ping\n"+d5+" ping\n")
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, stop := startTail(t, "-n", "shop", "-l", "app=web", "--poll", "50ms", "--dedupe", "--format", "json")
	waitFor(t, "both first lines", func() bool {
		return strings.Contains(out.String(), "connection lost") && strings.Contains(out.String(), `"raw":"ping"`)
	})
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(out.String(), "repeats") {
		t.Fatalf("repeat record emitted before the run ended:\n%s", out.String())
	}
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if n := strings.Count(out.String(), `"repeats":2`); n != 1 || strings.Count(out.String(), "connection lost") != 1 {
		t.Fatalf("got\n%s", out.String())
	}
}
