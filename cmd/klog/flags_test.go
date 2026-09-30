package main

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	good := map[string]time.Duration{
		"30m":    30 * time.Minute,
		"48h":    48 * time.Hour,
		"2d":     48 * time.Hour,
		"7d":     7 * 24 * time.Hour,
		"1d12h":  36 * time.Hour,
		"1d2h3m": 26*time.Hour + 3*time.Minute,
		"1.5d":   36 * time.Hour,
		"-2d":    -48 * time.Hour,
		"0":      0,
		"250ms":  250 * time.Millisecond,
	}
	for in, want := range good {
		if got, err := parseDuration(in); err != nil || got != want {
			t.Errorf("parseDuration(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "d", "2dd", "2x", "1d 2h", "abc", "2d3"} {
		_, err := parseDuration(bad)
		if err == nil || !strings.Contains(err.Error(), "2d") {
			t.Errorf("parseDuration(%q) error = %v; want a hint mentioning d", bad, err)
		}
	}
}

func TestDurationFlag(t *testing.T) {
	fs := newFlagSet("x", io.Discard)
	since := durationVar(fs, "since", 5*time.Minute, "")
	if err := fs.Parse(nil); err != nil || *since != 5*time.Minute {
		t.Fatalf("default: %v, %v", *since, err)
	}
	if err := fs.Parse([]string{"--since", "1d12h"}); err != nil || *since != 36*time.Hour {
		t.Fatalf("got %v, %v", *since, err)
	}
	if err := fs.Parse([]string{"--since", "soon"}); err == nil {
		t.Fatal("bad duration should fail to parse")
	}
}

func TestParseWhen(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	good := map[string]time.Time{
		"2026-09-30T11:00:00Z": now.Add(-time.Hour),
		"30m":                  now.Add(-30 * time.Minute),
		"2d":                   now.Add(-48 * time.Hour),
		"1d12h":                now.Add(-36 * time.Hour),
	}
	for in, want := range good {
		if got, err := parseWhen(in, now); err != nil || !got.Equal(want) {
			t.Errorf("parseWhen(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	for _, bad := range []string{"yesterday", "-1h", "-2d", ""} {
		if _, err := parseWhen(bad, now); err == nil {
			t.Errorf("parseWhen(%q) should fail", bad)
		}
	}
}
