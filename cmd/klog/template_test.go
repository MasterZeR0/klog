package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFetchTemplate(t *testing.T) {
	setup(t)
	code, out, errs := klog(t, with("--format", "template", "--tz", "America/New_York",
		"--template", `{{.Source}} {{.Time.Format "15:04:05"}} {{.Level}}|{{.Msg}}|{{.JSON.requestId}}`)...)
	want := []string{
		"a-1 08:00:01 INFO|a-one|<no value>",
		"b-1 08:00:02 WARN|b-two|r2",
		"a-1 08:00:03 ERROR|a-three|r1",
		"b-1 08:00:04 ||<no value>", // plain text: no level, no msg, nil JSON
	}
	if code != 0 || !equal(lines(out), want) {
		t.Fatalf("code %d, stderr %s, got\n%s\nwant\n%v", code, errs, out, want)
	}
}

func TestTemplateFlagUsageErrors(t *testing.T) {
	f := setup(t)
	for name, args := range map[string][]string{
		"template without format": with("--template", "{{.Raw}}"),
		"format without template": with("--format", "template"),
		"bad template":            with("--format", "template", "--template", "{{.Raw"),
		"template with json":      with("--format", "json", "--template", "{{.Raw}}"),
	} {
		t.Run(name, func(t *testing.T) {
			if code, _, errs := klog(t, args...); code != 2 {
				t.Fatalf("code %d, stderr %s", code, errs)
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}

func TestTailTemplateToFile(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	path := filepath.Join(t.TempDir(), "t.log")
	args := []string{"-n", "shop", "-l", "app=web", "--poll", "50ms", "--format", "template", "--template", "{{.Source}}:{{.Msg}}", "--out", path}
	out, _, stop := startTail(t, args...)
	read := func() string { b, _ := os.ReadFile(path); return string(b) }
	waitFor(t, "rendered lines", func() bool {
		s := read()
		return strings.Contains(s, "a-1:a-one\n") && strings.Contains(s, "b-1:b-two\n")
	})
	stop()
	if out.String() != "" {
		t.Fatalf("stdout %q", out.String())
	}
}
