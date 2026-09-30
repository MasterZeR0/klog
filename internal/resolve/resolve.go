// Package resolve turns a target (selector, deployment or pod regex) into
// the pod containers to read logs from.
package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"klog/internal/run"
)

// Target says which pods to read. Exactly one of Selector, Deployment and
// PodRegex is set. Container is an optional regex on container names.
type Target struct {
	Namespace  string // "" means the kubeconfig context's namespace
	Selector   string
	Deployment string
	PodRegex   string
	Container  string
}

func (t Target) Validate() error {
	n := 0
	for _, s := range []string{t.Selector, t.Deployment, t.PodRegex} {
		if s != "" {
			n++
		}
	}
	if n != 1 {
		return errors.New("specify exactly one of -l (label selector), -d (deployment) or -p (pod name regex)")
	}
	if _, err := regexp.Compile(t.PodRegex); err != nil {
		return fmt.Errorf("invalid -p regex: %w", err)
	}
	if _, err := regexp.Compile(t.Container); err != nil {
		return fmt.Errorf("invalid -c regex: %w", err)
	}
	return nil
}

// Describe names the target for "no pods matched" messages.
func (t Target) Describe() string {
	ns := t.Namespace
	if ns == "" {
		ns = "(current)"
	}
	switch {
	case t.Selector != "":
		return fmt.Sprintf("namespace %s, selector %q", ns, t.Selector)
	case t.Deployment != "":
		return fmt.Sprintf("namespace %s, deployment %q", ns, t.Deployment)
	}
	return fmt.Sprintf("namespace %s, pod regex %q", ns, t.PodRegex)
}

func nsArgs(t Target) []string {
	if t.Namespace == "" {
		return nil
	}
	return []string{"-n", t.Namespace}
}

type podList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Containers []struct {
				Name string `json:"name"`
			} `json:"containers"`
		} `json:"spec"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Name         string `json:"name"`
				RestartCount int    `json:"restartCount"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

// Resolve lists the matching pods. An empty result is not an error.
func Resolve(ctx context.Context, k run.Kubectl, t Target) ([]run.Stream, error) {
	podRe, err := regexp.Compile(t.PodRegex)
	if err != nil {
		return nil, fmt.Errorf("invalid -p regex: %w", err)
	}
	contRe, err := regexp.Compile(t.Container)
	if err != nil {
		return nil, fmt.Errorf("invalid -c regex: %w", err)
	}
	selector := t.Selector
	if t.Deployment != "" {
		if selector, err = deploymentSelector(ctx, k, t); err != nil {
			return nil, err
		}
	}
	args := append([]string{"get", "pods", "-o", "json"}, nsArgs(t)...)
	if selector != "" {
		args = append(args, "-l", selector)
	}
	out, err := k.Output(ctx, args...)
	if err != nil {
		return nil, err
	}
	var list podList
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("parse kubectl output: %w", err)
	}
	sort.Slice(list.Items, func(i, j int) bool { return list.Items[i].Metadata.Name < list.Items[j].Metadata.Name })

	var streams []run.Stream
	for _, p := range list.Items {
		name := p.Metadata.Name
		if p.Status.Phase == "Pending" || !podRe.MatchString(name) {
			continue
		}
		restarts := map[string]int{}
		for _, cs := range p.Status.ContainerStatuses {
			restarts[cs.Name] = cs.RestartCount
		}
		for _, c := range p.Spec.Containers {
			if !contRe.MatchString(c.Name) {
				continue
			}
			label := name
			if len(p.Spec.Containers) > 1 {
				label = name + "/" + c.Name
			}
			streams = append(streams, run.Stream{Pod: name, Container: c.Name, Label: label, Restarts: restarts[c.Name]})
		}
	}
	return streams, nil
}

func deploymentSelector(ctx context.Context, k run.Kubectl, t Target) (string, error) {
	args := append([]string{"get", "deployment", t.Deployment, "-o", "json"}, nsArgs(t)...)
	out, err := k.Output(ctx, args...)
	if err != nil {
		return "", err
	}
	var d struct {
		Spec struct {
			Selector struct {
				MatchLabels      map[string]string `json:"matchLabels"`
				MatchExpressions []json.RawMessage `json:"matchExpressions"`
			} `json:"selector"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(out, &d); err != nil {
		return "", fmt.Errorf("parse kubectl output: %w", err)
	}
	sel := d.Spec.Selector
	if len(sel.MatchExpressions) > 0 {
		return "", fmt.Errorf("deployment %q uses matchExpressions, which klog does not support; use -l", t.Deployment)
	}
	if len(sel.MatchLabels) == 0 {
		return "", fmt.Errorf("deployment %q has no matchLabels selector", t.Deployment)
	}
	parts := make([]string, 0, len(sel.MatchLabels))
	for k, v := range sel.MatchLabels {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return strings.Join(parts, ","), nil
}
