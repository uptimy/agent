// Package scheduler keeps every active monitor running: it probes
// healthchecks on their interval (healthchecks.go), tracks heartbeats against
// their schedule (heartbeats.go), records status changes and sends alerts.
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/uptimy/agent/internal/checks"
	"github.com/uptimy/agent/internal/delivery"
	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// Scheduler owns one goroutine per active monitor.
type Scheduler struct {
	store   *store.Store
	checker *checks.Checker
	alerts  *delivery.Dispatcher
	hub     *events.Hub
	log     *slog.Logger

	ctx     context.Context
	mu      sync.Mutex
	runners map[int64]runner
	tokens  map[string]int64 // heartbeat ping token → monitor ID, paused ones too
	maint   maintenanceState
	stats   statsTable

	// reminderEvery is how often reminders are checked; tests shorten it.
	reminderEvery time.Duration
}

// runner is the goroutine watching one monitor.
type runner interface {
	stop()
	status() monitor.Status
}

// New creates a scheduler. Call Start to begin running monitors.
// Alerts go to the outbox, which alerts delivers.
func New(st *store.Store, checker *checks.Checker, alerts *delivery.Dispatcher, hub *events.Hub, log *slog.Logger) *Scheduler {
	return &Scheduler{
		store: st, checker: checker, alerts: alerts, hub: hub, log: log,
		runners: map[int64]runner{},
		tokens:  map[string]int64{},

		reminderEvery: reminderCheckEvery,
	}
}

// Start loads every monitor from the store and begins watching them. Runners
// stop when ctx is canceled; call Wait to block until they have.
func (s *Scheduler) Start(ctx context.Context) error {
	s.ctx = ctx
	if err := s.ReloadMaintenance(ctx); err != nil {
		return err
	}
	monitors, err := s.store.ListMonitors(ctx)
	if err != nil {
		return err
	}
	for _, m := range monitors {
		s.Upsert(m)
	}
	go s.watchMaintenance()
	go s.watchReminders()
	return nil
}

// Wait blocks until every runner has exited.
func (s *Scheduler) Wait() {
	s.mu.Lock()
	runners := make([]runner, 0, len(s.runners))
	for _, r := range s.runners {
		runners = append(runners, r)
	}
	s.mu.Unlock()
	for _, r := range runners {
		r.stop()
	}
}

// Upsert (re)starts watching m after it was created or changed. Paused
// monitors are stopped.
func (s *Scheduler) Upsert(m monitor.Monitor) {
	s.mu.Lock()
	defer s.mu.Unlock()

	hadRunner := s.stopLocked(m.ID)
	s.setTokenLocked(m)
	if m.Paused {
		if hadRunner {
			s.recordTransition(m, monitor.StatusPaused, "was paused", false)
		}
		return
	}
	switch m.Kind {
	case monitor.KindHealthcheck:
		s.runners[m.ID] = s.startHealthcheck(m)
	case monitor.KindHeartbeat:
		s.runners[m.ID] = s.startHeartbeat(m)
	}
}

// Remove stops watching a deleted monitor.
func (s *Scheduler) Remove(id int64) {
	s.forgetStats(id)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked(id)
	for token, mid := range s.tokens {
		if mid == id {
			delete(s.tokens, token)
		}
	}
}

// Status returns a monitor's current status.
func (s *Scheduler) Status(m monitor.Monitor) monitor.Status {
	if m.Paused {
		return monitor.StatusPaused
	}
	s.mu.Lock()
	r, ok := s.runners[m.ID]
	s.mu.Unlock()
	if !ok {
		return monitor.StatusPending
	}
	return r.status()
}

// Counts returns how many active monitors are up and down.
func (s *Scheduler) Counts() (up, down int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.runners {
		switch r.status() {
		case monitor.StatusUp:
			up++
		case monitor.StatusDown:
			down++
		}
	}
	return up, down
}

func (s *Scheduler) stopLocked(id int64) bool {
	r, ok := s.runners[id]
	if !ok {
		return false
	}
	r.stop()
	delete(s.runners, id)
	return true
}

// setTokenLocked keeps the ping token map in step with heartbeat m.
func (s *Scheduler) setTokenLocked(m monitor.Monitor) {
	for token, id := range s.tokens {
		if id == m.ID {
			delete(s.tokens, token)
		}
	}
	if m.Heartbeat != nil {
		s.tokens[m.Heartbeat.Token] = m.ID
	}
}

// lastStatus is a monitor's status from its newest event, so a restart
// doesn't record (or alert on) a change that already happened.
func (s *Scheduler) lastStatus(id int64) monitor.Status {
	e, err := s.store.LastEvent(s.ctx, id)
	if err != nil || (e.Status != monitor.StatusUp && e.Status != monitor.StatusDown) {
		return monitor.StatusPending
	}
	return e.Status
}

// recordTransition records a status change and, if alert is set, the alert
// to the monitor's channels, in one transaction: a change is never recorded
// without its alert. A down alert during maintenance is held instead
// (maintenance.go).
func (s *Scheduler) recordTransition(m monitor.Monitor, status monitor.Status, message string, alert bool) {
	ctx := context.WithoutCancel(s.ctx)
	e := monitor.Event{MonitorID: m.ID, Time: now(), Status: status, Message: message}
	var err error
	if alert {
		held := status == monitor.StatusDown && s.InMaintenance(m.ID)
		e, err = s.store.InsertEventAlert(ctx, e, alertFor(m, status, message, e.Time), held, s.alerts.MaxAge)
		if held {
			s.log.Info("alert held for maintenance", "monitor", m.Name, "status", status)
		}
	} else {
		e, err = s.store.InsertEvent(ctx, e)
	}
	if err != nil {
		s.log.Error("saving event", "monitor", m.Name, "err", err)
	}
	s.hub.Publish(events.Message{Type: "status", MonitorID: m.ID, Data: e})
	s.log.Info("monitor status changed", "monitor", m.Name, "status", status, "message", message)
	if alert {
		s.alerts.Wake()
	}
}

// now is the scheduler's clock, at the database's millisecond precision so
// times kept in memory and stored ones compare equal.
func now() time.Time { return time.Now().UTC().Truncate(time.Millisecond) }

// changed reports whether a status change is worth recording, and alerting
// on: going down, and recovering from down. Pending → up is just the first
// good result and isn't news.
func changed(prev, next monitor.Status) (record, alert bool) {
	if prev == next {
		return false, false
	}
	return true, next == monitor.StatusDown || prev == monitor.StatusDown
}
