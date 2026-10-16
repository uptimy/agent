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
	"slices"
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
	c := check(data)
	return c.desired, c.problems
}

// checked is a decoded and validated file.
type checked struct {
	desired  []Desired
	problems []error
	// invalid are the names of the monitors with problems.
	invalid []string
	// whole: the problems are the file's, not single monitors': YAML that
	// doesn't decode, or two monitors with one name.
	whole bool
}

func check(data []byte) checked {
	var f File
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // a misspelled setting is an error, not silently ignored
	if err := dec.Decode(&f); err != nil && !errors.Is(err, io.EOF) {
		return checked{problems: []error{err}, whole: true}
	}
	var c checked
	c.desired = make([]Desired, 0, len(f.Healthchecks)+len(f.Heartbeats))
	for i, e := range f.Healthchecks {
		m, err := e.monitor()
		if err != nil {
			c.problems = append(c.problems, fmt.Errorf("healthcheck %d (%s): %w", i+1, e.Name, err))
			c.invalid = append(c.invalid, strings.TrimSpace(e.Name))
			continue
		}
		c.desired = append(c.desired, Desired{Monitor: m})
	}
	for i, e := range f.Heartbeats {
		m, err := e.monitor()
		if err != nil {
			c.problems = append(c.problems, fmt.Errorf("heartbeat %d (%s): %w", i+1, e.Name, err))
			c.invalid = append(c.invalid, strings.TrimSpace(e.Name))
			continue
		}
		c.desired = append(c.desired, Desired{Monitor: m, GeneratedToken: e.Token == ""})
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
			c.problems = append(c.problems, fmt.Errorf("two monitors are named %q; names must be unique", name))
			c.whole = true
		}
		seen[name] = true
	}
	return c
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

// Result is what Apply did with a monitors file.
type Result struct {
	Created, Updated, Deleted int
	// Held: the file as a whole couldn't be used, so nothing changed.
	Held bool
	// Problems are why it was held, or the monitors that were skipped.
	Problems []error
}

// Apply validates a monitors file and syncs the file-managed monitors to as
// much of it as is valid, so a mistake in the file never stops the agent:
//
//   - YAML that doesn't decode (a syntax error, a misspelled setting) or two
//     monitors with one name: the whole file is held back and the monitors
//     stay as the last good file left them.
//   - A monitor with a problem is skipped: if the file had it before, it keeps
//     its last good settings (and its history); if it's new, it isn't
//     created. The other monitors are applied.
//
// The error is the store's.
func Apply(ctx context.Context, st *store.Store, data []byte) (Result, error) {
	c := check(data)
	if c.whole {
		return Result{Held: true, Problems: c.problems}, nil
	}
	desired := c.desired
	if len(c.invalid) > 0 {
		existing, err := st.ListMonitors(ctx)
		if err != nil {
			return Result{}, err
		}
		for _, m := range existing {
			if m.Source == monitor.SourceFile && slices.Contains(c.invalid, m.Name) {
				desired = append(desired, Desired{Monitor: m}) // unchanged
			}
		}
	}
	created, updated, deleted, err := Sync(ctx, st, desired)
	return Result{Created: created, Updated: updated, Deleted: deleted, Problems: c.problems}, err
}

// Problem is what went wrong the last time the monitors file was applied,
// for the UI.
type Problem struct {
	// File is the file's name, or MONITORS_YAML.
	File string `json:"file"`
	// Applied: the file's valid monitors were applied and Errors are the
	// ones skipped. Otherwise the file was held back and nothing changed.
	Applied bool     `json:"applied"`
	Errors  []string `json:"errors"`
}
