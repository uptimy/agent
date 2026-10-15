package scheduler

import (
	"context"
	"errors"
	"time"

	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
)

// While a monitor stays down, channels with a repeat interval are reminded
// ("Checkout API is still down (for 45m)"), counting from the last alert
// they were sent about it, so a muted channel or a missed message doesn't
// leave an outage unnoticed. Acknowledging the outage stops the reminders
// and tells the channels someone is on it. Maintenance pauses reminders;
// any status change ends both (store.insertEvent).

// reminderCheckEvery is how often down monitors are checked for reminders
// that are due.
const reminderCheckEvery = 30 * time.Second

// ErrNotDown is returned when acknowledging a monitor that isn't down.
var ErrNotDown = errors.New("it isn't down")

func (s *Scheduler) watchReminders() {
	t := time.NewTicker(s.reminderEvery)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
			s.sendReminders()
		}
	}
}

// sendReminders queues the reminders that are due.
func (s *Scheduler) sendReminders() {
	ctx := context.WithoutCancel(s.ctx)
	acks, err := s.store.Acknowledgements(ctx)
	if err != nil {
		s.log.Error("loading acknowledgements", "err", err)
		return
	}
	s.mu.Lock()
	var down []int64
	for id, r := range s.runners {
		if r.status() == monitor.StatusDown {
			down = append(down, id)
		}
	}
	s.mu.Unlock()

	queued := false
	for _, id := range down {
		if _, acked := acks[id]; acked || s.InMaintenance(id) {
			continue
		}
		if err := s.remind(ctx, id); err != nil {
			s.log.Error("queueing reminders", "monitor", id, "err", err)
			continue
		}
		queued = true
	}
	if queued {
		s.alerts.Wake()
	}
}

// remind queues a reminder about monitor id for each channel that's due one.
func (s *Scheduler) remind(ctx context.Context, id int64) error {
	m, err := s.store.GetMonitor(ctx, id)
	if err != nil || m.Paused {
		return err
	}
	outage, err := s.store.LastEvent(ctx, id)
	if err != nil || outage.Status != monitor.StatusDown {
		return err
	}
	channels, err := s.store.NotifiersFor(ctx, id)
	if err != nil {
		return err
	}
	at := now()
	var due []int64
	for _, n := range channels {
		if ch, ok := notify.Lookup(n.Type); !ok || !ch.Reminders || n.RepeatSeconds == 0 {
			continue
		}
		last, waiting, err := s.store.LastAlertAt(ctx, n.ID, id)
		if err != nil {
			return err
		}
		if last.Before(outage.Time) {
			last = outage.Time // a channel added during the outage
		}
		if !waiting && at.Sub(last) >= time.Duration(n.RepeatSeconds)*time.Second {
			due = append(due, n.ID)
		}
	}
	if len(due) == 0 {
		return nil
	}
	a := alertFor(m, monitor.StatusDown, outage.Message, at)
	a.Type, a.DownSince = notify.AlertReminder, outage.Time
	if err := s.store.InsertAlert(ctx, outage.ID, a, due, s.alerts.MaxAge); err != nil {
		return err
	}
	s.log.Info("reminding that a monitor is still down", "monitor", m.Name, "channels", len(due))
	s.hub.Publish(events.Message{Type: "delivery", MonitorID: id})
	return nil
}

// Acknowledge records that by is on m's outage: reminders stop until its
// next status change, and the channels people read hear who took it on
// (unless maintenance held its alert back, so they never heard of it).
func (s *Scheduler) Acknowledge(m monitor.Monitor, by string) error {
	ctx := context.WithoutCancel(s.ctx)
	if s.Status(m) != monitor.StatusDown {
		return ErrNotDown
	}
	outage, err := s.store.LastEvent(ctx, m.ID)
	if err != nil {
		return err
	}
	if outage.Status != monitor.StatusDown {
		return ErrNotDown
	}
	at := now()
	if err := s.store.Acknowledge(ctx, m.ID, outage.ID, by, at); err != nil {
		return err
	}
	s.log.Info("outage acknowledged", "monitor", m.Name, "by", by)
	s.hub.Publish(events.Message{Type: "monitors", MonitorID: m.ID})
	if s.InMaintenance(m.ID) {
		return nil
	}
	channels, err := s.store.NotifiersFor(ctx, m.ID)
	if err != nil {
		return err
	}
	var ids []int64
	for _, n := range channels {
		if ch, ok := notify.Lookup(n.Type); ok && ch.Reminders {
			ids = append(ids, n.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	a := alertFor(m, monitor.StatusDown, outage.Message, at)
	a.Type, a.DownSince, a.AcknowledgedBy = notify.AlertAcknowledged, outage.Time, by
	if err := s.store.InsertAlert(ctx, outage.ID, a, ids, s.alerts.MaxAge); err != nil {
		return err
	}
	s.alerts.Wake()
	s.hub.Publish(events.Message{Type: "delivery", MonitorID: m.ID})
	return nil
}

// alertFor is the alert about m changing to status.
func alertFor(m monitor.Monitor, status monitor.Status, message string, at time.Time) notify.Alert {
	return notify.Alert{
		MonitorID: m.ID, MonitorName: m.Name, Kind: string(m.Kind), Target: m.Describe(),
		Status: string(status), Message: message, Time: at,
	}
}
