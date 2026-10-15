// Package checks implements the healthcheck probes, one file per check
// type. Each registers itself with register; see CONTRIBUTING.md.
package checks

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/kube"
	"github.com/uptimy/agent/internal/monitor"
)

// Outcome is what a probe reports; the scheduler turns it into a Result.
type Outcome struct {
	OK      bool
	Message string
	// Latency overrides the probe's wall time as the result's latency, for
	// probes whose wall time isn't the answer's (ping sends several).
	Latency time.Duration
	// StatusCode is the HTTP status an HTTP check got, if it got one.
	StatusCode int
	// ErrorClass is why it failed (see errorclass.go); Run fills in one for
	// failures that don't say.
	ErrorClass string
}

// Checker runs probes. It is safe for concurrent use.
type Checker struct {
	secure   *http.Client
	insecure *http.Client
	kube     *kube.Client // nil when not running inside Kubernetes
}

// New returns a Checker. k may be nil.
func New(k *kube.Client) *Checker {
	mk := func(skipVerify bool) *http.Client {
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.TLSClientConfig = &tls.Config{InsecureSkipVerify: skipVerify} //nolint:gosec // opt-in per monitor
		t.MaxIdleConnsPerHost = 2
		return &http.Client{Transport: t}
	}
	return &Checker{secure: mk(false), insecure: mk(true), kube: k}
}

// Probe checks one monitor. The context carries the monitor's timeout.
type Probe func(ctx context.Context, c *Checker, m monitor.Check) Outcome

var probes = map[monitor.Type]Probe{}

// register adds the probe for a monitor type; each probe file calls it from
// init. A type's settings and validation live in the monitor package
// (monitor.Register); this is the part that touches the network.
func register(t monitor.Type, p Probe) {
	if _, dup := probes[t]; dup {
		panic(fmt.Sprintf("checks: probe for %q registered twice", t))
	}
	probes[t] = p
}

// Run executes the probe for a healthcheck. The context carries its timeout.
func (c *Checker) Run(ctx context.Context, m monitor.Check) Outcome {
	p, ok := probes[m.Type]
	if !ok {
		return Outcome{Message: fmt.Sprintf("unsupported monitor type %q", m.Type), ErrorClass: classOther}
	}
	out := p(ctx, c, m)
	if !out.OK {
		// Out of time is a timeout, whichever step it cut short.
		if ctx.Err() == context.DeadlineExceeded {
			out.ErrorClass = classTimeout
		} else if out.ErrorClass == "" {
			out.ErrorClass = classOther
		}
	}
	return out
}

// trimErr shortens noisy wrapped errors from net/http for display.
func trimErr(err error) string {
	msg := err.Error()
	if i := strings.LastIndex(msg, ": dial tcp"); i >= 0 {
		msg = msg[i+2:]
	}
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
