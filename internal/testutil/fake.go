// Package testutil provides a fake kubectl for tests. It is not used in production code.
package testutil

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// The script logs every call to calls.log, then serves `get <resource>` from
// get-<resource>.json and `logs <pod> -c <container>` from
// logs-<pod>-<container>.txt. failn-<pod> holds a countdown of failures,
// hang-<pod> keeps the stream open after the lines.
const script = `#!/bin/sh
D='%s'
echo "$*" >> "$D/calls.log"
if [ "$1" = "--context" ]; then shift 2; fi
case "$1" in
get)
  f="$D/get-$2.json"
  if [ ! -f "$f" ]; then echo "Error from server (NotFound): $2 not found" >&2; exit 1; fi
  cat "$f"
  exit 0;;
logs)
  shift
  pod=; cont=
  while [ $# -gt 0 ]; do
    case "$1" in
      -n) shift 2;;
      -c) cont="$2"; shift 2;;
      -*) shift;;
      *) pod="$1"; shift;;
    esac
  done
  f="$D/logs-$pod-$cont.txt"
  [ -f "$f" ] && cat "$f"
  if [ -f "$D/failn-$pod" ]; then
    n=$(cat "$D/failn-$pod")
    if [ "$n" -gt 0 ]; then echo $((n-1)) > "$D/failn-$pod"; echo "boom" >&2; exit 1; fi
  fi
  if [ -f "$D/hang-$pod" ]; then exec sleep 60; fi
  exit 0;;
esac
echo "fake kubectl: unsupported: $*" >&2
exit 1
`

type Fake struct {
	Dir string
	Bin string
	t   testing.TB
}

func NewFake(t testing.TB) *Fake {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake kubectl is a sh script") // ponytail: port to a Go helper binary if Windows CI needs these tests
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte(fmt.Sprintf(script, dir)), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Fake{Dir: dir, Bin: bin, t: t}
}

// write is atomic (temp + rename) so a running fake never reads a half file.
func (f *Fake) write(name, content string) {
	f.t.Helper()
	tmp := filepath.Join(f.Dir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(f.Dir, name)); err != nil {
		f.t.Fatal(err)
	}
}

func (f *Fake) SetGet(resource, body string) { f.write("get-"+resource+".json", body) }
func (f *Fake) SetLogs(pod, container, content string) {
	f.write("logs-"+pod+"-"+container+".txt", content)
}
func (f *Fake) FailTimes(pod string, n int) { f.write("failn-"+pod, strconv.Itoa(n)) }
func (f *Fake) Hang(pod string)             { f.write("hang-"+pod, "") }

// Calls returns the argument line of every kubectl invocation so far.
func (f *Fake) Calls() []string {
	b, err := os.ReadFile(filepath.Join(f.Dir, "calls.log"))
	if err != nil || len(b) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

// Pod describes one pod for PodList.
type Pod struct {
	Name       string
	Phase      string // default Running
	Containers []string
	Restarts   int
}

// PodList renders `kubectl get pods -o json` output.
func PodList(pods ...Pod) string {
	items := []any{}
	for _, p := range pods {
		phase := p.Phase
		if phase == "" {
			phase = "Running"
		}
		var spec, status []map[string]any
		for _, c := range p.Containers {
			spec = append(spec, map[string]any{"name": c})
			status = append(status, map[string]any{"name": c, "restartCount": p.Restarts})
		}
		items = append(items, map[string]any{
			"metadata": map[string]any{"name": p.Name},
			"spec":     map[string]any{"containers": spec},
			"status":   map[string]any{"phase": phase, "containerStatuses": status},
		})
	}
	b, _ := json.Marshal(map[string]any{"items": items})
	return string(b)
}
