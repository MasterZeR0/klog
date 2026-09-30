package parse

import (
	"encoding/json"
	"testing"
	"time"
)

const stamp = "2026-09-30T12:00:00.123456789Z"

var stampTime = time.Date(2026, 9, 30, 12, 0, 0, 123456789, time.UTC)

func TestParse(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		wantTime time.Time
		wantRaw  string
		wantJSON bool
	}{
		{"json with timestamp", stamp + ` {"level":"INFO","msg":"hi"}`, stampTime, `{"level":"INFO","msg":"hi"}`, true},
		{"raw with timestamp", stamp + " plain text", stampTime, "plain text", false},
		{"no timestamp", `{"a":1}`, time.Time{}, `{"a":1}`, true},
		{"timestamp only", stamp, stampTime, "", false},
		{"timestamp and empty message", stamp + " ", stampTime, "", false},
		{"invalid json", stamp + " {not json", stampTime, "{not json", false},
		{"trailing garbage after object", stamp + ` {"a":1} tail`, stampTime, `{"a":1} tail`, false},
		{"json array is not an object", stamp + " [1,2]", stampTime, "[1,2]", false},
		{"empty object", stamp + " {}", stampTime, "{}", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse("pod-a", tc.in)
			if got.Label != "pod-a" {
				t.Errorf("Label = %q", got.Label)
			}
			if !got.Time.Equal(tc.wantTime) {
				t.Errorf("Time = %v, want %v", got.Time, tc.wantTime)
			}
			if got.Raw != tc.wantRaw {
				t.Errorf("Raw = %q, want %q", got.Raw, tc.wantRaw)
			}
			if (got.JSON != nil) != tc.wantJSON {
				t.Errorf("JSON != nil is %v, want %v", got.JSON != nil, tc.wantJSON)
			}
		})
	}
}

func TestParseKeepsBigNumbers(t *testing.T) {
	got := Parse("p", stamp+` {"orderId":12345678901234567890}`)
	n, ok := got.JSON["orderId"].(json.Number)
	if !ok || n.String() != "12345678901234567890" {
		t.Fatalf("orderId = %#v, want json.Number 12345678901234567890", got.JSON["orderId"])
	}
}

func TestSplitTimestamp(t *testing.T) {
	ts, rest := SplitTimestamp(stamp + " hello world")
	if !ts.Equal(stampTime) || rest != "hello world" {
		t.Fatalf("got %v, %q", ts, rest)
	}
	ts, rest = SplitTimestamp("no prefix here")
	if !ts.IsZero() || rest != "no prefix here" {
		t.Fatalf("got %v, %q", ts, rest)
	}
}
