package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"klog/internal/filter"
	"klog/internal/render"
	"klog/internal/resolve"
	"klog/internal/run"
	"klog/internal/theme"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// commonFlags are the raw flag values shared by tail and fetch.
type commonFlags struct {
	context, ns, selector, deployment, pod, container string
	level, grep, exclude, format, tz                  string
	fields                                            multiFlag
	theme                                             string
	noFlatten                                         bool
}

func addCommon(fs *flag.FlagSet) *commonFlags {
	c := &commonFlags{}
	fs.StringVar(&c.context, "context", "", "kubeconfig context")
	fs.StringVar(&c.ns, "n", "", "namespace (default: the context's namespace)")
	fs.StringVar(&c.selector, "l", "", "target: label selector")
	fs.StringVar(&c.deployment, "d", "", "target: deployment name")
	fs.StringVar(&c.pod, "p", "", "target: pod name regex")
	fs.StringVar(&c.container, "c", "", "container name regex (default: all containers)")
	fs.StringVar(&c.level, "level", "", "minimum level: TRACE, DEBUG, INFO, WARN, ERROR or FATAL")
	fs.Var(&c.fields, "field", "JSON field filter key=value, key!=value or key~regex (repeatable)")
	fs.StringVar(&c.grep, "grep", "", "keep lines matching this regex")
	fs.StringVar(&c.exclude, "exclude", "", "drop lines matching this regex")
	fs.StringVar(&c.format, "format", "pretty", "output format: pretty, json or raw")
	fs.StringVar(&c.tz, "tz", "", "timezone for timestamps (e.g. America/New_York or Local; default: UTC)")
	fs.BoolVar(&c.noFlatten, "no-flatten", false, "pretty format: print JSON lines as raw JSON instead of LEVEL msg key=val")
	fs.StringVar(&c.theme, "theme", "", "theme file (default: <user config dir>/klog/theme.json)")
	return c
}

// common is the validated result of commonFlags.
type common struct {
	kubectl run.Kubectl
	target  resolve.Target
	filter  filter.Config
	view    render.Options // Format, TZ, NoFlatten, Theme; callers set Color
}

func (c *commonFlags) build() (common, error) {
	var out common
	out.kubectl = run.Kubectl{Bin: os.Getenv("KLOG_KUBECTL"), Context: c.context}
	out.target = resolve.Target{
		Namespace: c.ns, Selector: c.selector, Deployment: c.deployment,
		PodRegex: c.pod, Container: c.container,
	}
	if err := out.target.Validate(); err != nil {
		return out, err
	}
	if c.level != "" {
		lv, ok := filter.ParseLevel(c.level)
		if !ok {
			return out, fmt.Errorf("unknown --level %q (want TRACE, DEBUG, INFO, WARN, ERROR or FATAL)", c.level)
		}
		out.filter.MinLevel = lv
	}
	for _, s := range c.fields {
		f, err := filter.ParseField(s)
		if err != nil {
			return out, err
		}
		out.filter.Fields = append(out.filter.Fields, f)
	}
	var err error
	if out.filter.Grep, err = compileOpt("--grep", c.grep); err != nil {
		return out, err
	}
	if out.filter.Exclude, err = compileOpt("--exclude", c.exclude); err != nil {
		return out, err
	}
	out.view.Format, err = render.ParseFormat(c.format)
	if err != nil {
		return out, err
	}
	if c.tz == "" {
		out.view.TZ = time.UTC
	} else {
		out.view.TZ, err = time.LoadLocation(c.tz)
		if err != nil {
			return out, fmt.Errorf("invalid --tz %q: %w", c.tz, err)
		}
	}
	out.view.NoFlatten = c.noFlatten
	if out.view.Theme, err = loadTheme(c.theme); err != nil {
		return out, err
	}
	return out, nil
}

func compileOpt(name, expr string) (*regexp.Regexp, error) {
	if expr == "" {
		return nil, nil
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", name, err)
	}
	return re, nil
}

// loadTheme reads --theme FILE, or the default file in the user config dir.
// A missing default file means the built-in theme; a missing explicit file is an error.
func loadTheme(path string) (theme.Theme, error) {
	if path != "" {
		return theme.Load(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return theme.Default(), nil // no $HOME: nothing to read, use the defaults
	}
	th, err := theme.Load(filepath.Join(dir, "klog", "theme.json"))
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) { // ENOTDIR: <config>/klog is a file
		return theme.Default(), nil
	}
	return th, err
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("klog "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parseFlags parses args. It returns (code, true) when the caller must exit now.
func parseFlags(fs *flag.FlagSet, args []string, stderr io.Writer) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, true
		}
		return 2, true // the flag package already printed the error and usage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "klog: unexpected argument %q\n", fs.Arg(0))
		return 2, true
	}
	return 0, false
}

func usageError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "klog: %v\n", err)
	return 2
}

func kubectlFailure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "klog: %v\n", err)
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(stderr, "hint: kubectl not found; install it, or set KLOG_KUBECTL to its path")
	} else {
		fmt.Fprintln(stderr, "hint: check your context, namespace and login (try: kubectl get pods)")
	}
	return 1
}

var dayUnit = regexp.MustCompile(`(\d*\.?\d+)d`)

// parseDuration is time.ParseDuration plus a day unit: a day is 24h, so 2d is
// 48h and 1d12h is 36h. It does not know about DST or calendars.
func parseDuration(s string) (time.Duration, error) {
	conv := dayUnit.ReplaceAllStringFunc(s, func(m string) string {
		n, _ := strconv.ParseFloat(m[:len(m)-1], 64)
		return strconv.FormatFloat(n*24, 'f', -1, 64) + "h"
	})
	d, err := time.ParseDuration(conv)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: want a number and unit such as 30m, 2h, 2d or 1d12h", s)
	}
	return d, nil
}

// durationValue is a flag.Value for durations that accept the d unit.
type durationValue time.Duration

func (d *durationValue) String() string { return time.Duration(*d).String() }
func (d *durationValue) Set(v string) error {
	x, err := parseDuration(v)
	*d = durationValue(x)
	return err
}

// durationVar is fs.Duration with parseDuration.
func durationVar(fs *flag.FlagSet, name string, def time.Duration, usage string) *time.Duration {
	d := def
	fs.Var((*durationValue)(&d), name, usage)
	return &d
}

// parseWhen reads an RFC3339 time, or a duration meaning "that long ago".
func parseWhen(s string, now time.Time) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	if d, err := parseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, fmt.Errorf("invalid time %q: want RFC3339 (2026-09-30T12:00:00Z) or a duration such as 30m or 2d", s)
}

// useColor is true only for a terminal stdout with NO_COLOR unset.
func useColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok || os.Getenv("NO_COLOR") != "" {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
