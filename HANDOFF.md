# klog handoff

Paste this into a new agent session opened in `~/Projects/klog`, or tell it: "Read HANDOFF.md and continue."

## Goal
Go CLI that tails and fetches logs from many Kubernetes pods at once, with filters for level, JSON fields and text. Standalone, usable across services.

## State
- Design approved section by section with the user (CLI surface and components; data flow, error handling, testing).
- Spec written: `docs/superpowers/specs/2026-09-30-klog-design.md`. Read it first.
- `git init` done. Nothing committed. The user has NOT yet approved the written spec.

## Settled decisions (do not re-litigate)
- Go single static binary that shells out to `kubectl` (not client-go, not stern + jq).
- kubectl is the only log source in v1.
- Commands `klog tail` and `klog fetch`. Filters: `--level`, `--field key=value|!=|~regex`, `--grep`, `--exclude`.
- Packages: `internal/{resolve,run,parse,filter,merge,render}`, `cmd/klog`, GoReleaser for mac and linux.
- Out of v1: config files and presets, TUI, metrics, pluggable backend.

## Next steps
1. Ask the user to review the spec. Offer to commit it; do not commit unless they say yes.
2. After approval, use the `superpowers:writing-plans` skill to write the implementation plan, then let the user pick the execution method.
3. No product code before the plan is approved.
