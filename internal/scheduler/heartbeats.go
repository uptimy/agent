package scheduler

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// A heartbeat is tracked against its schedule. Each run is due at a time the
// schedule sets (an interval after the last run, or the next cron time) and
// counts as on time until that time plus the grace period, the deadline:
//
//	on time ──due──▶ late ──deadline──▶ missed (a missed run is recorded)
//
// A ping finishes a run: success puts the heartbeat back on schedule,
// failure marks it failed. Runs are stored, so a restart picks up where the
// schedule stands.

// Signal is what a ping reports.
type Signal string

const (
	SignalSuccess Signal = "success" // the job finished
	SignalStart   Signal = "start"   // the job started; its finish gives the duration
	SignalFailure Signal = "failure" // the job finished and failed
)

// maxRunDuration bounds how long a started run is tracked: a finish ping
// later than this isn't paired with that start, and the start is closed as
// missed (abandonedRun).
const maxRunDuration = 24 * time.Hour

const abandonedRun = "started and never reported finishing"

type ping struct {
	signal  Signal
	message string
	at      time.Time // when it happened; zero is now
}

// HeartbeatStatus is where a heartbeat stands against its schedule.
type HeartbeatStatus struct {
	State monitor.HeartbeatState `json:"state"`
	// DueAt is when the next run is due; Deadline is DueAt plus the grace
	// period, when it counts as missed. Both are zero for a paused heartbeat.
	DueAt    time.Time `json:"due_at,omitzero"`
	Deadline time.Time `json:"deadline,omitzero"`
	// Running is set while a run that pinged /start hasn't finished.
	Running *time.Time `json:"running_since"`
}

type heartbeatRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
	pings  chan ping

	mu  sync.Mutex
	now HeartbeatStatus
}

func (r *heartbeatRunner) stop() {
	r.cancel()
	<-r.done
}

func (r *heartbeatRunner) status() monitor.Status {
	return r.snapshot().State.Status()
}

func (r *heartbeatRunner) snapshot() HeartbeatStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

// Heartbeat returns where heartbeat m stands against its schedule.
func (s *Scheduler) Heartbeat(m monitor.Monitor) HeartbeatStatus {
	if m.Paused {
		return HeartbeatStatus{State: monitor.StatePaused}
	}
	s.mu.Lock()
	r, ok := s.runners[m.ID].(*heartbeatRunner)
	s.mu.Unlock()
	if !ok {
		return HeartbeatStatus{State: monitor.StateWaiting}
	}
	return r.snapshot()
}

// ErrUnknownToken means no heartbeat has the ping token.
var ErrUnknownToken = errors.New("unknown ping token")

// Ping delivers a ping to the heartbeat with the token. Pings to a paused
// heartbeat are accepted and ignored, so a job keeps working while its
// alerts are paused.
func (s *Scheduler) Ping(token string, signal Signal, message string) error {
	s.mu.Lock()
	id, ok := s.tokens[token]
	r, running := s.runners[id].(*heartbeatRunner)
	s.mu.Unlock()
	if !ok {
		return ErrUnknownToken
	}
	if !running {
		return nil // paused
	}
	select {
	case r.pings <- ping{signal: signal, message: message}:
		return nil
	case <-r.done:
		return nil // stopped in between: being edited or deleted
	}
}

// Report delivers a run's start or finish that happened at at, to heartbeat
// id: for runs the agent observes itself (a Kubernetes CronJob's Jobs)
// rather than ones that ping. Like a ping, it's ignored while paused.
func (s *Scheduler) Report(id int64, signal Signal, at time.Time, message string) {
	s.mu.Lock()
	r, running := s.runners[id].(*heartbeatRunner)
	s.mu.Unlock()
	if !running {
		return
	}
	select {
	case r.pings <- ping{signal: signal, message: message, at: at}:
	case <-r.done:
	}
}

func (s *Scheduler) startHeartbeat(m monitor.Monitor) *heartbeatRunner {
	ctx, cancel := context.WithCancel(s.ctx)
	r := &heartbeatRunner{cancel: cancel, done: make(chan struct{}), pings: make(chan ping)}
	t := &heartbeatTracker{s: s, r: r, m: m, h: *m.Heartbeat, status: s.lastStatus(m.ID)}
	t.restore(ctx)
	go t.run(ctx)
	return r
}

// heartbeatTracker is the state a heartbeat's goroutine owns.
type heartbeatTracker struct {
	s *Scheduler
	r *heartbeatRunner
	m monitor.Monitor
	h monitor.Heartbeat

	anchor time.Time              // the next run is due h.NextDue(anchor)
	state  monitor.HeartbeatState // before applying the clock (late/on time)
	status monitor.Status         // last recorded status, to spot changes
	open   *monitor.Run           // a run that started and hasn't finished
	missed *monitor.Run           // the newest run, if it was missed
}

// restore picks up from the stored runs: the schedule continues from the
// last run, or from the last change to the heartbeat if that's later (so
// resuming it, or changing its schedule, doesn't report the runs due in the
// meantime as missed).
func (t *heartbeatTracker) restore(ctx context.Context) {
	t.anchor, t.state = t.m.UpdatedAt, monitor.StateWaiting
	if last, err := t.s.store.LastRun(ctx, t.m.ID); err == nil {
		if a := runAnchor(last); a.After(t.anchor) {
			t.anchor = a
		}
		switch last.Outcome {
		case monitor.OutcomeSuccess:
			t.state = monitor.StateOnTime
		case monitor.OutcomeFailure:
			t.state = monitor.StateFailed
		case monitor.OutcomeMissed:
			t.state, t.missed = monitor.StateMissed, &last
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		t.s.log.Error("loading the last run", "heartbeat", t.m.Name, "err", err)
	}
	var keep int64
	if open, err := t.s.store.OpenRun(ctx, t.m.ID); err == nil {
		t.open, keep = &open, open.ID
	}
	// Older starts were replaced or left behind (e.g. before an upgrade).
	if err := t.s.store.AbandonRuns(ctx, t.m.ID, keep, abandonedRun); err != nil {
		t.s.log.Error("closing abandoned runs", "heartbeat", t.m.Name, "err", err)
	}
	t.publish()
}

// runAnchor is the time the schedule continues from after a run: when it
// finished, or when it was due if it never arrived.
func runAnchor(r monitor.Run) time.Time {
	if r.FinishedAt != nil {
		return *r.FinishedAt
	}
	if r.DueAt != nil {
		return *r.DueAt
	}
	return time.Now().UTC()
}

func (t *heartbeatTracker) run(ctx context.Context) {
	defer close(t.r.done)
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		due := t.h.NextDue(t.anchor)
		deadline := due.Add(t.h.Grace())
		now := now()
		if t.open != nil && !now.Before(t.open.StartedAt.Add(maxRunDuration)) {
			t.abandon(ctx)
		}
		if !now.Before(deadline) {
			t.miss(ctx, due)
			continue // the next run is due after this one
		}
		t.publish()
		// Wake at the next change: when the run becomes late, or missed, or
		// a started run is given up on.
		wake := deadline
		if now.Before(due) {
			wake = due
		}
		if t.open != nil {
			wake = minTime(wake, t.open.StartedAt.Add(maxRunDuration))
		}
		timer.Reset(wake.Sub(now))
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case p := <-t.r.pings:
			t.handle(ctx, p, due, deadline)
		}
	}
}

// publish updates the status the API and other goroutines read: the stored
// state, adjusted for the clock (a run past due is late).
func (t *heartbeatTracker) publish() {
	due := t.h.NextDue(t.anchor)
	st := HeartbeatStatus{State: t.state, DueAt: due, Deadline: due.Add(t.h.Grace())}
	// Past due is late, unless the run has started: it's running, and only
	// counts as missed if it hasn't finished by the deadline.
	if t.state == monitor.StateOnTime && !now().Before(due) && t.open == nil {
		st.State = monitor.StateLate
	}
	if t.open != nil {
		st.Running = t.open.StartedAt
	}
	t.r.mu.Lock()
	changed := t.r.now.State != st.State
	t.r.now = st
	t.r.mu.Unlock()
	if changed {
		t.s.hub.Publish(events.Message{Type: "heartbeat", MonitorID: t.m.ID, Data: st})
	}
}

// miss records that the run due at due didn't arrive in time.
func (t *heartbeatTracker) miss(ctx context.Context, due time.Time) {
	msg := "no ping by " + t.formatDue(due.Add(t.h.Grace()))
	run := monitor.Run{MonitorID: t.m.ID, DueAt: &due, Outcome: monitor.OutcomeMissed, Message: msg}
	run, err := t.s.store.InsertRun(ctx, run)
	if err != nil {
		t.s.log.Error("saving a missed run", "heartbeat", t.m.Name, "err", err)
	}
	t.anchor, t.state, t.missed = due, monitor.StateMissed, &run
	t.s.hub.Publish(events.Message{Type: "run", MonitorID: t.m.ID, Data: run})
	t.s.countRun(t.m.ID, monitor.OutcomeMissed)
	t.transition(fmt.Sprintf("missed its run due %s", t.formatDue(due)))
}

// handle applies a ping. due and deadline are for the run it reports.
func (t *heartbeatTracker) handle(ctx context.Context, p ping, due, deadline time.Time) {
	now := now()
	if !p.at.IsZero() && p.at.Before(now) {
		now = p.at
	}
	if p.signal == SignalStart {
		t.start(ctx, now, due)
		return
	}

	run := monitor.Run{MonitorID: t.m.ID, DueAt: &due, OnTime: !now.After(deadline), Message: p.message}
	switch {
	case t.state == monitor.StateMissed && t.missed != nil && now.Before(due):
		// The run recorded as missed arrived after all, before the next was
		// due: it was late, not absent.
		// (A reported run can also have been on time: it finished before
		// the deadline, and was only seen after it.)
		run = *t.missed
		run.OnTime, run.Message = run.DueAt != nil && !now.After(run.DueAt.Add(t.h.Grace())), p.message
		// If it pinged /start, that start is this run's: fold it in.
		if t.open != nil && now.Sub(*t.open.StartedAt) <= maxRunDuration {
			run.StartedAt = t.open.StartedAt
		}
	case t.open != nil && now.Sub(*t.open.StartedAt) <= maxRunDuration:
		run.ID, run.StartedAt = t.open.ID, t.open.StartedAt
	}
	run.FinishedAt = &now
	run.Outcome = monitor.OutcomeSuccess
	if p.signal == SignalFailure {
		run.Outcome = monitor.OutcomeFailure
	}
	if run.StartedAt != nil {
		d := now.Sub(*run.StartedAt).Milliseconds()
		run.DurationMS = &d
	}
	var err error
	if run.ID != 0 {
		err = t.s.store.UpdateRun(ctx, run)
	} else {
		run, err = t.s.store.InsertRun(ctx, run)
	}
	if err != nil {
		t.s.log.Error("saving a run", "heartbeat", t.m.Name, "err", err)
	}
	if t.open != nil && t.open.ID != run.ID {
		// The start wasn't finished by this run's row: folded into it, or too
		// old to pair with it.
		if run.StartedAt == t.open.StartedAt {
			err = t.s.store.DeleteRun(ctx, t.open.ID)
		} else {
			err = t.s.store.AbandonRuns(ctx, t.m.ID, run.ID, abandonedRun)
		}
		if err != nil {
			t.s.log.Error("closing a started run", "heartbeat", t.m.Name, "err", err)
		}
	}
	t.open, t.missed, t.anchor = nil, nil, now
	t.s.hub.Publish(events.Message{Type: "run", MonitorID: t.m.ID, Data: run})
	t.s.countRun(t.m.ID, run.Outcome)

	if run.Outcome == monitor.OutcomeFailure {
		t.state = monitor.StateFailed
		t.transition(failureMessage(p.message))
		return
	}
	msg := "is back on schedule"
	if t.status == monitor.StatusPending {
		msg = "reported its first run"
	}
	t.state = monitor.StateOnTime
	t.transition(msg)
}

// abandon closes the started run once it can no longer be finished.
func (t *heartbeatTracker) abandon(ctx context.Context) {
	run := *t.open
	run.Outcome, run.Message = monitor.OutcomeMissed, abandonedRun
	if err := t.s.store.UpdateRun(ctx, run); err != nil {
		t.s.log.Error("closing a started run", "heartbeat", t.m.Name, "err", err)
	}
	t.open = nil
	t.s.hub.Publish(events.Message{Type: "run", MonitorID: t.m.ID, Data: run})
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// start records that a run began; its finish ping gives the duration. A new
// start replaces a run that started and never finished.
func (t *heartbeatTracker) start(ctx context.Context, now, due time.Time) {
	run := monitor.Run{MonitorID: t.m.ID, DueAt: &due, StartedAt: &now, Outcome: monitor.OutcomeRunning}
	var err error
	if t.open != nil {
		run.ID = t.open.ID
		err = t.s.store.UpdateRun(ctx, run)
	} else {
		run, err = t.s.store.InsertRun(ctx, run)
	}
	if err != nil {
		t.s.log.Error("saving a started run", "heartbeat", t.m.Name, "err", err)
	}
	t.open = &run
	t.s.hub.Publish(events.Message{Type: "run", MonitorID: t.m.ID, Data: run})
}

// transition records and alerts on a status change caused by the current
// state; message describes it.
func (t *heartbeatTracker) transition(message string) {
	next := t.state.Status()
	record, alert := changed(t.status, next)
	t.status = next
	t.publish()
	if record {
		t.s.recordTransition(t.m, next, message, alert)
	}
}

func failureMessage(detail string) string {
	if detail == "" {
		return "reported a failure"
	}
	return "reported a failure: " + detail
}

// formatDue formats a time in the schedule's time zone, e.g. "03:00 CEST"
// (with the date when it isn't today there).
func (t *heartbeatTracker) formatDue(at time.Time) string {
	loc := time.UTC
	if t.h.Timezone != "" {
		if l, err := time.LoadLocation(t.h.Timezone); err == nil {
			loc = l
		}
	}
	local, today := at.In(loc), now().In(loc)
	if local.YearDay() == today.YearDay() && local.Year() == today.Year() {
		return local.Format("15:04 MST")
	}
	return local.Format("Jan 2 15:04 MST")
}
