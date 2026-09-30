package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = `klog: tail and fetch Kubernetes pod logs with filters

Usage:
  klog tail  [flags]   follow logs live
  klog fetch [flags]   fetch a time range, sorted by timestamp

Either command accepts a leading @name to load saved flags from
<user config dir>/klog/profiles.json; flags after it override the profile.

Run "klog tail -h" or "klog fetch -h" for the flags.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := execute(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// execute runs the CLI and returns the process exit code.
func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	stderr = &syncWriter{w: stderr} // goroutines print warnings concurrently
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	if args[0] == "tail" || args[0] == "fetch" {
		rest, err := expandProfile(args[1:])
		if err != nil {
			return usageError(stderr, err)
		}
		args = append([]string{args[0]}, rest...)
	}
	switch args[0] {
	case "tail":
		return runTail(ctx, args[1:], stdout, stderr)
	case "fetch":
		return runFetch(ctx, args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage)
		return 0
	}
	fmt.Fprintf(stderr, "klog: unknown command %q\n\n%s", args[0], usage)
	return 2
}
