// Package merge interleaves per-pod line streams by timestamp.
package merge

import (
	"container/heap"
	"context"
	"time"

	"klog/internal/parse"
)

type item struct {
	line parse.Line
	key  time.Time // ordering key: the line time, or the source's previous time
	src  int
	seq  uint64 // arrival order, breaks ties
}

type itemHeap []item

func (h itemHeap) Len() int { return len(h) }
func (h itemHeap) Less(i, j int) bool {
	if !h[i].key.Equal(h[j].key) {
		return h[i].key.Before(h[j].key)
	}
	return h[i].seq < h[j].seq
}
func (h itemHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *itemHeap) Push(x any)   { *h = append(*h, x.(item)) }
func (h *itemHeap) Pop() any {
	old := *h
	it := old[len(old)-1]
	*h = old[:len(old)-1]
	return it
}

// Merge k-way merges time-ordered sources. It holds one pending line per
// source, so memory is bounded by the source count, not the log size.
func Merge(ctx context.Context, srcs []<-chan parse.Line) <-chan parse.Line {
	out := make(chan parse.Line)
	go func() {
		defer close(out)
		h := &itemHeap{}
		last := make([]time.Time, len(srcs))
		var seq uint64
		pull := func(i int) {
			select {
			case l, ok := <-srcs[i]:
				if !ok {
					return
				}
				key := l.Time
				if key.IsZero() {
					key = last[i]
				} else {
					last[i] = key
				}
				seq++
				heap.Push(h, item{line: l, key: key, src: i, seq: seq})
			case <-ctx.Done():
			}
		}
		for i := range srcs {
			pull(i)
		}
		for h.Len() > 0 {
			it := heap.Pop(h).(item)
			select {
			case out <- it.line:
			case <-ctx.Done():
				return
			}
			pull(it.src)
		}
	}()
	return out
}
