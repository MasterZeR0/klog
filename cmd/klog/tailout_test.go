package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"klog/internal/parse"
	"klog/internal/render"
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

type writeCounter struct{ writes [][]byte }

func (w *writeCounter) Write(p []byte) (int, error) {
	w.writes = append(w.writes, append([]byte(nil), p...))
	return len(p), nil
}

// tail --out relies on the renderer handing each line to the file in a single
// Write call (O_APPEND then lands it whole).
func TestRendererWritesEachLineInOneCall(t *testing.T) {
	for _, format := range []render.Format{render.Pretty, render.JSON, render.Raw, render.Template} {
		o := render.Options{Format: format}
		if format == render.Template {
			if err := o.SetTemplate("{{.Source}} {{.Raw}}"); err != nil {
				t.Fatal(err)
			}
		}
		w := &writeCounter{}
		r := render.New(w, o)
		for _, l := range []parse.Line{parse.Parse("a-1", d1+` {"level":"INFO","msg":"x"}`), parse.Parse("b-1", d2+" plain")} {
			if err := r.Write(l); err != nil {
				t.Fatal(err)
			}
		}
		if len(w.writes) != 2 || w.writes[0][len(w.writes[0])-1] != '\n' {
			t.Errorf("format %d: %d writes, want one per line: %q", format, len(w.writes), w.writes)
		}
	}
}

func TestTailOutLinesLandWhole(t *testing.T) {
	f := setup(t)
	var a, b strings.Builder
	want := map[string]int{}
	for i := 0; i < 300; i++ {
		la, lb := fmt.Sprintf("a-line-%03d %s", i, strings.Repeat("x", 500)), fmt.Sprintf("b-line-%03d %s", i, strings.Repeat("y", 500))
		ts := fmt.Sprintf("2026-09-30T12:%02d:%02d.000000000Z", i/60, i%60)
		a.WriteString(ts + " " + la + "\n")
		b.WriteString(ts + " " + lb + "\n")
		want[la], want[lb] = 1, 1
	}
	f.SetLogs("a-1", "app", a.String())
	f.SetLogs("b-1", "app", b.String())
	f.Hang("a-1")
	f.Hang("b-1")
	path := filepath.Join(t.TempDir(), "tail.log")
	_, _, stop := startTail(t, append([]string{"--out", path, "--since", "1h"}, tailBase...)...)
	read := func() []string { b, _ := os.ReadFile(path); return lines(string(b)) }
	waitFor(t, "all lines in the file", func() bool { return len(read()) >= 600 })
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	for _, l := range read() {
		if want[l] != 1 {
			t.Fatalf("torn or duplicate line %q", l)
		}
		want[l]++
	}
}
