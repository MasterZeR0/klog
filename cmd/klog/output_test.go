package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTheme(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFetchPrettyFlattensJSON(t *testing.T) {
	setup(t)
	code, out, _ := klog(t, with()...)
	want := []string{
		"[a-1] 12:00:01.000 INFO a-one",
		"[b-1] 12:00:02.000 WARN b-two  requestId=r2",
		"[a-1] 12:00:03.000 ERROR a-three  orderId=12345678901234567890 requestId=r1",
		"[b-1] 12:00:04.000 plain b-four",
	}
	if code != 0 || !equal(lines(out), want) {
		t.Fatalf("code %d, got\n%s\nwant\n%s", code, out, strings.Join(want, "\n"))
	}
}

func TestFetchNoFlattenKeepsRawJSON(t *testing.T) {
	setup(t)
	code, out, _ := klog(t, with("--no-flatten")...)
	want := []string{
		"[a-1] 12:00:01.000 " + aOne,
		"[b-1] 12:00:02.000 " + bTwo,
		"[a-1] 12:00:03.000 " + aThree,
		"[b-1] 12:00:04.000 " + bFour,
	}
	if code != 0 || !equal(lines(out), want) {
		t.Fatalf("code %d, got\n%s\nwant\n%s", code, out, strings.Join(want, "\n"))
	}
}

func TestThemeErrorsExit2AndNeverCallKubectl(t *testing.T) {
	f := setup(t)
	cfg, err := os.UserConfigDir() // HOME was redirected by setup
	if err != nil {
		t.Skip("no user config dir")
	}
	if err := os.MkdirAll(filepath.Join(cfg, "klog"), 0o755); err != nil {
		t.Fatal(err)
	}
	defaultPath := filepath.Join(cfg, "klog", "theme.json")
	if err := os.WriteFile(defaultPath, []byte(`{"colour":"1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bad := writeTheme(t, `{"keys":"red"}`)
	cases := map[string][]string{
		"malformed default file":     with(),
		"explicit file is malformed": with("--theme", bad),
		"explicit file is missing":   with("--theme", filepath.Join(t.TempDir(), "nope.json")),
		"still validated for json":   with("--format", "json", "--theme", bad),
		"still validated for raw":    with("--format", "raw", "--theme", bad),
		"tail validates it as well":  {"tail", "-n", "shop", "-l", "app=web", "--theme", bad},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			code, _, errs := klog(t, args...)
			if code != 2 || !strings.Contains(errs, "klog: invalid theme ") {
				t.Fatalf("code %d, stderr %q", code, errs)
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}

func TestThemeFlagAcceptsAValidFile(t *testing.T) {
	setup(t)
	p := writeTheme(t, `{"levels":{"warn":"34"},"labels":["90"]}`)
	code, out, errs := klog(t, with("--theme", p)...)
	if code != 0 || !strings.Contains(out, "WARN b-two  requestId=r2") {
		t.Fatalf("code %d, stderr %q, stdout\n%s", code, errs, out)
	}
}

func TestDefaultThemePathBlockedByAFileFallsBackToDefaults(t *testing.T) {
	setup(t)
	cfg, err := os.UserConfigDir() // HOME was redirected by setup
	if err != nil {
		t.Skip("no user config dir")
	}
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	// <configdir>/klog is a regular file, so reading klog/theme.json fails with ENOTDIR.
	if err := os.WriteFile(filepath.Join(cfg, "klog"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := klog(t, with()...)
	if code != 0 || !strings.Contains(out, "INFO a-one") {
		t.Fatalf("code %d, stderr %q, stdout\n%s", code, errs, out)
	}
}

func TestTailNoFlattenKeepsRawJSON(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, _ := startTail(t, "-n", "shop", "-l", "app=web", "--poll", "50ms", "--no-flatten")
	waitFor(t, "raw JSON line", func() bool { return strings.Contains(out.String(), aOne) })
}

func TestTailFlattensByDefault(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, _ := startTail(t, "-n", "shop", "-l", "app=web", "--poll", "50ms")
	waitFor(t, "flattened line", func() bool { return strings.Contains(out.String(), "INFO a-one") })
	if strings.Contains(out.String(), aOne) {
		t.Fatalf("raw JSON leaked into flattened output:\n%s", out.String())
	}
}

func TestValidDefaultThemeFileIsUsed(t *testing.T) {
	setup(t)
	cfg, err := os.UserConfigDir() // HOME was redirected by setup
	if err != nil {
		t.Skip("no user config dir")
	}
	if err := os.MkdirAll(filepath.Join(cfg, "klog"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg, "klog", "theme.json"), []byte(`{"levels":{"warn":"34"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errs := klog(t, with()...)
	if code != 0 || !strings.Contains(out, "WARN b-two  requestId=r2") {
		t.Fatalf("code %d, stderr %q, stdout\n%s", code, errs, out)
	}
}
