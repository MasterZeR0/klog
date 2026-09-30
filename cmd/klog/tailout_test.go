package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailOutAppendsToFile(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	path := filepath.Join(t.TempDir(), "tail.log")
	if err := os.WriteFile(path, []byte("earlier\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, stop := startTail(t, append([]string{"--out", path}, tailBase...)...)
	read := func() string { b, _ := os.ReadFile(path); return string(b) }
	waitFor(t, "lines in the file", func() bool { return strings.Contains(read(), "a-one") && strings.Contains(read(), "b-two") })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if !strings.HasPrefix(read(), "earlier\n") || out.String() != "" {
		t.Fatalf("file %q, stdout %q", read(), out.String())
	}
}

func TestTailOutUnwritableExits1(t *testing.T) {
	setup(t)
	out, errs := &safeBuf{}, &safeBuf{}
	bad := filepath.Join(t.TempDir(), "no", "such", "dir", "x.log")
	code := execute(context.Background(), append([]string{"tail", "--out", bad}, tailBase...), out, errs)
	if code != 1 || !strings.Contains(errs.String(), "--out") {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
}
