package main

import (
	"context"
	"strings"
	"testing"
)

func TestFetchStatsTable(t *testing.T) {
	setup(t)
	code, out, errs := klog(t, with("--stats")...)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	want := []string{
		"SOURCE  TRACE  DEBUG  INFO  WARN  ERROR  FATAL  ?  TOTAL",
		"a-1     0      0      1     0     1      0      0  2",
		"b-1     0      0      0     1     0      0      1  2",
		"TOTAL   0      0      1     1     1      0      1  4",
	}
	got := lines(out)
	for i := range got {
		got[i] = strings.TrimRight(got[i], " ")
	}
	if !equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestFetchStatsAfterFilters(t *testing.T) {
	setup(t)
	_, out, _ := klog(t, with("--stats", "--level", "WARN", "--format", "json")...)
	want := []string{
		`{"source":"a-1","counts":{"?":0,"DEBUG":0,"ERROR":1,"FATAL":0,"INFO":0,"TRACE":0,"WARN":0},"total":1}`,
		`{"source":"b-1","counts":{"?":0,"DEBUG":0,"ERROR":0,"FATAL":0,"INFO":0,"TRACE":0,"WARN":1},"total":1}`,
	}
	if got := lines(out); !equal(got, want) {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestTailRejectsStats(t *testing.T) {
	f := setup(t)
	out, errs := &safeBuf{}, &safeBuf{}
	if code := execute(context.Background(), append([]string{"tail", "--stats"}, tailBase...), out, errs); code != 2 {
		t.Fatalf("code %d, stderr %s", code, errs.String())
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}
