package checks

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/uptimy/agent/internal/kube"
	"github.com/uptimy/agent/internal/monitor"
)

func init() {
	register(monitor.TypeKubernetes, func(ctx context.Context, c *Checker, m monitor.Check) Outcome {
		if c.kube == nil {
			return Outcome{Message: "not running inside Kubernetes (no service account found)"}
		}
		ns, kind, name, err := monitor.ParseKubernetesTarget(m.Target)
		if err != nil {
			return Outcome{Message: err.Error()}
		}
		if kind == "service" {
			return checkService(ctx, c.kube, ns, name)
		}
		return checkWorkload(ctx, c.kube, ns, kind, name)
	})
}

// apiFailure turns an API error into an outcome, or reports ok when there
// was none.
func apiFailure(err error, kind, ns, name string) (Outcome, bool) {
	var se *kube.StatusError
	switch {
	case err == nil:
		return Outcome{}, false
	case kube.IsNotFound(err):
		return Outcome{Message: fmt.Sprintf("%s %s/%s not found", kind, ns, name), ErrorClass: classAssertion}, true
	case kube.IsForbidden(err):
		return Outcome{Message: "forbidden: the agent's service account can't read " + kind + "s (check RBAC)"}, true
	case errors.As(err, &se):
		return Outcome{Message: se.Error()}, true
	}
	return failure(err), true
}

type workloadStatus struct {
	Spec struct {
		Replicas *int `json:"replicas"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas          int `json:"readyReplicas"`
		AvailableReplicas      int `json:"availableReplicas"`
		DesiredNumberScheduled int `json:"desiredNumberScheduled"`
		NumberReady            int `json:"numberReady"`
	} `json:"status"`
}

// checkWorkload reports whether a workload has all its replicas ready.
func checkWorkload(ctx context.Context, k *kube.Client, ns, kind, name string) Outcome {
	var w workloadStatus
	err := k.Get(ctx, fmt.Sprintf("/apis/apps/v1/namespaces/%s/%ss/%s", ns, kind, name), &w)
	if out, failed := apiFailure(err, kind, ns, name); failed {
		return out
	}
	var ready, desired int
	switch kind {
	case "deployment":
		desired, ready = 1, w.Status.AvailableReplicas
		if w.Spec.Replicas != nil {
			desired = *w.Spec.Replicas
		}
	case "statefulset":
		desired, ready = 1, w.Status.ReadyReplicas
		if w.Spec.Replicas != nil {
			desired = *w.Spec.Replicas
		}
	case "daemonset":
		desired, ready = w.Status.DesiredNumberScheduled, w.Status.NumberReady
	}
	msg := fmt.Sprintf("%d/%d ready", ready, desired)
	if desired == 0 {
		return Outcome{OK: true, Message: "scaled to zero"}
	}
	if ready < desired {
		return Outcome{Message: msg, ErrorClass: classAssertion}
	}
	return Outcome{OK: true, Message: msg}
}

type endpointSlices struct {
	Items []struct {
		Endpoints []struct {
			Addresses  []string `json:"addresses"`
			Conditions struct {
				Ready *bool `json:"ready"`
			} `json:"conditions"`
			TargetRef *struct {
				UID string `json:"uid"`
			} `json:"targetRef"`
		} `json:"endpoints"`
	} `json:"items"`
}

// checkService reports whether a Service has a ready endpoint: a pod whose
// readinessProbe passes. It reuses the kubelet's probes instead of sending
// the app a request of its own.
func checkService(ctx context.Context, k *kube.Client, ns, name string) Outcome {
	var l endpointSlices
	selector := url.QueryEscape("kubernetes.io/service-name=" + name)
	err := k.Get(ctx, fmt.Sprintf("/apis/discovery.k8s.io/v1/namespaces/%s/endpointslices?labelSelector=%s", ns, selector), &l)
	if out, failed := apiFailure(err, "endpointslice", ns, name); failed {
		return out
	}
	// A dual-stack Service lists each pod once per address family.
	ready, total := map[string]bool{}, map[string]bool{}
	for _, s := range l.Items {
		for _, e := range s.Endpoints {
			key := ""
			if e.TargetRef != nil {
				key = e.TargetRef.UID
			}
			if key == "" && len(e.Addresses) > 0 {
				key = e.Addresses[0]
			}
			total[key] = true
			if e.Conditions.Ready == nil || *e.Conditions.Ready { // unset means ready
				ready[key] = true
			}
		}
	}
	if len(total) > 0 {
		msg := fmt.Sprintf("%d/%d endpoints ready", len(ready), len(total))
		if len(ready) == 0 {
			return Outcome{Message: msg, ErrorClass: classAssertion}
		}
		return Outcome{OK: true, Message: msg}
	}
	// No endpoints at all: say why.
	var svc struct {
		Spec struct {
			Type string `json:"type"`
		} `json:"spec"`
	}
	err = k.Get(ctx, fmt.Sprintf("/api/v1/namespaces/%s/services/%s", ns, name), &svc)
	if out, failed := apiFailure(err, "service", ns, name); failed {
		return out
	}
	if svc.Spec.Type == "ExternalName" {
		return Outcome{Message: "an ExternalName Service has no endpoints; check its hostname with an HTTP or TCP check", ErrorClass: classAssertion}
	}
	return Outcome{Message: "no endpoints: no running pod matches the Service's selector", ErrorClass: classAssertion}
}
