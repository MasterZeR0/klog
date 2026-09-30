package run

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"klog/internal/testutil"
)

const (
	ts1 = "2026-09-30T12:00:01.000000000Z"
	ts2 = "2026-09-30T12:00:02.000000000Z"
)

var web = Stream{Pod: "web-1", Container: "app", Label: "web-1"}

func runner(f *testutil.Fake, o Opts) (Runner, *[]string) {
	var notes []string
	return Runner{
		K:       Kubectl{Bin: f.Bin},
		Opts:    o,
		Backoff: func(int) time.Duration { return 0 },
		Notify:  func(m string) { notes = append(notes, m) },
	}, &notes
}

func collect() (func(Raw), *[]string) {
	var got []string
	return func(r Raw) { got = append(got, r.Text) }, &got
}

func TestRunEmitsLinesInOrder(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetLogs("web-1", "app", ts1+" one\n"+ts2+" two\r\n")
	r, _ := runner(f, Opts{Namespace: "shop", Since: time.Hour})
	emit, got := collect()
	if err := r.Run(context.Background(), web, emit); err != nil {
		t.Fatal(err)
	}
	want := []string{ts1 + " one", ts2 + " two"}
	if strings.Join(*got, "|") != strings.Join(want, "|") {
		t.Fatalf("got %q, want %q", *got, want)
	}
	if c := f.Calls(); len(c) != 1 || c[0] != "logs -n shop web-1 -c app --timestamps --since=1h0m0s" {
		t.Fatalf("calls = %q", c)
	}
}

func TestRunLongLineAndCRLF(t *testing.T) {
	f := testutil.NewFake(t)
	long := strings.Repeat("x", 200_000)
	f.SetLogs("web-1", "app", ts1+" "+long+"\r\n"+ts2+" after\r\n")
	r, _ := runner(f, Opts{})
	emit, got := collect()
	if err := r.Run(context.Background(), web, emit); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 || (*got)[0] != ts1+" "+long || (*got)[1] != ts2+" after" {
		t.Fatalf("long line mangled: %d lines", len(*got))
	}
}

func TestRunFetchFailureReturnsKubectlError(t *testing.T) {
	f := testutil.NewFake(t)
	f.FailTimes("web-1", 1)
	r, notes := runner(f, Opts{})
	emit, _ := collect()
	err := r.Run(context.Background(), web, emit)
	var ke *KubectlError
	if !errors.As(err, &ke) || ke.Stderr != "boom" {
		t.Fatalf("err = %v", err)
	}
	if len(*notes) != 1 || (*notes)[0] != "[web-1] stream ended: kubectl: boom" {
		t.Fatalf("notes = %q", *notes)
	}
}

func TestRunFollowRetriesWithoutDuplicates(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetLogs("web-1", "app", ts1+" one\n"+ts2+" two\n")
	f.FailTimes("web-1", 2) // two failed attempts, each replays both lines
	r, notes := runner(f, Opts{Follow: true, Since: time.Minute})
	emit, got := collect()
	if err := r.Run(context.Background(), web, emit); err != nil {
		t.Fatal(err)
	}
	if len(*got) != 2 {
		t.Fatalf("want 2 lines without duplicates, got %q", *got)
	}
	calls := f.Calls()
	if len(calls) != 3 || !strings.Contains(calls[1], "--since-time=2026-09-30T12:00:02Z") {
		t.Fatalf("calls = %q", calls)
	}
	retries := 0
	for _, n := range *notes {
		if strings.Contains(n, "retry") {
			retries++
		}
	}
	if retries != 2 {
		t.Fatalf("notes = %q", *notes)
	}
}

func TestRunFollowGivesUp(t *testing.T) {
	f := testutil.NewFake(t)
	f.FailTimes("web-1", 99)
	r, notes := runner(f, Opts{Follow: true})
	emit, _ := collect()
	if err := r.Run(context.Background(), web, emit); err == nil {
		t.Fatal("expected an error")
	}
	if n := len(f.Calls()); n != 4 {
		t.Fatalf("want 1 try + 3 retries, got %d calls", n)
	}
	if last := (*notes)[len(*notes)-1]; !strings.Contains(last, "giving up") {
		t.Fatalf("last note = %q", last)
	}
}

func TestRunCancelKillsKubectl(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetLogs("web-1", "app", ts1+" one\n")
	f.Hang("web-1")
	r, _ := runner(f, Opts{Follow: true})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, web, func(Raw) { cancel() }) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancel should end cleanly, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunSlowConsumerLosesNothing(t *testing.T) {
	f := testutil.NewFake(t)
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "%s line-%03d\n", ts1, i)
	}
	f.SetLogs("web-1", "app", b.String())
	r, _ := runner(f, Opts{})
	var got []string
	err := r.Run(context.Background(), web, func(x Raw) {
		time.Sleep(time.Millisecond)
		got = append(got, x.Text)
	})
	if err != nil || len(got) != 300 || got[299] != ts1+" line-299" {
		t.Fatalf("err=%v len=%d", err, len(got))
	}
}

func TestOutputMissingBinary(t *testing.T) {
	_, err := Kubectl{Bin: "/nonexistent/kubectl"}.Output(context.Background(), "get", "pods")
	var ke *KubectlError
	if !errors.As(err, &ke) {
		t.Fatalf("err = %v", err)
	}
}
