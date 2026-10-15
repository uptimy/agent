// Package monitor defines what the agent watches.
//
// A monitor is either a healthcheck, which the agent probes on an interval
// (check.go), or a heartbeat, which a scheduled job pings when it runs
// (heartbeat.go). Both share a name, a place on the status page, events and
// alerts; their settings and history are their own.
package monitor

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Kind says what a monitor is.
type Kind string

const (
	// KindHealthcheck: the agent probes a target (a website, a database, ...).
	KindHealthcheck Kind = "healthcheck"
	// KindHeartbeat: a scheduled job pings the agent each time it runs.
	KindHeartbeat Kind = "heartbeat"
)

// Status is a monitor's state, common to both kinds: what dashboards, the
// status page and alerts work with. Heartbeats have a more specific
// HeartbeatState that maps onto it.
type Status string

const (
	StatusPending Status = "pending"
	StatusUp      Status = "up"
	StatusDown    Status = "down"
	StatusPaused  Status = "paused"
)

// Source records where a monitor was defined.
const (
	SourceUI   = "ui"
	SourceFile = "file"
	// SourceKubernetes: discovered from a labeled Service, Ingress,
	// HTTPRoute or workload (internal/discovery).
	SourceKubernetes = "kubernetes"
)

// Monitor is a healthcheck or a heartbeat. Exactly one of Check and
// Heartbeat is set, matching Kind.
type Monitor struct {
	ID     int64  `json:"id"`
	Kind   Kind   `json:"kind"`
	Name   string `json:"name"`
	Paused bool   `json:"paused"`
	Source string `json:"source"`
	// SourceRef is what the source made it from, for monitors that aren't
	// from the UI or the file: "service/shop/checkout". It identifies the
	// monitor across renames.
	SourceRef string `json:"source_ref,omitempty"`

	// On the status page.
	Public        bool   `json:"public"`
	StatusLabel   string `json:"status_label"` // public name; empty = Name
	StatusOrder   int    `json:"status_order"`
	StatusSection string `json:"status_section"`

	Check     *Check     `json:"check,omitempty"`
	Heartbeat *Heartbeat `json:"heartbeat,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PublicName is how the monitor is labeled on the public status page.
func (m Monitor) PublicName() string {
	if m.StatusLabel != "" {
		return m.StatusLabel
	}
	return m.Name
}

// Describe says what the monitor watches, for lists and alerts: the target
// (password masked) for a healthcheck, the schedule for a heartbeat.
func (m Monitor) Describe() string {
	switch {
	case m.Check != nil:
		return m.Check.DisplayTarget()
	case m.Heartbeat != nil:
		return m.Heartbeat.Describe()
	}
	return ""
}

// Normalize fills defaults and validates the monitor. It must be called before
// a monitor is stored.
func (m *Monitor) Normalize() error {
	m.Name = strings.TrimSpace(m.Name)
	switch {
	case m.Name == "":
		return errors.New("name is required")
	case len(m.Name) > 100:
		return errors.New("keep the name under 100 characters")
	}
	if m.Source == "" {
		m.Source = SourceUI
	}
	switch m.Kind {
	case KindHealthcheck:
		if m.Check == nil || m.Heartbeat != nil {
			return errors.New("a healthcheck needs check settings")
		}
		return m.Check.normalize()
	case KindHeartbeat:
		if m.Heartbeat == nil || m.Check != nil {
			return errors.New("a heartbeat needs a schedule")
		}
		return m.Heartbeat.normalize()
	default:
		return fmt.Errorf("unknown monitor kind %q", m.Kind)
	}
}

// Event is a change in a monitor's status.
type Event struct {
	ID        int64     `json:"id"`
	MonitorID int64     `json:"monitor_id"`
	Time      time.Time `json:"time"`
	Status    Status    `json:"status"`
	Message   string    `json:"message"`
}

// Acknowledgement is someone taking on a monitor's outage: reminders stop
// until its next status change.
type Acknowledgement struct {
	By   string    `json:"by"`
	Time time.Time `json:"time"`
}
