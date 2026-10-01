package main

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

var webBase = []string{"-n", "shop", "-l", "app=web", "--poll", "50ms", "--web"}

// webURL waits for the "web UI at" line on stderr and returns the URL.
func webURL(t *testing.T, errs *safeBuf) string {
	t.Helper()
	const marker = "web UI at "
	var url string
	waitFor(t, "web UI URL", func() bool {
		s := errs.String()
		i := strings.Index(s, marker)
		if i < 0 {
			return false
		}
		rest := s[i+len(marker):]
		j := strings.IndexByte(rest, '\n')
		if j < 0 {
			return false
		}
		url = rest[:j]
		return true
	})
	return url
}

// sseData reads /events until every want substring has appeared in the data
// lines, and returns all data lines read.
func sseData(t *testing.T, url string, want ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body) // a Reader: records can be long
	var got strings.Builder
	for {
		line, err := r.ReadString('\n')
		if strings.HasPrefix(line, "data: ") {
			got.WriteString(line)
		}
		all := true
		for _, w := range want {
			all = all && strings.Contains(got.String(), w)
		}
		if all {
			return got.String()
		}
		if err != nil {
			t.Fatalf("stream ended before %q: %v\ngot: %s", want, err, got.String())
		}
	}
}

func TestTailWebServesFilteredRecordsAndKeepsStdoutEmpty(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	out, errs, stop := startTail(t, append(append([]string{}, webBase...), "--level", "ERROR")...)
	url := webURL(t, errs)
	got := sseData(t, url, "a-three")
	for _, dropped := range []string{"a-one", "b-two", "b-four"} {
		if strings.Contains(got, dropped) {
			t.Errorf("record %q passed --level ERROR: %s", dropped, got)
		}
	}
	if !strings.Contains(got, `"raw":`) {
		t.Errorf("not a JSON record: %s", got)
	}
	if s := out.String(); s != "" {
		t.Errorf("stdout must stay empty with --web, got %q", s)
	}
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

func TestTailWebSurfacesDedupeRepeats(t *testing.T) {
	f := setup(t)
	f.SetLogs("a-1", "app", a1+" same\n"+a1+" same\n"+a1+" same\n"+a3+" other\n")
	f.Hang("a-1")
	f.Hang("b-1")
	_, errs, _ := startTail(t, append(append([]string{}, webBase...), "--dedupe")...)
	sseData(t, webURL(t, errs), `"repeats":2`)
}

func TestTailWebCtrlCWithOpenStreamExitsCleanly(t *testing.T) {
	f := setup(t)
	f.Hang("a-1")
	f.Hang("b-1")
	_, errs, stop := startTail(t, webBase...)
	url := webURL(t, errs)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url+"/events", nil)
	resp, err := http.DefaultClient.Do(req) // a browser tab that stays connected
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	if code := stop(); code != 0 {
		t.Fatalf("exit code %d", code)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Fatalf("tail took %v to exit with an open stream", d)
	}
}

func TestTailWebPortInUseExits1(t *testing.T) {
	setup(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	out, errs := &safeBuf{}, &safeBuf{}
	code := execute(context.Background(), append(append([]string{"tail"}, webBase...), "--web-addr", ln.Addr().String()), out, errs)
	if code != 1 || !strings.Contains(errs.String(), "klog: --web:") || strings.Contains(errs.String(), "web UI at") {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
}

func TestTailWebZeroPodsExits3WithoutURL(t *testing.T) {
	f := setup(t)
	f.SetGet("pods", `{"items":[]}`)
	out, errs := &safeBuf{}, &safeBuf{}
	code := execute(context.Background(), append([]string{"tail"}, webBase...), out, errs)
	if code != 3 || strings.Contains(errs.String(), "web UI at") {
		t.Fatalf("code %d, stderr %q", code, errs.String())
	}
}

func TestTailWebUsageErrorsNeverCallKubectl(t *testing.T) {
	f := setup(t)
	for name, args := range map[string][]string{
		"web-addr without web": {"-l", "a=b", "--web-addr", "127.0.0.1:0"},
		"non-loopback address": {"-l", "a=b", "--web", "--web-addr", "0.0.0.0:0"},
		"malformed address":    {"-l", "a=b", "--web", "--web-addr", "nope"},
		"with out":             {"-l", "a=b", "--web", "--out", "x.log"},
		"with format":          {"-l", "a=b", "--web", "--format", "json"},
		"with template":        {"-l", "a=b", "--web", "--template", "{{.Raw}}"},
	} {
		t.Run(name, func(t *testing.T) {
			out, errs := &safeBuf{}, &safeBuf{}
			if code := execute(context.Background(), append([]string{"tail"}, args...), out, errs); code != 2 {
				t.Fatalf("code %d, stderr %s", code, errs.String())
			}
		})
	}
	if calls := f.Calls(); len(calls) != 0 {
		t.Fatalf("kubectl was called: %q", calls)
	}
}
