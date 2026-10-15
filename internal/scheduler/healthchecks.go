package scheduler

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
)

// healthcheckRunner probes a healthcheck on its interval.
type healthcheckRunner struct {
	cancel context.CancelFunc
	done   chan struct{}
	now    chan struct{} // "check now", from the UI

	mu        sync.Mutex
	current   monitor.Status
	failures  int // consecutive failed probes
	successes int // consecutive good probes
}

func (r *healthcheckRunner) stop() {
	r.cancel()
	<-r.done
}

func (r *healthcheckRunner) status() monitor.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.current
}

func (s *Scheduler) startHealthcheck(m monitor.Monitor) *healthcheckRunner {
	ctx, cancel := context.WithCancel(s.ctx)
	r := &healthcheckRunner{cancel: cancel, done: make(chan struct{}), now: make(chan struct{}, 1), current: s.lastStatus(m.ID)}
	go s.runHealthcheck(ctx, r, m)
	return r
}

func (s *Scheduler) runHealthcheck(ctx context.Context, r *healthcheckRunner, m monitor.Monitor) {
	defer close(r.done)
	interval := m.Check.Interval()
	// Stagger first checks so a restart doesn't fire everything at once.
	first := time.Duration(rand.Int64N(int64(min(interval, 5*time.Second)))) //nolint:gosec // G404: timing jitter, not a secret
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-r.now:
		}
		s.probe(ctx, r, m)
		// Go 1.23+ timers: Reset discards any pending fire, no drain needed.
		timer.Reset(interval)
	}
}

// CheckNow probes a healthcheck right away instead of waiting for its
// interval. It reports false if the healthcheck isn't running (paused).
func (s *Scheduler) CheckNow(id int64) bool {
	s.mu.Lock()
	r, ok := s.runners[id].(*healthcheckRunner)
	s.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case r.now <- struct{}{}:
	default: // a check is already queued
	}
	return true
}

// probe runs one check, records the result and updates the status: down
// after FailureThreshold consecutive failures, up again after
// RecoveryThreshold consecutive successes.
func (s *Scheduler) probe(ctx context.Context, r *healthcheckRunner, m monitor.Monitor) {
	c := *m.Check
	start := time.Now()
	cctx, cancel := context.WithTimeout(ctx, c.Timeout())
	out := s.checker.Run(cctx, c)
	if cctx.Err() == context.DeadlineExceeded && !out.OK {
		out.Message = "timed out after " + c.Timeout().String()
	}
	cancel()
	if ctx.Err() != nil {
		return // stopped or edited mid-check
	}

	latency := time.Since(start)
	if out.Latency > 0 {
		latency = out.Latency
	}
	res := monitor.Result{
		MonitorID: m.ID, Time: start.UTC(), OK: out.OK, LatencyMS: latency.Milliseconds(), Message: out.Message,
		StatusCode: out.StatusCode, ErrorClass: out.ErrorClass,
	}
	if err := s.store.InsertResult(ctx, res); err != nil {
		s.log.Error("saving result", "monitor", m.Name, "err", err)
	}
	s.hub.Publish(events.Message{Type: "result", MonitorID: m.ID, Data: res})
	s.countCheck(m.ID, res.OK, latency, start)

	r.mu.Lock()
	prev := r.current
	next := prev
	if res.OK {
		r.failures = 0
		r.successes++
		// Once down, it takes RecoveryThreshold good probes in a row to be
		// up again; from pending, the first good one will do.
		if prev != monitor.StatusDown || r.successes >= c.RecoveryThreshold {
			next = monitor.StatusUp
		}
	} else {
		r.successes = 0
		r.failures++
		if r.failures >= c.FailureThreshold {
			next = monitor.StatusDown
		}
	}
	r.current = next
	r.mu.Unlock()

	if record, alert := changed(prev, next); record {
		s.recordTransition(m, next, res.Message, alert)
	}
}
