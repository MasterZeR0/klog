// Package run spawns kubectl and streams its log lines.
package run

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"klog/internal/parse"
)

// Kubectl is an injectable kubectl invocation.
type Kubectl struct {
	Bin     string // "" means "kubectl"
	Context string // "" means the current kubeconfig context
}

// KubectlError is a failed kubectl call. Stderr is its last stderr line.
type KubectlError struct {
	Stderr string
	Err    error
}

func (e *KubectlError) Error() string {
	if e.Stderr != "" {
		return "kubectl: " + e.Stderr
	}
	return "kubectl: " + e.Err.Error()
}

func (e *KubectlError) Unwrap() error { return e.Err }

func lastLine(b []byte) string {
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (k Kubectl) cmd(ctx context.Context, args []string) *exec.Cmd {
	bin := k.Bin
	if bin == "" {
		bin = "kubectl"
	}
	if k.Context != "" {
		args = append([]string{"--context", k.Context}, args...)
	}
	c := exec.CommandContext(ctx, bin, args...)
	c.WaitDelay = time.Second // do not hang on pipes held by orphaned children
	return c
}

// Output runs kubectl and returns its stdout.
func (k Kubectl) Output(ctx context.Context, args ...string) ([]byte, error) {
	c := k.cmd(ctx, args)
	var stderr bytes.Buffer
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &KubectlError{Stderr: lastLine(stderr.Bytes()), Err: err}
	}
	return out, nil
}

// Stream is one pod container to read logs from.
type Stream struct {
	Pod, Container string
	Label          string // "pod" or "pod/container"
	Restarts       int    // container restartCount when resolved
}

// Raw is one line as kubectl printed it, timestamp prefix included.
type Raw struct {
	Label string
	Text  string
}

// Opts selects which logs to read.
type Opts struct {
	Namespace string
	Follow    bool
	Since     time.Duration
	SinceTime time.Time
	Previous  bool
}

func logArgs(o Opts, s Stream, resume time.Time) []string {
	a := []string{"logs"}
	if o.Namespace != "" {
		a = append(a, "-n", o.Namespace)
	}
	a = append(a, s.Pod, "-c", s.Container, "--timestamps")
	if o.Follow {
		a = append(a, "-f")
	}
	if o.Previous {
		a = append(a, "--previous")
	}
	start := resume
	if start.IsZero() {
		start = o.SinceTime
	}
	switch {
	case !start.IsZero():
		a = append(a, "--since-time="+start.UTC().Format(time.RFC3339Nano))
	case o.Since > 0:
		a = append(a, "--since="+o.Since.String())
	}
	return a
}

const defaultRetries = 3

// Runner streams one container's logs, with retries when following.
type Runner struct {
	K          Kubectl
	Opts       Opts
	MaxRetries int                             // follow only; 0 means 3
	Backoff    func(attempt int) time.Duration // nil means 1s, 2s, 4s...
	Notify     func(msg string)                // may be nil
}

func (r Runner) notify(s Stream, format string, a ...any) {
	if r.Notify != nil {
		r.Notify(fmt.Sprintf("[%s] stream ended: ", s.Label) + fmt.Sprintf(format, a...))
	}
}

// Run reads the stream and calls emit for every line. emit runs on the
// caller's goroutine, so a blocking emit slows kubectl down and loses nothing.
// Run returns nil on a clean end or when ctx is cancelled.
func (r Runner) Run(ctx context.Context, s Stream, emit func(Raw)) error {
	retries := r.MaxRetries
	if retries == 0 {
		retries = defaultRetries
	}
	var last time.Time
	attempt := 0
	for {
		n, err := r.once(ctx, s, &last, emit)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			if r.Opts.Follow {
				r.notify(s, "EOF")
			}
			return nil
		}
		if !r.Opts.Follow {
			r.notify(s, "%v", err)
			return err
		}
		if n > 0 {
			attempt = 0
		}
		attempt++
		if attempt > retries {
			r.notify(s, "%v; giving up", err)
			return err
		}
		r.notify(s, "%v; retry %d/%d", err, attempt, retries)
		wait := time.Second << (attempt - 1)
		if r.Backoff != nil {
			wait = r.Backoff(attempt)
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil
		}
	}
}

// once runs kubectl one time. *last is the newest timestamp emitted so far:
// it resumes the stream and drops replayed lines. kubectl --since-time has
// one-second precision, so a retry can replay up to a second of lines.
func (r Runner) once(ctx context.Context, s Stream, last *time.Time, emit func(Raw)) (int, error) {
	c := r.K.cmd(ctx, logArgs(r.Opts, s, *last))
	stdout, err := c.StdoutPipe()
	if err != nil {
		return 0, &KubectlError{Err: err}
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		return 0, &KubectlError{Err: err}
	}
	skipUpTo := *last
	rd := bufio.NewReader(stdout) // no line length limit
	n := 0
	for {
		line, rerr := rd.ReadString('\n')
		if line != "" {
			text := strings.TrimRight(line, "\r\n")
			ts, _ := parse.SplitTimestamp(text)
			if skipUpTo.IsZero() || ts.IsZero() || ts.After(skipUpTo) {
				emit(Raw{Label: s.Label, Text: text})
				n++
				if !ts.IsZero() {
					*last = ts
				}
			}
		}
		if rerr != nil {
			break
		}
	}
	werr := c.Wait()
	if ctx.Err() != nil {
		return n, ctx.Err()
	}
	if werr != nil {
		return n, &KubectlError{Stderr: lastLine(stderr.Bytes()), Err: werr}
	}
	return n, nil
}
