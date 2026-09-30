package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"klog/internal/filter"
	"klog/internal/merge"
	"klog/internal/parse"
	"klog/internal/render"
	"klog/internal/resolve"
	"klog/internal/run"
)

func runFetch(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("fetch", stderr)
	cf := addCommon(fs)
	since := fs.Duration("since", 0, "how far back to fetch, for example 2h (largest unit: h)")
	sinceTime := fs.String("since-time", "", "absolute start time, RFC3339")
	until := fs.String("until", "", "end time: RFC3339, or a duration meaning that long ago")
	previous := fs.Bool("previous", false, "logs of the previous container instance")
	outPath := fs.String("out", "", "write to this file (atomically) instead of stdout")
	if code, done := parseFlags(fs, args, stderr); done {
		return code
	}
	c, err := cf.build()
	if err != nil {
		return usageError(stderr, err)
	}

	now := time.Now()
	var sinceT time.Time
	switch {
	case *since < 0:
		return usageError(stderr, errors.New("--since must not be negative"))
	case *since > 0 && *sinceTime != "":
		return usageError(stderr, errors.New("--since and --since-time are mutually exclusive"))
	case *since > 0:
		sinceT = now.Add(-*since)
	case *sinceTime != "":
		if sinceT, err = time.Parse(time.RFC3339, *sinceTime); err != nil {
			return usageError(stderr, fmt.Errorf("invalid --since-time %q: want RFC3339", *sinceTime))
		}
	default:
		return usageError(stderr, errors.New("fetch needs --since or --since-time"))
	}
	var untilT time.Time
	if *until != "" {
		if untilT, err = parseWhen(*until, now); err != nil {
			return usageError(stderr, err)
		}
		if !untilT.After(sinceT) {
			return usageError(stderr, errors.New("--until must be after the start time"))
		}
	}

	streams, err := resolve.Resolve(ctx, c.kubectl, c.target)
	if err != nil {
		if ctx.Err() != nil {
			return 1
		}
		return kubectlFailure(stderr, err)
	}
	if len(streams) == 0 {
		fmt.Fprintf(stderr, "klog: no pods matched (%s)\n", c.target.Describe())
		return 3
	}

	w, commit, abort, err := openOutput(*outPath, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "klog: --out: %v\n", err)
		return 1
	}
	view := c.view
	view.Color = *outPath == "" && useColor(stdout)
	renderer := render.New(w, view)

	opts := run.Opts{Namespace: c.target.Namespace, Previous: *previous}
	if *since > 0 {
		opts.Since = *since
	} else {
		opts.SinceTime = sinceT
	}
	runner := run.Runner{K: c.kubectl, Opts: opts, Notify: func(m string) { fmt.Fprintln(stderr, m) }}

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	var failed atomic.Int32
	srcs := make([]<-chan parse.Line, len(streams))
	for i, s := range streams {
		ch := make(chan parse.Line, 64)
		srcs[i] = ch
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer close(ch)
			f := filter.New(c.filter)
			first := true
			err := runner.Run(ctx, s, func(raw run.Raw) {
				l := parse.Parse(raw.Label, raw.Text)
				if first && !l.Time.IsZero() {
					first = false
					if l.Time.After(sinceT) {
						fmt.Fprintf(stderr, "klog: warning: earliest line for [%s] is %s, later than the requested start %s; older logs may be rotated away or the pod started later\n",
							s.Label, l.Time.Format(time.RFC3339), sinceT.Format(time.RFC3339))
					}
				}
				if !untilT.IsZero() && !l.Time.IsZero() && l.Time.After(untilT) {
					return
				}
				if !f.Keep(l) {
					return
				}
				select {
				case ch <- l:
				case <-ctx.Done():
				}
			})
			if err != nil {
				failed.Add(1)
			}
		}()
	}

	var writeErr error
	for l := range merge.Merge(ctx, srcs) {
		if writeErr = renderer.Write(l); writeErr != nil {
			cancel() // stops the streams and the merger; every goroutine selects on ctx.Done()
			break
		}
	}
	cancel()
	wg.Wait()

	switch {
	case writeErr != nil:
		abort()
		fmt.Fprintf(stderr, "klog: write: %v\n", writeErr)
		return 1
	case parent.Err() != nil:
		abort()
		fmt.Fprintln(stderr, "klog: interrupted")
		return 1
	case int(failed.Load()) == len(streams):
		abort()
		fmt.Fprintf(stderr, "klog: all %d streams failed\n", len(streams))
		return 1
	}
	if err := commit(); err != nil {
		fmt.Fprintf(stderr, "klog: --out: %v\n", err)
		return 1
	}
	return 0
}

// openOutput returns a buffered writer. With no path it writes to stdout and
// commit only flushes. With a path it writes a temp file in the same
// directory and commit renames it over path, so readers never see a partial
// file; abort removes the temp file.
func openOutput(path string, stdout io.Writer) (w io.Writer, commit func() error, abort func(), err error) {
	if path == "" {
		bw := bufio.NewWriter(stdout)
		return bw, bw.Flush, func() { bw.Flush() }, nil
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".klog-*")
	if err != nil {
		return nil, nil, nil, err
	}
	bw := bufio.NewWriter(f)
	commit = func() error {
		if err := bw.Flush(); err != nil {
			f.Close()
			os.Remove(f.Name())
			return err
		}
		if err := f.Chmod(0o644); err != nil {
			f.Close()
			os.Remove(f.Name())
			return err
		}
		if err := f.Close(); err != nil {
			os.Remove(f.Name())
			return err
		}
		return os.Rename(f.Name(), path)
	}
	abort = func() {
		f.Close()
		os.Remove(f.Name())
	}
	return bw, commit, abort, nil
}
