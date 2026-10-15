// Package filesync loads healthchecks and heartbeats from YAML, so they can
// be managed in Git (a ConfigMap, a mounted file, or an env var on a PaaS).
// See examples/monitors.yaml for the format.
package filesync

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/uptimy/agent/internal/managed"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// File is the YAML document.
type File struct {
	Healthchecks []HealthcheckEntry `yaml:"healthchecks"`
	Heartbeats   []HeartbeatEntry   `yaml:"heartbeats"`
}

// common holds what both kinds share. Whether a monitor is on the status
// page, and where, is set in the status page editor, not here.
type common struct {
	Name   string `yaml:"name"`
	Paused bool   `yaml:"paused"`
}

// HealthcheckEntry is a healthcheck in YAML.
type HealthcheckEntry struct {
	common            `yaml:",inline"`
	Type              monitor.Type   `yaml:"type"`
	Target            string         `yaml:"target"`
	Interval          string         `yaml:"interval"` // e.g. 30s
	Timeout           string         `yaml:"timeout"`
	FailureThreshold  int            `yaml:"failure_threshold"`
	RecoveryThreshold int            `yaml:"recovery_threshold"`
	Config            monitor.Config `yaml:"config"`
}

// HeartbeatEntry is a heartbeat in YAML: every or cron sets the schedule.
type HeartbeatEntry struct {
	common   `yaml:",inline"`
	Every    string `yaml:"every"` // e.g. 5m
	Cron     string `yaml:"cron"`  // e.g. "0 3 * * *"
	Timezone string `yaml:"timezone"`
	Grace    string `yaml:"grace"` // e.g. 30m
	// Token pins the ping URL, e.g. so a cron job's config can be written
	// before the agent runs. Without it the agent picks one.
	Token string `yaml:"token"`
}

// Desired is a monitor as the file describes it.
type Desired = managed.Desired

// Parse decodes and validates YAML.
func Parse(data []byte) ([]Desired, error) {
	out, problems := Check(data)
	if len(problems) > 0 {
		return nil, problems[0]
	}
	return out, nil
}

// Check decodes and validates YAML like Parse, but reports every problem,
// for `uptimy-agent validate`. The monitors are those without problems.
func Check(data []byte) ([]Desired, []error) {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelled setting is an error, not silently ignored
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return nil, []error{err}
	}
	var problems []error
	out := make([]Desired, 0, len(f.Healthchecks)+len(f.Heartbeats))
	for i, e := range f.Healthchecks {
		m, err := e.monitor()
		if err != nil {
			problems = append(problems, fmt.Errorf("healthcheck %d (%s): %w", i+1, e.Name, err))
			continue
		}
		out = append(out, Desired{Monitor: m})
	}
	for i, e := range f.Heartbeats {
		m, err := e.monitor()
		if err != nil {
			problems = append(problems, fmt.Errorf("heartbeat %d (%s): %w", i+1, e.Name, err))
			continue
		}
		out = append(out, Desired{Monitor: m, GeneratedToken: e.Token == ""})
	}
	seen := map[string]bool{}
	names := make([]string, 0, len(f.Healthchecks)+len(f.Heartbeats))
	for _, e := range f.Healthchecks {
		names = append(names, strings.TrimSpace(e.Name))
	}
	for _, e := range f.Heartbeats {
		names = append(names, strings.TrimSpace(e.Name))
	}
	for _, name := range names {
		if seen[name] && name != "" {
			problems = append(problems, fmt.Errorf("two monitors are named %q; names must be unique", name))
		}
		seen[name] = true
	}
	return out, problems
}

func (c common) monitor(kind monitor.Kind) monitor.Monitor {
	return monitor.Monitor{Kind: kind, Name: c.Name, Paused: c.Paused, Source: monitor.SourceFile}
}

func (e HealthcheckEntry) monitor() (monitor.Monitor, error) {
	m := e.common.monitor(monitor.KindHealthcheck)
	c := monitor.Check{Type: e.Type, Target: e.Target, FailureThreshold: e.FailureThreshold, RecoveryThreshold: e.RecoveryThreshold, Config: e.Config}
	var err error
	if c.IntervalSeconds, err = seconds(e.Interval); err != nil {
		return m, fmt.Errorf("interval: %w", err)
	}
	if c.TimeoutSeconds, err = seconds(e.Timeout); err != nil {
		return m, fmt.Errorf("timeout: %w", err)
	}
	m.Check = &c
	return m, m.Normalize()
}

func (e HeartbeatEntry) monitor() (monitor.Monitor, error) {
	m := e.common.monitor(monitor.KindHeartbeat)
	h := monitor.Heartbeat{Token: e.Token, Cron: e.Cron, Timezone: e.Timezone}
	if h.Token == "" {
		h.Token = monitor.NewToken()
	}
	var err error
	if h.EverySeconds, err = seconds(e.Every); err != nil {
		return m, fmt.Errorf("every: %w", err)
	}
	if h.GraceSeconds, err = seconds(e.Grace); err != nil {
		return m, fmt.Errorf("grace: %w", err)
	}
	m.Heartbeat = &h
	return m, m.Normalize()
}

func seconds(s string) (int, error) {
	if s == "" {
		return 0, nil // Normalize applies the default
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	return int(d.Seconds()), nil
}

// Sync makes the file-managed monitors in the store match desired. It runs
// at startup, before the scheduler loads monitors.
func Sync(ctx context.Context, st *store.Store, desired []Desired) (created, updated, deleted int, err error) {
	ch, err := managed.Sync(ctx, st, monitor.SourceFile, desired, managed.Options{})
	return ch.Created, ch.Updated, len(ch.Deleted), err
}
