package merge

import (
	"context"
	"reflect"
	"testing"
	"time"

	"klog/internal/parse"
)

func feed(lines ...parse.Line) <-chan parse.Line {
	ch := make(chan parse.Line, len(lines))
	for _, l := range lines {
		ch <- l
	}
	close(ch)
	return ch
}

func at(sec int, raw string) parse.Line {
	return parse.Line{Time: time.Unix(int64(sec), 0), Raw: raw}
}

func drain(ch <-chan parse.Line) []string {
	var out []string
	for l := range ch {
		out = append(out, l.Raw)
	}
	return out
}

func TestMergeOrdersByTimestamp(t *testing.T) {
	a := feed(at(1, "a1"), at(4, "a4"), at(5, "a5"))
	b := feed(at(2, "b2"), at(3, "b3"), at(6, "b6"))
	got := drain(Merge(context.Background(), []<-chan parse.Line{a, b}))
	want := []string{"a1", "b2", "b3", "a4", "a5", "b6"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeTiesKeepSourceOrder(t *testing.T) {
	a := feed(at(1, "a"))
	b := feed(at(1, "b"))
	got := drain(Merge(context.Background(), []<-chan parse.Line{a, b}))
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("got %v", got)
	}
}

func TestMergeZeroTimeInheritsPrevious(t *testing.T) {
	a := feed(at(1, "a1"), parse.Line{Raw: "a-notime"}, at(9, "a9"))
	b := feed(at(2, "b2"), at(3, "b3"))
	got := drain(Merge(context.Background(), []<-chan parse.Line{a, b}))
	want := []string{"a1", "a-notime", "b2", "b3", "a9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestMergeNoSourcesAndEmptySources(t *testing.T) {
	if got := drain(Merge(context.Background(), nil)); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
	got := drain(Merge(context.Background(), []<-chan parse.Line{feed(), feed(at(1, "x"))}))
	if !reflect.DeepEqual(got, []string{"x"}) {
		t.Fatalf("got %v", got)
	}
}

func TestMergeStopsOnCancel(t *testing.T) {
	never := make(chan parse.Line) // a source that never sends or closes
	ctx, cancel := context.WithCancel(context.Background())
	out := Merge(ctx, []<-chan parse.Line{never})
	cancel()
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("Merge did not stop after cancel")
	}
}
