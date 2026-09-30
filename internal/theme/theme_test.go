package theme

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func file(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "theme.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefault(t *testing.T) {
	d := Default()
	if d.Timestamp != "2" || d.Levels.Error != "1;31" || d.Levels.Info != "" ||
		d.Levels.Fatal != "1;97;41" || d.Trace.CausedBy != "31" || d.Keys != "36" || len(d.Labels) != 6 {
		t.Fatalf("unexpected default %+v", d)
	}
	d.Labels[0] = "changed"
	if Default().Labels[0] != "36" {
		t.Fatal("Default shares its Labels slice between calls")
	}
}

func TestPaint(t *testing.T) {
	if got := Paint("", "x"); got != "x" {
		t.Errorf("empty sgr: got %q", got)
	}
	if got := Paint("1;31", "x"); got != "\x1b[1;31mx\x1b[0m" {
		t.Errorf("got %q", got)
	}
}

func TestLoadPartialKeepsDefaults(t *testing.T) {
	th, err := Load(file(t, `{"levels":{"warn":"34"},"keys":"1","labels":["90"]}`))
	if err != nil {
		t.Fatal(err)
	}
	if th.Levels.Warn != "34" || th.Keys != "1" || len(th.Labels) != 1 || th.Labels[0] != "90" {
		t.Fatalf("overrides not applied: %+v", th)
	}
	if th.Levels.Error != "1;31" || th.Timestamp != "2" || th.Trace.Frame != "2" {
		t.Fatalf("defaults lost: %+v", th)
	}
}

func TestLoadExplicitEmptyValueClearsStyle(t *testing.T) {
	th, err := Load(file(t, `{"levels":{"error":""}}`))
	if err != nil || th.Levels.Error != "" {
		t.Fatalf("got %+v, %v", th, err)
	}
}

func TestLoadRejects(t *testing.T) {
	bad := map[string]string{
		"unknown top key":   `{"colour":"1"}`,
		"unknown level key": `{"levels":{"loud":"1"}}`,
		"word instead sgr":  `{"keys":"red"}`,
		"letter in sgr":     `{"timestamp":"1m"}`,
		"nested bad sgr":    `{"trace":{"frame":"x"}}`,
		"empty labels":      `{"labels":[]}`,
		"bad label entry":   `{"labels":["36","x"]}`,
		"wrong type":        `{"keys":1}`,
		"trailing data":     `{} {}`,
		"empty file":        ``,
		"not json":          `nope`,
	}
	for name, content := range bad {
		t.Run(name, func(t *testing.T) {
			p := file(t, content)
			_, err := Load(p)
			if err == nil || !strings.HasPrefix(err.Error(), "invalid theme "+p+": ") {
				t.Fatalf("Load(%s) error = %v", content, err)
			}
		})
	}
}

func TestLoadMissingFileWrapsNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v", err)
	}
}
