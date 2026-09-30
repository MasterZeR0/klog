# klog handoff

Tell a new agent session opened in `~/Projects/klog`: "Read HANDOFF.md and continue."

## Goal
Go CLI that tails and fetches logs from many Kubernetes pods at once, with filters for level, JSON fields and text. Standalone, usable across services.

## State
- Spec approved: `docs/superpowers/specs/2026-09-30-klog-design.md` (read its "Clarifications from planning" section too).
- Plan approved: `docs/superpowers/plans/2026-09-30-klog.md`, 10 tasks.
- All 10 tasks are implemented and committed in one commit (`feat: implement klog tail and fetch`): `cmd/klog`, `internal/{parse,filter,merge,render,run,resolve,testutil}`, `.goreleaser.yaml`, `README.md`, opt-in `integration/kind_test.go`.
- The code was reviewed against the plan: identical except `gofmt` whitespace.
- Verified locally with Go 1.27.1 (go.mod targets 1.22): `go build`, `go vet`, `gofmt -l` clean. `go test ./... -race` passes, also with `-count=3`. `CGO_ENABLED=0` build works.

## Not verified
- klog has never run against a real `kubectl` or cluster. Every test uses the fake kubectl script in `internal/testutil`.
- `goreleaser check` and the snapshot build: goreleaser is not installed.
- The kind integration test: kind is not installed. Run it with `KLOG_IT_CONTEXT=<ctx> go test -tags integration ./integration/`.

## Settled decisions (do not re-litigate)
- Go single static binary that shells out to `kubectl` (not client-go, not stern + jq). Stdlib only.
- kubectl is the only log source in v1.
- Commands `klog tail` and `klog fetch`. Filters: `--level`, `--field key=value|!=|~regex`, `--grep`, `--exclude`.
- Packages: `internal/{resolve,run,parse,filter,merge,render}`, `cmd/klog`, GoReleaser for mac and linux.
- Out of v1: config files and presets, TUI, metrics, pluggable backend.

## Known caveats
- The retention-gap warning in `fetch` follows the spec literally: it fires for any stream whose first line is later than the requested start, so quiet or newly started pods trigger it. It goes to stderr only.
- `tail` resolves pods on its main loop, so a slow API server briefly delays output (`ponytail:` comment in `cmd/klog/tail.go`).
- Numeric log levels (for example pino `30`) are not supported. Duration flags top out at hours (`48h`, not `2d`).

## Next steps
1. Smoke test against a real cluster with a safe context and namespace: `klog tail`, `klog fetch --out`, Ctrl-C, a multi-container pod, a crash-looping pod.
2. Install goreleaser (`brew install goreleaser`), run `goreleaser check` and `goreleaser build --snapshot --clean --single-target`.
3. Optionally run the kind integration test.
4. Decide on the retention-gap warning noise, and on the module path (`klog` today) before any `go install` or release.
