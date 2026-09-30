package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"klog/internal/testutil"
)

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// startTail runs `klog tail` in the background. stop cancels it (Ctrl-C) and returns the exit code.
func startTail(t *testing.T, args ...string) (out, errs *safeBuf, stop func() int) {
	t.Helper()
	out, errs = &safeBuf{}, &safeBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- execute(ctx, append([]string{"tail"}, args...), out, errs) }()
	var once sync.Once
	var code int
	stop = func() int {
		once.Do(func() {
			cancel()
			select {
			case code = <-done:
			case <-time.After(5 * time.Second):
				t.Error("tail did not exit after cancel")
				code = -1
			}
		})
		return code
	}
	t.Cleanup(func() { stop() })
	return out, errs, stop
}

var tailBase = []string{"-n", "shop", "-l", "app=web", "--poll", "50ms", "--format", "raw"}

func TestTailStreamsAndStopsOnCancel(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, stop := startTail(t, tailBase...)
	waitFor(t, "lines from both pods", func() bool {
		s := out.String()
		return strings.Contains(s, "a-one") && strings.Contains(s, "b-two")
	})
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

func TestTailPicksUpNewPods(t *testing.T) {
	f := setup(t)
	f.SetGet("pods", testutil.PodList(testutil.Pod{Name: "a-1", Containers: []string{"app"}}))
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, _ := startTail(t, tailBase...)
	waitFor(t, "a-1 lines", func() bool { return strings.Contains(out.String(), "a-one") })
	if strings.Contains(out.String(), "b-two") {
		t.Fatal("b-1 is not running yet")
	}
	f.SetGet("pods", testutil.PodList(
		testutil.Pod{Name: "a-1", Containers: []string{"app"}},
		testutil.Pod{Name: "b-1", Containers: []string{"app"}},
	))
	waitFor(t, "b-1 lines after it appears", func() bool { return strings.Contains(out.String(), "b-two") })
}

func TestTailRestartsStreamWhenContainerRestarts(t *testing.T) {
	f := setup(t)
	f.SetGet("pods", testutil.PodList(testutil.Pod{Name: "a-1", Containers: []string{"app"}}))
	// No Hang: the fake ends the stream right after the lines, like an exited container.
	out, _, _ := startTail(t, tailBase...)
	waitFor(t, "first instance lines", func() bool { return strings.Contains(out.String(), "a-one") })
	time.Sleep(300 * time.Millisecond) // several polls pass; the ended stream must NOT be re-read
	if n := strings.Count(out.String(), "a-one"); n != 1 {
		t.Fatalf("a-one printed %d times", n)
	}
	f.SetLogs("a-1", "app", b4+` {"level":"INFO","msg":"after-restart"}`+"\n")
	f.SetGet("pods", testutil.PodList(testutil.Pod{Name: "a-1", Containers: []string{"app"}, Restarts: 1}))
	waitFor(t, "lines after restart", func() bool { return strings.Contains(out.String(), "after-restart") })
}

func TestTailZeroPodsExits3(t *testing.T) {
	f := setup(t)
	f.SetGet("pods", testutil.PodList())
	out, errs := &safeBuf{}, &safeBuf{}
	code := execute(context.Background(), append([]string{"tail"}, tailBase...), out, errs)
	if code != 3 || !strings.Contains(errs.String(), "app=web") {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
}

func TestTailWaitKeepsPolling(t *testing.T) {
	f := setup(t)
	f.SetGet("pods", testutil.PodList())
	f.Hang("a-1")
	out, errs, stop := startTail(t, append([]string{"--wait"}, tailBase...)...)
	waitFor(t, "waiting notice", func() bool { return strings.Contains(errs.String(), "waiting") })
	f.SetGet("pods", testutil.PodList(testutil.Pod{Name: "a-1", Containers: []string{"app"}}))
	waitFor(t, "lines once the pod appears", func() bool { return strings.Contains(out.String(), "a-one") })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

func TestTailUsageErrorsNeverCallKubectl(t *testing.T) {
	f := setup(t)
	for name, args := range map[string][]string{
		"bad field":   {"-l", "a=b", "--field", "nope"},
		"bad poll":    {"-l", "a=b", "--poll", "0s"},
		"bad since":   {"-l", "a=b", "--since", "-1m"},
		"bad unit":    {"-l", "a=b", "--since", "2x"},
		"negative d":  {"-l", "a=b", "--since", "-2d"},
		"no target":   {},
		"bad exclude": {"-l", "a=b", "--exclude", "("},
	} {
		t.Run(name, func(t *testing.T) {
			out, errs := &safeBuf{}, &safeBuf{}
			if code := execute(context.Background(), append([]string{"tail"}, args...), out, errs); code != 2 {
				t.Fatalf("code %d, stderr %s", code, errs.String())
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}

func TestTailFirstResolveFailureExits1(t *testing.T) {
	setup(t)
	t.Setenv("KLOG_KUBECTL", "/nonexistent/kubectl")
	out, errs := &safeBuf{}, &safeBuf{}
	code := execute(context.Background(), append([]string{"tail"}, tailBase...), out, errs)
	if code != 1 || !strings.Contains(errs.String(), "hint:") {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
}
