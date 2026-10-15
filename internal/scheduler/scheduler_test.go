package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/checks"
	"github.com/uptimy/agent/internal/delivery"
	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
	"github.com/uptimy/agent/internal/store"
)

type fixture struct {
	store   *store.Store
	sched   *Scheduler
	alerter *delivery.Dispatcher
	alerts  chan notify.Alert
	stop    func()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	alerts := make(chan notify.Alert, 10)
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a notify.Alert
		json.NewDecoder(r.Body).Decode(&a)
		alerts <- a
	}))
	t.Cleanup(sink.Close)
	if _, err := st.CreateNotifier(context.Background(), notify.Notifier{Name: "sink", Type: "webhook", Enabled: true, AllMonitors: true, Config: notify.Config{URL: sink.URL}}); err != nil {
		t.Fatal(err)
	}
	f := &fixture{store: st, alerts: alerts}
	f.start(t)
	return f
}

// start (re)starts the scheduler on the fixture's store.
func (f *fixture) start(t *testing.T) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub()
	f.alerter = delivery.New(f.store, notify.NewSender(log), hub, log)
	f.alerter.Backoff = []time.Duration{50 * time.Millisecond}
	f.sched = New(f.store, checks.New(nil), f.alerter, hub, log)
	f.sched.reminderEvery = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.alerter.Run(ctx, time.Second); close(done) }()
	f.stop = func() { cancel(); f.sched.Wait(); <-done }
	t.Cleanup(f.stop)
	if err := f.sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) create(t *testing.T, m monitor.Monitor) monitor.Monitor {
	t.Helper()
	m, err := f.store.CreateMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	f.sched.Upsert(m)
	return m
}

// eventually polls cond until it holds or the time runs out.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *fixture) alert(t *testing.T) notify.Alert {
	t.Helper()
	select {
	case a := <-f.alerts:
		return a
	case <-time.After(10 * time.Second):
		t.Fatal("no alert")
		return notify.Alert{}
	}
}

func TestHealthcheckDownAfterThresholdThenRecovers(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer target.Close()

	f := newFixture(t)
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "web", Check: &monitor.Check{
		Type: monitor.TypeHTTP, Target: target.URL, IntervalSeconds: 3600, FailureThreshold: 2,
	}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m = f.create(t, m)
	status := func(want monitor.Status) func() bool {
		return func() bool { return f.sched.Status(m) == want }
	}
	eventually(t, "up", status(monitor.StatusUp))

	healthy.Store(false)
	f.sched.CheckNow(m.ID)
	time.Sleep(300 * time.Millisecond)
	if got := f.sched.Status(m); got != monitor.StatusUp {
		t.Fatalf("one failure below the threshold should stay up, got %s", got)
	}
	f.sched.CheckNow(m.ID)
	eventually(t, "down", status(monitor.StatusDown))
	if a := f.alert(t); a.Status != "down" || a.MonitorName != "web" || a.Kind != "healthcheck" {
		t.Fatalf("unexpected alert %+v", a)
	}

	healthy.Store(true)
	f.sched.CheckNow(m.ID)
	eventually(t, "up again", status(monitor.StatusUp))
	if a := f.alert(t); a.Status != "up" {
		t.Fatalf("expected a recovery alert, got %+v", a)
	}
	events, _ := f.store.ListEvents(context.Background(), m.ID, 10)
	if len(events) != 3 { // up (first check), down, up
		t.Fatalf("expected 3 events, got %d", len(events))
	}
}

// fastHeartbeat is due every 2 seconds with 1 second of grace. Real
// heartbeats are at least a minute apart; the scheduler doesn't mind.
func fastHeartbeat(token string) monitor.Monitor {
	return monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "backup", Heartbeat: &monitor.Heartbeat{
		Token: token, EverySeconds: 2, GraceSeconds: 1,
	}}
}

func TestHeartbeatSchedule(t *testing.T) {
	f := newFixture(t)
	m := f.create(t, fastHeartbeat("tok-schedule"))
	state := func(want monitor.HeartbeatState) func() bool {
		return func() bool { return f.sched.Heartbeat(m).State == want }
	}
	if got := f.sched.Heartbeat(m).State; got != monitor.StateWaiting {
		t.Fatalf("new heartbeat: %s", got)
	}

	if err := f.sched.Ping("tok-schedule", SignalSuccess, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "on time", state(monitor.StateOnTime))
	eventually(t, "late once due", state(monitor.StateLate))
	eventually(t, "missed after the grace period", state(monitor.StateMissed))
	if a := f.alert(t); a.Status != "down" || !strings.HasPrefix(a.Message, "missed its run due") || a.Kind != "heartbeat" {
		t.Fatalf("unexpected alert %+v", a)
	}
	if text := (notify.Alert{Kind: "heartbeat", MonitorName: "backup", Status: "down", Message: "missed its run due 03:00 UTC"}).Text(); text != "🔴 backup missed its run due 03:00 UTC" {
		t.Fatalf("alert text: %q", text)
	}

	// The missed run arrives before the next one is due: it was late.
	if err := f.sched.Ping("tok-schedule", SignalSuccess, ""); err != nil {
		t.Fatal(err)
	}
	eventually(t, "back on schedule", state(monitor.StateOnTime))
	if a := f.alert(t); a.Status != "up" || a.Message != "is back on schedule" {
		t.Fatalf("expected a recovery, got %+v", a)
	}
	runs, _ := f.store.RecentRuns(context.Background(), m.ID, 10)
	if len(runs) != 2 || !runs[0].OnTime || runs[1].OnTime || runs[1].Outcome != monitor.OutcomeSuccess {
		t.Fatalf("want one on-time run and one late one, got %+v", runs)
	}
}

func TestHeartbeatDurationAndFailure(t *testing.T) {
	f := newFixture(t)
	m := monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "export", Heartbeat: &monitor.Heartbeat{
		Token: "tok-signals", EverySeconds: 3600, GraceSeconds: 300,
	}}
	m = f.create(t, m)

	f.sched.Ping("tok-signals", SignalStart, "")
	eventually(t, "running", func() bool { return f.sched.Heartbeat(m).Running != nil })
	time.Sleep(50 * time.Millisecond)
	f.sched.Ping("tok-signals", SignalSuccess, "")
	eventually(t, "on time", func() bool { return f.sched.Heartbeat(m).State == monitor.StateOnTime })
	last, err := f.store.LastRun(context.Background(), m.ID)
	if err != nil || last.DurationMS == nil || *last.DurationMS < 50 || last.StartedAt == nil {
		t.Fatalf("duration not recorded: %+v %v", last, err)
	}

	f.sched.Ping("tok-signals", SignalFailure, "exit code 3")
	eventually(t, "failed", func() bool { return f.sched.Heartbeat(m).State == monitor.StateFailed })
	if a := f.alert(t); a.Status != "down" || a.Message != "reported a failure: exit code 3" {
		t.Fatalf("unexpected alert %+v", a)
	}
	stats, _ := f.store.RunStatsSince(context.Background(), m.ID, time.Now().Add(-time.Hour))
	if stats.Runs != 2 || stats.OnTime != 1 || stats.Failed != 1 || *stats.Ratio != 0.5 {
		t.Fatalf("stats: %+v", stats)
	}
}

func TestHeartbeatPingsAndRestart(t *testing.T) {
	f := newFixture(t)
	m := f.create(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "sync", Heartbeat: &monitor.Heartbeat{
		Token: "tok-restart", EverySeconds: 3600, GraceSeconds: 300,
	}})
	if err := f.sched.Ping("nope", SignalSuccess, ""); !errors.Is(err, ErrUnknownToken) {
		t.Fatalf("unknown token: %v", err)
	}
	f.sched.Ping("tok-restart", SignalSuccess, "")
	eventually(t, "on time", func() bool { return f.sched.Heartbeat(m).State == monitor.StateOnTime })
	due := f.sched.Heartbeat(m).DueAt

	// A restart picks up from the stored run.
	f.stop()
	f.start(t)
	if st := f.sched.Heartbeat(m); st.State != monitor.StateOnTime || !st.DueAt.Equal(due) {
		t.Fatalf("after a restart: %+v, want on time and due %v", st, due)
	}

	// Pings to a paused heartbeat are accepted and not recorded.
	m.Paused = true
	f.sched.Upsert(m)
	if err := f.sched.Ping("tok-restart", SignalSuccess, ""); err != nil {
		t.Fatal(err)
	}
	if runs, _ := f.store.RecentRuns(context.Background(), m.ID, 10); len(runs) != 1 {
		t.Fatalf("a paused heartbeat recorded a run: %d runs", len(runs))
	}
}

func TestPauseStopsRunner(t *testing.T) {
	f := newFixture(t)
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "db", Check: &monitor.Check{
		Type: monitor.TypeTCP, Target: "127.0.0.1:1", IntervalSeconds: 3600,
	}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m = f.create(t, m)
	m.Paused = true
	f.sched.Upsert(m)
	if got := f.sched.Status(m); got != monitor.StatusPaused {
		t.Fatalf("status = %s, want paused", got)
	}
	if f.sched.CheckNow(m.ID) {
		t.Fatal("a paused healthcheck accepted a check")
	}
}

// Report records runs at the times they happened, e.g. a CronJob's Job
// read a few seconds after it finished.
func TestHeartbeatReport(t *testing.T) {
	f := newFixture(t)
	m := f.create(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "cron", Heartbeat: &monitor.Heartbeat{
		Token: "tok-report", EverySeconds: 3600, GraceSeconds: 300,
	}})
	start := now().Add(-3 * time.Second)
	end := start.Add(1500 * time.Millisecond)
	f.sched.Report(m.ID, SignalStart, start, "")
	f.sched.Report(m.ID, SignalSuccess, end, "Job cron-1 completed")
	eventually(t, "on time", func() bool { return f.sched.Heartbeat(m).State == monitor.StateOnTime })
	last, err := f.store.LastRun(context.Background(), m.ID)
	if err != nil || !last.StartedAt.Equal(start) || !last.FinishedAt.Equal(end) || *last.DurationMS != 1500 || last.Message != "Job cron-1 completed" {
		t.Fatalf("run not recorded at the reported times: %+v %v", last, err)
	}

	// A run that finished within its grace period but was only seen after
	// it was marked missed counts as on time.
	fast := f.create(t, fastHeartbeat("tok-report-fast"))
	f.sched.Ping("tok-report-fast", SignalSuccess, "")
	eventually(t, "missed", func() bool { return f.sched.Heartbeat(fast).State == monitor.StateMissed })
	missed, _ := f.store.LastRun(context.Background(), fast.ID)
	f.sched.Report(fast.ID, SignalSuccess, missed.DueAt.Add(500*time.Millisecond), "")
	eventually(t, "on time", func() bool { return f.sched.Heartbeat(fast).State == monitor.StateOnTime })
	if run, _ := f.store.LastRun(context.Background(), fast.ID); run.ID != missed.ID || !run.OnTime || run.Outcome != monitor.OutcomeSuccess {
		t.Fatalf("the missed run should be on time after all: %+v", run)
	}
}

func TestRecoveryThreshold(t *testing.T) {
	var healthy atomic.Bool
	healthy.Store(true)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer target.Close()

	f := newFixture(t)
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "web", Check: &monitor.Check{
		Type: monitor.TypeHTTP, Target: target.URL, IntervalSeconds: 3600, FailureThreshold: 1, RecoveryThreshold: 3,
	}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m = f.create(t, m)
	status := func(want monitor.Status) func() bool {
		return func() bool { return f.sched.Status(m) == want }
	}
	// From pending, the first good check is enough.
	eventually(t, "up", status(monitor.StatusUp))

	check := func(ok bool) {
		t.Helper()
		healthy.Store(ok)
		before := len(results(t, f, m.ID))
		f.sched.CheckNow(m.ID)
		eventually(t, "the check", func() bool { return len(results(t, f, m.ID)) > before })
	}
	check(false)
	eventually(t, "down", status(monitor.StatusDown))
	if a := f.alert(t); a.Status != "down" {
		t.Fatalf("alert %+v", a)
	}

	// Two good checks, a bad one (which restarts the count), then three good.
	for i, ok := range []bool{true, true, false, true, true} {
		check(ok)
		if got := f.sched.Status(m); got != monitor.StatusDown {
			t.Fatalf("check %d: %s before 3 good checks in a row", i+1, got)
		}
	}
	check(true)
	eventually(t, "up again", status(monitor.StatusUp))
	if a := f.alert(t); a.Status != "up" {
		t.Fatalf("alert %+v", a)
	}
	select {
	case a := <-f.alerts:
		t.Fatalf("flapping alerted: %+v", a)
	case <-time.After(200 * time.Millisecond):
	}
}

func results(t *testing.T, f *fixture, id int64) []monitor.Result {
	t.Helper()
	rs, err := f.store.RecentResults(context.Background(), id, 100)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}
