package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/maintenance"
	"github.com/uptimy/agent/internal/monitor"
)

// During maintenance a monitor's down alert is held back. If it recovers
// before the window ends, nobody hears about it; if it's still down when the
// window ends, the alert goes out then. Status changes are recorded either
// way. Held alerts are rows in the outbox, so a restart keeps them.

// maintenanceCheckEvery is how soon after a window ends held alerts go out.
const maintenanceCheckEvery = 15 * time.Second

type maintenanceState struct {
	mu      sync.Mutex
	windows maintenance.Schedule
}

// ReloadMaintenance reads the current and upcoming windows; call it after
// they change.
func (s *Scheduler) ReloadMaintenance(ctx context.Context) error {
	ws, err := s.store.ListMaintenance(ctx, time.Now())
	if err != nil {
		return err
	}
	s.maint.mu.Lock()
	s.maint.windows = ws
	s.maint.mu.Unlock()
	s.releaseHeldAlerts() // a window may have been ended early
	return nil
}

// InMaintenance reports whether a monitor is in a maintenance window now.
func (s *Scheduler) InMaintenance(monitorID int64) bool {
	s.maint.mu.Lock()
	defer s.maint.mu.Unlock()
	return len(s.maint.windows.Covering(monitorID, time.Now())) > 0
}

// Maintenance returns the current and upcoming windows.
func (s *Scheduler) Maintenance() maintenance.Schedule {
	s.maint.mu.Lock()
	defer s.maint.mu.Unlock()
	return append(maintenance.Schedule(nil), s.maint.windows...)
}

// watchMaintenance sends held alerts once their window ends.
func (s *Scheduler) watchMaintenance() {
	t := time.NewTicker(maintenanceCheckEvery)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.releaseHeldAlerts()
		}
	}
}

// releaseHeldAlerts sends the held alerts of monitors no longer in
// maintenance that are still down, and drops the rest. A recovery during
// the window already dropped its down alert (store.InsertEventAlert).
func (s *Scheduler) releaseHeldAlerts() {
	ctx := context.WithoutCancel(s.ctx)
	held, err := s.store.HeldAlerts(ctx)
	if err != nil {
		s.log.Error("loading held alerts", "err", err)
		return
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	released := map[int64]bool{}
	for _, h := range held { // newest first
		if s.InMaintenance(h.MonitorID) {
			continue
		}
		drop := ""
		m, err := s.store.GetMonitor(ctx, h.MonitorID)
		switch {
		case err != nil:
			drop = "the monitor is gone"
		case released[m.ID]:
			drop = "a later alert went out instead"
		case m.Paused || s.lastStatus(m.ID) != monitor.StatusDown:
			// The newest event, not the runner, so this holds before
			// runners have started.
			drop = "it wasn't down when maintenance ended"
		}
		if drop != "" {
			err = s.store.SuppressHeld(ctx, h.AlertID, drop, now)
		} else {
			released[m.ID] = true
			msg := "still down after maintenance"
			if m.Kind == monitor.KindHeartbeat {
				msg = "is still down after maintenance"
			}
			err = s.store.ReleaseAlert(ctx, h.AlertID, msg, now, s.alerts.MaxAge)
		}
		if err != nil {
			s.log.Error("releasing a held alert", "err", err)
			continue
		}
		s.hub.Publish(events.Message{Type: "delivery", MonitorID: h.MonitorID})
	}
	if len(released) > 0 {
		s.alerts.Wake()
	}
}
