package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"klog/internal/parse"
	"klog/internal/render"
	"klog/internal/resolve"
	"klog/internal/run"
)

// tailStream is the main loop's record of one running container stream.
type tailStream struct {
	cancel   context.CancelFunc
	done     bool // the stream ended; set by the main loop only
	restarts int  // container restartCount when this stream started
}

func runTail(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlagSet("tail", stderr)
	cf := addCommon(fs)
	since := durationVar(fs, "since", 5*time.Minute, "backlog to show before following, for example 10m or 1d")
	poll := durationVar(fs, "poll", 5*time.Second, "how often to look for new and deleted pods")
	incf := addIncident(fs)
	wait := fs.Bool("wait", false, "keep polling when no pods match instead of exiting")
	outPath := fs.String("out", "", "append to this file (one write per line) instead of stdout")
	if code, done := parseFlags(fs, args, stderr); done {
		return code
	}
	c, err := cf.build()
	if err != nil {
		return usageError(stderr, err)
	}
	inc, err := incf.build(c)
	if err != nil {
		return usageError(stderr, err)
	}
	if err := c.checkDedupe(); err != nil {
		return usageError(stderr, err)
	}
	if *since < 0 {
		return usageError(stderr, errors.New("--since must not be negative"))
	}
	if *poll <= 0 {
		return usageError(stderr, errors.New("--poll must be positive"))
	}

	streams, err := resolve.Resolve(ctx, c.kubectl, c.target)
	if err != nil {
		if ctx.Err() != nil {
			return 0
		}
		return kubectlFailure(stderr, err)
	}
	if len(streams) == 0 {
		if !*wait {
			fmt.Fprintf(stderr, "klog: no pods matched (%s)\n", c.target.Describe())
			return 3
		}
		fmt.Fprintf(stderr, "klog: waiting for pods (%s)\n", c.target.Describe())
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	runner := run.Runner{
		K:      c.kubectl,
		Opts:   run.Opts{Namespace: c.target.Namespace, Follow: true, Since: *since},
		Notify: func(m string) { fmt.Fprintln(stderr, m) },
	}
	w := stdout
	if *outPath != "" {
		// The renderer builds each line in memory and writes it in one Write call
		// on this unbuffered O_APPEND file, so a line reaches it whole. The file
		// is append-only (no atomic rename like fetch --out) and concurrent klog
		// processes writing to one file are not supported.
		f, err := os.OpenFile(*outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(stderr, "klog: --out: %v\n", err)
			return 1
		}
		defer f.Close()
		w = f
	}
	view := c.view
	view.Color = *outPath == "" && useColor(stdout)
	renderer := render.New(w, view)

	out := make(chan parse.Line, 256) // bounded: a slow terminal slows the runners
	ended := make(chan *tailStream)
	active := map[string]*tailStream{}
	var wg sync.WaitGroup

	start := func(s run.Stream) {
		sctx, scancel := context.WithCancel(ctx)
		st := &tailStream{cancel: scancel, restarts: s.Restarts}
		active[s.Pod+"/"+s.Container] = st
		wg.Add(1)
		go func() {
			defer wg.Done()
			stg := inc.newStage(c.filter, nil)
			push, flush := c.stage(func(l parse.Line) {
				select {
				case out <- l: // room in the queue: never drop, even while stopping
					return
				default:
				}
				select {
				case out <- l:
				case <-sctx.Done():
				}
			})
			runner.Run(sctx, s, func(raw run.Raw) {
				for _, l := range stg.Feed(parse.Parse(raw.Label, raw.Text)) {
					push(l)
				}
			})
			flush() // stream ended or was cancelled: emit the pending summary
			select {
			case ended <- st:
			case <-ctx.Done():
			}
		}()
	}

	reconcile := func(streams []run.Stream) {
		seen := map[string]bool{}
		for _, s := range streams {
			key := s.Pod + "/" + s.Container
			seen[key] = true
			if st := active[key]; st != nil {
				if !st.done || s.Restarts <= st.restarts {
					continue // still running, or finished and not restarted
				}
				st.cancel()
			}
			start(s)
		}
		for key, st := range active {
			if !seen[key] {
				st.cancel()
				delete(active, key)
			}
		}
	}
	reconcile(streams)

	// ponytail: resolve runs on this loop, so a slow API server briefly delays
	// output. Move it to its own goroutine if that ever shows up.
	tick := time.NewTicker(*poll)
	defer tick.Stop()
	code := 0
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case l := <-out:
			if err := renderer.Write(l); err != nil {
				fmt.Fprintf(stderr, "klog: write: %v\n", err)
				code = 1
				break loop
			}
		case st := <-ended:
			st.done = true
		case <-tick.C:
			streams, err := resolve.Resolve(ctx, c.kubectl, c.target)
			if err != nil {
				if ctx.Err() == nil {
					fmt.Fprintf(stderr, "klog: warning: could not refresh pods: %v\n", err)
				}
				continue
			}
			reconcile(streams)
		}
	}
	cancel()
	wg.Wait()
	// Lines still queued, including summaries flushed as the streams stopped.
	for drained := false; code == 0 && !drained; {
		select {
		case l := <-out:
			if err := renderer.Write(l); err != nil {
				fmt.Fprintf(stderr, "klog: write: %v\n", err)
				code = 1
			}
		default:
			drained = true
		}
	}
	return code
}
