package discovery

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

// prefix of the annotations that adjust a discovered monitor.
const prefix = "upti.my"

type objectList struct {
	Items []object `json:"items"`
}

type object struct {
	Metadata struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		UID         string            `json:"uid"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	} `json:"metadata"`
	Spec json.RawMessage `json:"spec"`
}

// options are the annotations, parsed.
type options struct {
	name, path, port, typ, scheme string
	interval, grace               int // seconds
	expectedStatus, keyword       string
	failureThreshold              int
	recoveryThreshold             int
}

func parseOptions(o object) (options, error) {
	a := func(key string) string { return strings.TrimSpace(o.Metadata.Annotations[prefix+"/"+key]) }
	opts := options{
		name: a("name"), path: a("path"), port: a("port"), typ: a("type"), scheme: a("scheme"),
		expectedStatus: a("expected-status"), keyword: a("keyword"),
	}
	for key, dst := range map[string]*int{"interval": &opts.interval, "grace": &opts.grace} {
		if v := a(key); v != "" {
			d, err := time.ParseDuration(v)
			if err != nil {
				return opts, fmt.Errorf("%s/%s: %w", prefix, key, err)
			}
			*dst = int(d.Seconds())
		}
	}
	for key, dst := range map[string]*int{"failure-threshold": &opts.failureThreshold, "recovery-threshold": &opts.recoveryThreshold} {
		if v := a(key); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 10 {
				return opts, fmt.Errorf("%s/%s must be a number from 1 to 10", prefix, key)
			}
			*dst = n
		}
	}
	if opts.path != "" && !strings.HasPrefix(opts.path, "/") {
		opts.path = "/" + opts.path
	}
	switch opts.scheme {
	case "", "http", "https":
	default:
		return opts, fmt.Errorf("%s/scheme must be http or https", prefix)
	}
	return opts, nil
}

func (opts options) httpCheck(target string) *monitor.Check {
	return &monitor.Check{
		Type: monitor.TypeHTTP, Target: target, IntervalSeconds: opts.interval,
		Config: monitor.Config{ExpectedStatus: opts.expectedStatus, Keyword: opts.keyword},
	}
}

// healthcheck is a discovered healthcheck, with the annotations' thresholds.
func (opts options) healthcheck(name string, c *monitor.Check) monitor.Monitor {
	c.FailureThreshold, c.RecoveryThreshold = opts.failureThreshold, opts.recoveryThreshold
	return monitor.Monitor{Kind: monitor.KindHealthcheck, Name: name, Check: c}
}

type servicePort struct {
	Name        string      `json:"name"`
	Port        int         `json:"port"`
	TargetPort  intOrString `json:"targetPort"`
	AppProtocol string      `json:"appProtocol"`
}

// fromService checks a Service. By default the agent sends its own request
// to the Service's cluster DNS name: HTTP on the pods' readinessProbe path
// when they have one, or when the port looks like HTTP, otherwise a TCP
// connect. With upti.my/type: kubernetes it reuses the kubelet's
// readinessProbes instead: up while the Service has a ready endpoint.
func (d *Discoverer) fromService(ctx context.Context, o object) ([]monitor.Monitor, error) {
	opts, err := parseOptions(o)
	if err != nil {
		return nil, err
	}
	var spec struct {
		Selector map[string]string `json:"selector"`
		Ports    []servicePort     `json:"ports"`
	}
	if err := json.Unmarshal(o.Spec, &spec); err != nil {
		return nil, err
	}
	name := opts.name
	if name == "" {
		name = o.Metadata.Namespace + "/" + o.Metadata.Name
	}

	typ := opts.typ
	if typ == "kubernetes" {
		target := o.Metadata.Namespace + "/service/" + o.Metadata.Name
		return []monitor.Monitor{opts.healthcheck(name, &monitor.Check{Type: monitor.TypeKubernetes, Target: target, IntervalSeconds: opts.interval})}, nil
	}

	if len(spec.Ports) == 0 {
		return nil, errors.New("has no ports")
	}
	p := spec.Ports[0]
	if opts.port != "" {
		i := slices.IndexFunc(spec.Ports, func(p servicePort) bool {
			return p.Name == opts.port || strconv.Itoa(p.Port) == opts.port
		})
		if i < 0 {
			return nil, fmt.Errorf("%s/port %q isn't one of its ports", prefix, opts.port)
		}
		p = spec.Ports[i]
	}
	host := net.JoinHostPort(o.Metadata.Name+"."+o.Metadata.Namespace+".svc", strconv.Itoa(p.Port))

	var rp probe
	if (typ == "" || typ == "http") && (opts.path == "" || opts.scheme == "") {
		if rp, err = d.readinessProbe(ctx, o, spec.Selector, p); err != nil {
			return nil, err
		}
	}
	if typ == "" {
		typ = "tcp"
		if opts.path != "" || opts.scheme != "" || rp.path != "" || isHTTP(p) {
			typ = "http"
		}
	}
	switch typ {
	case "tcp":
		return []monitor.Monitor{opts.healthcheck(name, &monitor.Check{Type: monitor.TypeTCP, Target: host, IntervalSeconds: opts.interval})}, nil
	case "http":
		scheme := cmp.Or(opts.scheme, rp.scheme)
		if scheme == "" {
			scheme = "http"
			if isHTTPS(p) {
				scheme = "https"
			}
		}
		path := cmp.Or(opts.path, rp.path, "/")
		return []monitor.Monitor{opts.healthcheck(name, opts.httpCheck(scheme+"://"+host+path))}, nil
	}
	return nil, fmt.Errorf("%s/type must be http, tcp or kubernetes", prefix)
}

// isHTTP: the port's appProtocol or name says HTTP (Istio-style names like
// http-web count), or it's a usual HTTP port.
func isHTTP(p servicePort) bool {
	switch strings.ToLower(p.AppProtocol) {
	case "http", "https", "kubernetes.io/h2c", "kubernetes.io/ws", "kubernetes.io/wss":
		return true
	}
	n := strings.ToLower(p.Name)
	if n == "http" || n == "https" || n == "web" || strings.HasPrefix(n, "http-") || strings.HasPrefix(n, "https-") {
		return true
	}
	return p.Port == 80 || p.Port == 443 || p.Port == 8080
}

func isHTTPS(p servicePort) bool {
	n := strings.ToLower(p.Name)
	return strings.EqualFold(p.AppProtocol, "https") || strings.EqualFold(p.AppProtocol, "kubernetes.io/wss") ||
		n == "https" || strings.HasPrefix(n, "https-") || p.Port == 443
}

// fromIngress checks each of an Ingress's hostnames from the outside, over
// HTTPS when the Ingress has TLS for it.
func fromIngress(_ *Discoverer, _ context.Context, o object) ([]monitor.Monitor, error) {
	var spec struct {
		Rules []struct {
			Host string `json:"host"`
		} `json:"rules"`
		TLS []struct {
			Hosts []string `json:"hosts"`
		} `json:"tls"`
	}
	if err := json.Unmarshal(o.Spec, &spec); err != nil {
		return nil, err
	}
	var hosts []string
	for _, r := range spec.Rules {
		hosts = append(hosts, r.Host)
	}
	tls := map[string]bool{}
	for _, t := range spec.TLS {
		for _, h := range t.Hosts {
			tls[h] = true
		}
	}
	return fromHosts(o, hosts, func(h string) string {
		if tls[h] {
			return "https"
		}
		return "http"
	})
}

// fromHTTPRoute checks each of an HTTPRoute's hostnames. Whether the
// Gateway terminates TLS isn't on the route, so it assumes HTTPS; set
// upti.my/scheme: http otherwise.
func fromHTTPRoute(_ *Discoverer, _ context.Context, o object) ([]monitor.Monitor, error) {
	var spec struct {
		Hostnames []string `json:"hostnames"`
	}
	if err := json.Unmarshal(o.Spec, &spec); err != nil {
		return nil, err
	}
	return fromHosts(o, spec.Hostnames, func(string) string { return "https" })
}

func fromHosts(o object, hosts []string, scheme func(host string) string) ([]monitor.Monitor, error) {
	opts, err := parseOptions(o)
	if err != nil {
		return nil, err
	}
	if opts.typ != "" && opts.typ != "http" {
		return nil, fmt.Errorf("%s/type can only be http here", prefix)
	}
	path := opts.path
	if path == "" {
		path = "/"
	}
	var usable []string
	for _, h := range hosts {
		// No host is a catch-all and a wildcard isn't one address: neither
		// can be checked.
		if h != "" && !strings.Contains(h, "*") && !slices.Contains(usable, h) {
			usable = append(usable, h)
		}
	}
	if len(usable) == 0 {
		return nil, errors.New("has no hostname to check (catch-all and wildcard hosts are skipped)")
	}
	var out []monitor.Monitor
	for _, h := range usable {
		s := opts.scheme
		if s == "" {
			s = scheme(h)
		}
		name := h
		if path != "/" {
			name += path
		}
		if opts.name != "" {
			name = opts.name
			if len(usable) > 1 {
				name += " (" + h + ")"
			}
		}
		m := opts.healthcheck(name, opts.httpCheck(s+"://"+h+path))
		m.SourceRef = "#" + h // one monitor per hostname
		out = append(out, m)
	}
	return out, nil
}

// fromWorkload checks that a workload has all its replicas ready.
func fromWorkload(kind string) func(*Discoverer, context.Context, object) ([]monitor.Monitor, error) {
	return func(_ *Discoverer, _ context.Context, o object) ([]monitor.Monitor, error) {
		opts, err := parseOptions(o)
		if err != nil {
			return nil, err
		}
		if opts.typ != "" && opts.typ != "kubernetes" {
			return nil, fmt.Errorf("%s/type can only be kubernetes here", prefix)
		}
		name := opts.name
		if name == "" {
			name = fmt.Sprintf("%s/%s (%s)", o.Metadata.Namespace, o.Metadata.Name, kind)
		}
		target := o.Metadata.Namespace + "/" + kind + "/" + o.Metadata.Name
		return []monitor.Monitor{opts.healthcheck(name, &monitor.Check{Type: monitor.TypeKubernetes, Target: target, IntervalSeconds: opts.interval})}, nil
	}
}

// fromCronJob makes a heartbeat on the CronJob's schedule. Its runs are
// read from the CronJob's Jobs (see jobs.go), so the job needs no ping.
func fromCronJob(_ *Discoverer, _ context.Context, o object) ([]monitor.Monitor, error) {
	opts, err := parseOptions(o)
	if err != nil {
		return nil, err
	}
	if opts.typ != "" && opts.typ != "heartbeat" {
		return nil, fmt.Errorf("%s/type can only be heartbeat here", prefix)
	}
	var spec struct {
		Schedule    string  `json:"schedule"`
		TimeZone    *string `json:"timeZone"`
		Suspend     bool    `json:"suspend"`
		JobTemplate struct {
			Spec struct {
				ActiveDeadlineSeconds *int `json:"activeDeadlineSeconds"`
			} `json:"spec"`
		} `json:"jobTemplate"`
	}
	if err := json.Unmarshal(o.Spec, &spec); err != nil {
		return nil, err
	}
	name := opts.name
	if name == "" {
		name = fmt.Sprintf("%s/%s (cronjob)", o.Metadata.Namespace, o.Metadata.Name)
	}
	h := monitor.Heartbeat{Token: monitor.NewToken(), Cron: spec.Schedule, Timezone: "UTC"}
	if spec.TimeZone != nil && *spec.TimeZone != "" {
		h.Timezone = *spec.TimeZone
	}
	h.GraceSeconds = opts.grace
	if h.GraceSeconds == 0 {
		h.GraceSeconds = cronJobGrace(h, spec.JobTemplate.Spec.ActiveDeadlineSeconds)
	}
	return []monitor.Monitor{{Kind: monitor.KindHeartbeat, Name: name, Paused: spec.Suspend, Heartbeat: &h}}, nil
}

// cronJobGrace is how long after its scheduled time a run may finish. A
// run is only done when its Job completes, so the default leaves time for
// the job itself: until Kubernetes would stop it (activeDeadlineSeconds), or
// else until the next run is due (the shortest gap between runs), at most
// an hour.
func cronJobGrace(h monitor.Heartbeat, activeDeadline *int) int {
	if activeDeadline != nil && *activeDeadline > 0 {
		return *activeDeadline + 60 // plus a minute to start the pod
	}
	// The shortest gap over the next runs, so an irregular schedule (every
	// 20 minutes during the day) gets the same grace on every scan.
	runs := h.Upcoming(time.Now(), 20)
	if len(runs) < 2 {
		return 0 // an invalid schedule; Normalize reports it
	}
	gap := time.Hour
	for i := 1; i < len(runs); i++ {
		gap = min(gap, runs[i].Sub(runs[i-1]))
	}
	return int(max(gap, time.Minute).Seconds())
}
