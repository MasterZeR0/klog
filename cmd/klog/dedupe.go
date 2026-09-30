package main

import (
	"errors"

	"klog/internal/dedupe"
	"klog/internal/parse"
	"klog/internal/render"
)

// checkDedupe rejects --dedupe with --format raw: raw output is the log text
// only, so it has no room for a summary line or a repeat count.
func (c common) checkDedupe() error {
	if c.dedupe && c.view.Format == render.Raw {
		return errors.New("--dedupe needs --format pretty, json or template")
	}
	return nil
}

// stage returns where a stream pushes its kept lines and what it calls when the
// stream ends or is cancelled. Without --dedupe that is send itself and a no-op.
func (c common) stage(send func(parse.Line)) (push func(parse.Line), flush func()) {
	if !c.dedupe {
		return send, func() {}
	}
	// json and template output has no room for a summary line: a run of repeats
	// ends with a record carrying a repeats count instead.
	structured := c.view.Format == render.JSON || c.view.Format == render.Template
	d := dedupe.New(structured, send)
	return d.Push, d.Flush
}
