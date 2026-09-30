package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeProfiles(t *testing.T, content string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KLOG_PROFILES", p)
}

func TestExpandProfile(t *testing.T) {
	writeProfiles(t, `{"prod": ["-n","shop","--level","WARN"], "other": []}`)
	got, err := expandProfile([]string{"@prod", "--level", "ERROR", "--field", "a=b"})
	want := []string{"-n", "shop", "--level", "WARN", "--level", "ERROR", "--field", "a=b"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, err %v, want %q", got, err, want)
	}
	for _, args := range [][]string{nil, {"-n", "x"}, {"-n", "@prod"}} {
		if got, err := expandProfile(args); err != nil || !reflect.DeepEqual(got, args) {
			t.Fatalf("%q changed to %q, err %v", args, got, err)
		}
	}
}

func TestExpandProfileErrors(t *testing.T) {
	writeProfiles(t, `{"prod": [], "dev": []}`)
	_, err := expandProfile([]string{"@nope"})
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "dev, prod") {
		t.Fatalf("err %v", err)
	}
	for name, content := range map[string]string{
		"not json":   `{`,
		"not arrays": `{"prod": "-n shop"}`,
	} {
		writeProfiles(t, content)
		if _, err := expandProfile([]string{"@prod"}); err == nil || !strings.Contains(err.Error(), "profiles.json") {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	t.Setenv("KLOG_PROFILES", filepath.Join(t.TempDir(), "missing.json"))
	if _, err := expandProfile([]string{"@prod"}); err == nil || !strings.Contains(err.Error(), "none") {
		t.Fatalf("missing file: err %v", err)
	}
}

func TestDefaultProfilesPath(t *testing.T) {
	setup(t) // HOME is a temp dir
	t.Setenv("KLOG_PROFILES", "")
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Skip(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "klog"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "klog", "profiles.json"), []byte(`{"x": ["-n","y"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := expandProfile([]string{"@x"}); err != nil || !reflect.DeepEqual(got, []string{"-n", "y"}) {
		t.Fatalf("got %q, err %v", got, err)
	}
}

func TestFetchProfile(t *testing.T) {
	setup(t)
	writeProfiles(t, `{"web": ["-n","shop","-l","app=web","--since-time","2026-09-30T12:00:01Z","--format","json","--field","requestId=r2"]}`)
	code, out, errs := klog(t, "fetch", "@web", "--format", "raw")
	if code != 0 || !equal(lines(out), []string{bTwo}) { // CLI --format wins over the profile's
		t.Fatalf("code %d, stderr %s, stdout %q", code, errs, out)
	}
}

func TestTailProfileFlagsAccumulateAndErrors(t *testing.T) {
	f := setup(t)
	writeProfiles(t, `{"w": ["-n","shop","-l","app=web","--format","raw","--field","requestId=r2"]}`)
	f.Hang("a-1")
	f.Hang("b-1")
	out, _, _ := startTail(t, "@w", "--field", "msg=b-two")
	waitFor(t, "b-two", func() bool { return strings.Contains(out.String(), "b-two") })
	if strings.Contains(out.String(), "a-one") {
		t.Fatalf("filters did not accumulate: %q", out.String())
	}
	for _, args := range [][]string{{"fetch", "@nope"}, {"tail", "@nope"}} {
		if code, _, errs := klog(t, args...); code != 2 || !strings.Contains(errs, "available: w") {
			t.Fatalf("%v: code %d, stderr %q", args, code, errs)
		}
	}
}
