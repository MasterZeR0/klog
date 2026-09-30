//go:build integration

// Run with: KLOG_IT_CONTEXT=kind-klog-it go test -tags integration ./integration/
// Needs a running cluster (for example `kind create cluster --name klog-it`).
package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sh(t *testing.T, name string, args ...string) string {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return string(out)
}

func TestFetchFromRealPod(t *testing.T) {
	kctx := os.Getenv("KLOG_IT_CONTEXT")
	if kctx == "" {
		t.Skip("set KLOG_IT_CONTEXT to a kubectl context to run")
	}
	bin := filepath.Join(t.TempDir(), "klog")
	sh(t, "go", "build", "-o", bin, "../cmd/klog")

	sh(t, "kubectl", "--context", kctx, "run", "klog-it-emitter", "--image=busybox", "--restart=Never", "--",
		"sh", "-c", `echo '{"level":"INFO","msg":"quiet"}'; echo '{"level":"ERROR","msg":"loud"}'; sleep 120`)
	t.Cleanup(func() {
		exec.Command("kubectl", "--context", kctx, "delete", "pod", "klog-it-emitter", "--now").Run()
	})
	sh(t, "kubectl", "--context", kctx, "wait", "--for=condition=Ready", "pod/klog-it-emitter", "--timeout=90s")

	out := sh(t, bin, "fetch", "--context", kctx, "-p", "^klog-it-emitter$", "--since", "10m", "--level", "ERROR", "--format", "raw")
	if !strings.Contains(out, "loud") || strings.Contains(out, "quiet") {
		t.Fatalf("unexpected output:\n%s", out)
	}
}
