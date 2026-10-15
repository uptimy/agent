// Package delivery sends the alerts the scheduler stores in the outbox, to
// each of their channels separately: retried with backoff while a channel is
// unreachable, given up on when a channel refuses or the alert gets too old,
// and in order per channel and monitor, so a recovery never arrives before
// the outage it ends. Pending deliveries are in the database, so a restart
// carries on where it stopped.
//
// Delivery is at least once: an attempt cut off after the channel accepted
// it (a timeout, a restart) is sent again. Alerts carry an ID that stays the
// same, for receivers that drop copies.
package delivery

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/notify"
	"github.com/uptimy/agent/internal/store"
)

// MaxAge is how long a failing delivery is retried. An alert later than
// this is more confusing than useful: the monitor has likely changed since.
const MaxAge = time.Hour

// Backoff is the wait before each retry; the last one repeats.
var Backoff = []time.Duration{10 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}

const (
	// attemptTimeout bounds one attempt; channels have shorter timeouts of
	// their own, this catches the rest.
	attemptTimeout = 30 * time.Second
	// maxRetryAfter caps how long a channel's Retry-After can delay an alert.
	maxRetryAfter = 15 * time.Minute
	// idlePoll is how often the queue is checked with nothing scheduled,
	// in case a write didn't Wake the dispatcher.
	idlePoll = 30 * time.Second
	// batch is how many due deliveries are picked up at once.
	batch = 100
)

// Dispatcher delivers the outbox.
type Dispatcher struct {
	store  *store.Store
	sender *notify.Sender
	hub    *events.Hub
	log    *slog.Logger

	// Backoff and MaxAge default to the package's; tests shorten them.
	Backoff []time.Duration
	MaxAge  time.Duration

	wake     chan struct{}
	inflight sync.WaitGroup
	mu       sync.Mutex
	busy     map[route]bool // channel and monitor pairs with an attempt running
	stats    sync.Map       // statKey → *atomic.Int64
}

// route is a channel and a monitor: alerts along one are sent in order.
type route struct{ notifier, monitor int64 }

// New returns a dispatcher. Call Run to start it.
func New(st *store.Store, sender *notify.Sender, hub *events.Hub, log *slog.Logger) *Dispatcher {
	return &Dispatcher{
		store: st, sender: sender, hub: hub, log: log,
		Backoff: Backoff, MaxAge: MaxAge,
		wake: make(chan struct{}, 1),
		busy: map[route]bool{},
	}
}

// Wake makes the dispatcher look for due deliveries now; call it after
// adding or changing some.
func (d *Dispatcher) Wake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// Run delivers until ctx is canceled, then waits up to grace for attempts
// in flight. Those still running stay pending and are sent again on the
// next start.
func (d *Dispatcher) Run(ctx context.Context, grace time.Duration) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			d.drain(grace)
			return
		case <-d.wake:
		case <-timer.C:
		}
		d.dispatch(ctx)
		timer.Reset(d.untilNext(ctx))
	}
}

func (d *Dispatcher) drain(grace time.Duration) {
	done := make(chan struct{})
	go func() { d.inflight.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(grace):
		d.log.Warn("alerts still being sent at shutdown; they'll be sent again on the next start")
	}
}

// untilNext is how long to sleep before the next delivery is due.
func (d *Dispatcher) untilNext(ctx context.Context) time.Duration {
	next, ok, err := d.store.NextDeliveryAt(ctx)
	if err != nil || !ok {
		return idlePoll
	}
	return min(max(time.Until(next), 0), idlePoll)
}

func (d *Dispatcher) dispatch(ctx context.Context) {
	due, err := d.store.DueDeliveries(ctx, time.Now(), batch)
	if err != nil {
		if ctx.Err() == nil {
			d.log.Error("loading alerts to send", "err", err)
		}
		return
	}
	for _, o := range due {
		r := route{o.Delivery.NotifierID, o.MonitorID}
		d.mu.Lock()
		if d.busy[r] {
			d.mu.Unlock()
			continue
		}
		d.busy[r] = true
		d.mu.Unlock()
		d.inflight.Add(1)
		go func() {
			defer d.inflight.Done()
			d.attempt(ctx, o)
			d.mu.Lock()
			delete(d.busy, r)
			d.mu.Unlock()
			d.Wake() // the next alert on this route may be due
		}()
	}
}

// attempt sends one delivery and records how it went.
func (d *Dispatcher) attempt(ctx context.Context, o store.Outgoing) {
	del := o.Delivery
	now := time.Now().UTC()
	finish := func(state notify.DeliveryState, detail string) {
		del.State, del.Detail, del.DoneAt = state, detail, &now
	}
	switch {
	case !o.Notifier.Enabled:
		finish(notify.DeliverySuppressed, "the channel was disabled")
	case o.Unheard:
		// Nobody heard it went down, so "back up" would only confuse.
		finish(notify.DeliverySuppressed, "its down alert wasn't delivered")
	default:
		// Attempts outlive shutdown briefly (see Run), so a send that's
		// nearly done isn't cut off and sent twice.
		actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), attemptTimeout)
		err := d.sender.Send(actx, o.Notifier, o.Alert)
		cancel()
		now = time.Now().UTC()
		del.Attempts++
		switch {
		case err == nil:
			finish(notify.DeliverySent, "")
		case notify.IsPermanent(err):
			finish(notify.DeliveryFailed, err.Error())
		default:
			next := now.Add(max(d.backoff(del.Attempts), min(notify.RetryAfter(err), maxRetryAfter)))
			if next.After(del.ExpiresAt) {
				finish(notify.DeliveryExpired, err.Error())
			} else {
				del.NextAttempt, del.Detail = next, err.Error()
			}
		}
		if err != nil {
			d.log.Warn("alert not delivered", "channel", o.Notifier.Name, "type", o.Notifier.Type,
				"monitor", o.Alert.MonitorName, "status", o.Alert.Status, "attempt", del.Attempts, "outcome", del.State, "err", err)
		}
	}
	d.count(o.Notifier.Type, del)
	if err := d.store.UpdateDelivery(context.WithoutCancel(ctx), del); err != nil {
		d.log.Error("saving an alert delivery", "err", err)
		return
	}
	d.hub.Publish(events.Message{Type: "delivery", MonitorID: o.MonitorID, Data: del})
}

// backoff is the wait after the given number of failed attempts.
func (d *Dispatcher) backoff(attempts int) time.Duration {
	return d.Backoff[min(attempts, len(d.Backoff))-1]
}

// statKey counts attempts by channel type and result.
type statKey struct{ channelType, result string }

func (d *Dispatcher) count(channelType string, del notify.Delivery) {
	result := string(del.State)
	if del.State == notify.DeliveryPending {
		result = "retry"
	}
	v, _ := d.stats.LoadOrStore(statKey{channelType, result}, new(atomic.Int64))
	v.(*atomic.Int64).Add(1)
}

// Stat is a count of delivery outcomes since the agent started.
type Stat struct {
	ChannelType string
	Result      string // sent, retry, failed, expired or suppressed
	Count       int64
}

// Stats returns delivery outcomes since the agent started.
func (d *Dispatcher) Stats() []Stat {
	var out []Stat
	d.stats.Range(func(k, v any) bool {
		key := k.(statKey)
		out = append(out, Stat{key.channelType, key.result, v.(*atomic.Int64).Load()})
		return true
	})
	return out
}
