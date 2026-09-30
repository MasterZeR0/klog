package resolve

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"klog/internal/run"
	"klog/internal/testutil"
)

func labels(s []run.Stream) []string {
	var out []string
	for _, x := range s {
		out = append(out, x.Label)
	}
	return out
}

func TestValidate(t *testing.T) {
	ok := []Target{{Selector: "app=web"}, {Deployment: "web"}, {PodRegex: "^web-"}}
	for _, tg := range ok {
		if err := tg.Validate(); err != nil {
			t.Errorf("%+v: %v", tg, err)
		}
	}
	bad := []Target{{}, {Selector: "a=b", Deployment: "web"}, {PodRegex: "("}, {Selector: "a=b", Container: "("}}
	for _, tg := range bad {
		if err := tg.Validate(); err == nil {
			t.Errorf("%+v should fail", tg)
		}
	}
}

func TestResolveSelector(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetGet("pods", testutil.PodList(
		testutil.Pod{Name: "web-2", Containers: []string{"app"}},
		testutil.Pod{Name: "web-1", Containers: []string{"app", "proxy"}, Restarts: 2},
	))
	got, err := Resolve(context.Background(), run.Kubectl{Bin: f.Bin, Context: "prod"}, Target{Namespace: "shop", Selector: "app=web"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(labels(got), []string{"web-1/app", "web-1/proxy", "web-2"}) {
		t.Fatalf("labels = %v", labels(got))
	}
	if got[0].Pod != "web-1" || got[0].Container != "app" || got[0].Restarts != 2 {
		t.Fatalf("stream = %+v", got[0])
	}
	if c := f.Calls(); len(c) != 1 || c[0] != "--context prod get pods -o json -n shop -l app=web" {
		t.Fatalf("calls = %q", c)
	}
}

func TestResolveDeployment(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetGet("deployment", `{"spec":{"selector":{"matchLabels":{"tier":"api","app":"web"}}}}`)
	f.SetGet("pods", testutil.PodList(testutil.Pod{Name: "web-1", Containers: []string{"app"}}))
	got, err := Resolve(context.Background(), run.Kubectl{Bin: f.Bin}, Target{Namespace: "shop", Deployment: "web"})
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v", got, err)
	}
	want := []string{"get deployment web -o json -n shop", "get pods -o json -n shop -l app=web,tier=api"}
	if !reflect.DeepEqual(f.Calls(), want) {
		t.Fatalf("calls = %q", f.Calls())
	}
}

func TestResolveDeploymentMatchExpressionsUnsupported(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetGet("deployment", `{"spec":{"selector":{"matchExpressions":[{"key":"a","operator":"Exists"}]}}}`)
	_, err := Resolve(context.Background(), run.Kubectl{Bin: f.Bin}, Target{Deployment: "web"})
	if err == nil || !strings.Contains(err.Error(), "matchExpressions") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolvePodRegexContainerRegexAndPending(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetGet("pods", testutil.PodList(
		testutil.Pod{Name: "web-1", Containers: []string{"app", "proxy"}},
		testutil.Pod{Name: "web-2", Phase: "Pending", Containers: []string{"app"}},
		testutil.Pod{Name: "db-1", Containers: []string{"app"}},
	))
	got, err := Resolve(context.Background(), run.Kubectl{Bin: f.Bin}, Target{PodRegex: "^web-", Container: "^app$"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(labels(got), []string{"web-1/app"}) { // label keeps the container: the pod has two
		t.Fatalf("labels = %v", labels(got))
	}
}

func TestResolveZeroPodsIsNotAnError(t *testing.T) {
	f := testutil.NewFake(t)
	f.SetGet("pods", testutil.PodList())
	got, err := Resolve(context.Background(), run.Kubectl{Bin: f.Bin}, Target{Selector: "app=none"})
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
}

func TestResolveKubectlFailure(t *testing.T) {
	f := testutil.NewFake(t) // no get-pods.json: fake prints a NotFound error
	_, err := Resolve(context.Background(), run.Kubectl{Bin: f.Bin}, Target{Selector: "app=web"})
	var ke *run.KubectlError
	if !errors.As(err, &ke) || !strings.Contains(ke.Stderr, "NotFound") {
		t.Fatalf("err = %v", err)
	}
}
