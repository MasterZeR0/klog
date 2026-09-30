package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"klog/internal/testutil"
)

const (
	a1 = "2026-09-30T12:00:01.000000000Z"
	b2 = "2026-09-30T12:00:02.000000000Z"
	a3 = "2026-09-30T12:00:03.000000000Z"
	b4 = "2026-09-30T12:00:04.000000000Z"
)

const (
	aOne   = `{"level":"INFO","msg":"a-one"}`
	bTwo   = `{"level":"WARN","msg":"b-two","requestId":"r2"}`
	aThree = `{"level":"ERROR","msg":"a-three","requestId":"r1","orderId":12345678901234567890}`
	bFour  = `plain b-four`
)

// setup installs a fake kubectl with pods a-1 and b-1 (label app=web).
func setup(t *testing.T) *testutil.Fake {
	t.Helper()
	// os.UserConfigDir reads these; keep the developer's real theme out of tests.
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "")
	f := testutil.NewFake(t)
	t.Setenv("KLOG_KUBECTL", f.Bin)
	f.SetGet("pods", testutil.PodList(
		testutil.Pod{Name: "a-1", Containers: []string{"app"}},
		testutil.Pod{Name: "b-1", Containers: []string{"app"}},
	))
	f.SetLogs("a-1", "app", a1+" "+aOne+"\n"+a3+" "+aThree+"\n")
	f.SetLogs("b-1", "app", b2+" "+bTwo+"\n"+b4+" "+bFour+"\n")
	return f
}

func klog(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = execute(context.Background(), args, &out, &errb)
	return code, out.String(), errb.String()
}

var base = []string{"fetch", "-n", "shop", "-l", "app=web", "--since-time", "2026-09-30T12:00:01Z"}

func with(extra ...string) []string { return append(append([]string{}, base...), extra...) }

func lines(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func equal(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

func TestFetchMergesByTimestamp(t *testing.T) {
	setup(t)
	code, out, _ := klog(t, with("--format", "raw")...)
	if code != 0 {
		t.Fatalf("code %d", code)
	}
	if want := []string{aOne, bTwo, aThree, bFour}; !equal(lines(out), want) {
		t.Fatalf("got\n%s\nwant\n%s", out, strings.Join(want, "\n"))
	}
}

func TestFetchPrettyShowsLabelAndTime(t *testing.T) {
	setup(t)
	_, out, _ := klog(t, with("--level", "error")...)
	if want := "[a-1] 12:00:03.000 ERROR a-three  orderId=12345678901234567890 requestId=r1"; strings.TrimSpace(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
}

func TestFetchFilters(t *testing.T) {
	setup(t)
	tests := []struct {
		name string
		args []string
		want []string
	}{
		{"level", []string{"--level", "WARN"}, []string{bTwo, aThree}},
		{"field eq", []string{"--field", "requestId=r2"}, []string{bTwo}},
		{"field ne", []string{"--field", "requestId!=r2"}, []string{aOne, aThree}},
		{"field regex", []string{"--field", "msg~^a-"}, []string{aOne, aThree}},
		{"big number field", []string{"--field", "orderId=12345678901234567890"}, []string{aThree}},
		{"grep", []string{"--grep", "b-"}, []string{bTwo, bFour}},
		{"exclude", []string{"--exclude", "a-|b-two"}, []string{bFour}},
		{"until", []string{"--until", "2026-09-30T12:00:02Z"}, []string{aOne, bTwo}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, out, _ := klog(t, with(append(tc.args, "--format", "raw")...)...)
			if code != 0 || !equal(lines(out), tc.want) {
				t.Fatalf("code %d, got\n%s\nwant %v", code, out, tc.want)
			}
		})
	}
}

func TestFetchOutFileIsAtomic(t *testing.T) {
	setup(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.txt")
	code, out, _ := klog(t, with("--format", "raw", "--out", path)...)
	if code != 0 || out != "" {
		t.Fatalf("code %d, stdout %q", code, out)
	}
	b, err := os.ReadFile(path)
	if err != nil || !equal(lines(string(b)), []string{aOne, bTwo, aThree, bFour}) {
		t.Fatalf("file = %q, err %v", b, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("leftover files in %s: %v", dir, entries)
	}
}

func TestFetchInterruptedLeavesNoFile(t *testing.T) {
	f := setup(t)
	f.Hang("b-1")
	dir := t.TempDir()
	path := filepath.Join(dir, "logs.txt")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	var out, errb bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- execute(ctx, with("--out", path), &out, &errb) }()
	select {
	case code := <-done:
		if code != 1 {
			t.Fatalf("code %d, stderr %s", code, errb.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetch did not stop on cancel")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("expected an empty dir, got %v", entries)
	}
}

func TestFetchUsageErrorsNeverCallKubectl(t *testing.T) {
	f := setup(t)
	cases := map[string][]string{
		"bad grep regex":     with("--grep", "("),
		"bad exclude regex":  with("--exclude", "("),
		"bad container":      with("-c", "("),
		"bad pod regex":      {"fetch", "-p", "(", "--since", "1h"},
		"bad field":          with("--field", "nope"),
		"bad level":          with("--level", "LOUD"),
		"bad format":         with("--format", "xml"),
		"no target":          {"fetch", "--since", "1h"},
		"two targets":        {"fetch", "-l", "a=b", "-d", "web", "--since", "1h"},
		"no since":           {"fetch", "-l", "a=b"},
		"both since":         {"fetch", "-l", "a=b", "--since", "1h", "--since-time", "2026-01-01T00:00:00Z"},
		"bad since unit":     {"fetch", "-l", "a=b", "--since", "2x"},
		"bad until unit":     with("--until", "2x"),
		"bad since-time":     {"fetch", "-l", "a=b", "--since-time", "yesterday"},
		"until before since": with("--until", "2026-01-01T00:00:00Z"),
		"stray argument":     with("oops"),
		"unknown flag":       with("--nope"),
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, errs := klog(t, args...)
			if code != 2 {
				t.Fatalf("code %d, stderr %s", code, errs)
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}

func TestFetchNoPodsExits3(t *testing.T) {
	f := setup(t)
	f.SetGet("pods", testutil.PodList())
	code, _, errs := klog(t, with()...)
	if code != 3 || !strings.Contains(errs, "app=web") {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
}

func TestFetchKubectlMissingExits1WithHint(t *testing.T) {
	setup(t)
	t.Setenv("KLOG_KUBECTL", "/nonexistent/kubectl")
	code, _, errs := klog(t, with()...)
	if code != 1 || !strings.Contains(errs, "hint:") {
		t.Fatalf("code %d, stderr %q", code, errs)
	}
}

func TestFetchOneStreamFailsOthersContinue(t *testing.T) {
	f := setup(t)
	f.FailTimes("b-1", 5)
	code, out, errs := klog(t, with("--format", "raw")...)
	if code != 0 {
		t.Fatalf("code %d, stderr %s", code, errs)
	}
	if !strings.Contains(out, "a-one") || !strings.Contains(errs, "[b-1] stream ended: kubectl: boom") {
		t.Fatalf("stdout %q, stderr %q", out, errs)
	}
}

func TestFetchAllStreamsFailExits1(t *testing.T) {
	f := setup(t)
	f.FailTimes("a-1", 5)
	f.FailTimes("b-1", 5)
	if code, _, _ := klog(t, with()...); code != 1 {
		t.Fatalf("code %d", code)
	}
}

func TestFetchRetentionGapWarning(t *testing.T) {
	setup(t)
	// Earliest line of a-1 (12:00:01) is later than --since-time 11:00:00.
	_, _, errs := klog(t, "fetch", "-n", "shop", "-l", "app=web", "--since-time", "2026-09-30T11:00:00Z")
	if !strings.Contains(errs, "earliest line for [a-1]") {
		t.Fatalf("stderr %q", errs)
	}
	// Both times are UTC even when --since-time was given with an offset.
	_, _, errs = klog(t, "fetch", "-n", "shop", "-l", "app=web", "--since-time", "2026-09-30T13:00:00+02:00")
	if !strings.Contains(errs, "is 2026-09-30T12:00:01Z, later than the requested start 2026-09-30T11:00:00Z;") {
		t.Fatalf("stderr %q", errs)
	}
	// since-time at or after the first line: no warning for a-1.
	_, _, errs = klog(t, "fetch", "-n", "shop", "-p", "^a-", "--since-time", "2026-09-30T12:00:02Z")
	if strings.Contains(errs, "earliest line") {
		t.Fatalf("unexpected warning: %q", errs)
	}
}

func TestFetchJSONFormat(t *testing.T) {
	setup(t)
	_, out, _ := klog(t, with("--format", "json", "--field", "requestId=r2")...)
	if !strings.Contains(out, `"source":"b-1"`) || !strings.Contains(out, `"time":"2026-09-30T12:00:02Z"`) {
		t.Fatalf("got %q", out)
	}
}
