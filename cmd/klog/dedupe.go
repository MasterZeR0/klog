package main

import (
	"klog/internal/dedupe"
	"klog/internal/parse"
	"klog/internal/render"
)

// stage returns where a stream pushes its kept lines and what it calls when the
// stream ends or is cancelled. Without --dedupe that is send itself and a no-op.
func (c common) stage(send func(parse.Line)) (push func(parse.Line), flush func()) {
	if !c.dedupe {
		return send, func() {}
	}
	// json and template output has no room for a summary line: they get a repeats count instead.
	hold := c.view.Format == render.JSON || c.view.Format == render.Template
	d := dedupe.New(hold, send)
	return d.Push, d.Flush
}
