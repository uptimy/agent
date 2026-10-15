package delivery

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
	"github.com/uptimy/agent/internal/store"
)

// sink is a webhook that answers with the statuses it's given, in turn
// (the last one repeats), and records what it accepted.
type sink struct {
	*httptest.Server
	mu       sync.Mutex
	statuses []int
	calls    int
	got      []notify.Alert
}

func newSink(t *testing.T, statuses ...int) *sink {
	t.Helper()
	s := &sink{statuses: statuses}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		status := http.StatusOK
		if len(s.statuses) > 0 {
			status = s.statuses[min(s.calls, len(s.statuses)-1)]
		}
		s.calls++
		if status < 300 {
			var a notify.Alert
			json.NewDecoder(r.Body).Decode(&a)
			s.got = append(s.got, a)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *sink) set(statuses ...int) {
	s.mu.Lock()
	s.statuses, s.calls = statuses, 0
	s.mu.Unlock()
}

func (s *sink) received() []notify.Alert {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]notify.Alert(nil), s.got...)
}

type fixture struct {
	t       *testing.T
	st      *store.Store
	monitor monitor.Monitor
	d       *Dispatcher
	stop    func()
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "web", Check: &monitor.Check{Type: monitor.TypeTCP, Target: "x:1"}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	if m, err = st.CreateMonitor(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, st: st, monitor: m}
	f.start()
	return f
}

// start (re)starts the dispatcher, as a restart would.
func (f *fixture) start() { f.startWith([]time.Duration{20 * time.Millisecond}) }

// startWith (re)starts the dispatcher with its own retry backoff.
func (f *fixture) startWith(backoff []time.Duration) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f.d = New(f.st, notify.NewSender(log), events.NewHub(), log)
	f.d.Backoff = backoff
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { f.d.Run(ctx, time.Second); close(done) }()
	f.stop = func() { cancel(); <-done }
	f.t.Cleanup(f.stop)
}

func (f *fixture) channel(name, url string) notify.Notifier {
	f.t.Helper()
	n := notify.Notifier{Name: name, Type: "webhook", Enabled: true, AllMonitors: true, Config: notify.Config{URL: url}}
	if err := n.Normalize(); err != nil {
		f.t.Fatal(err)
	}
	n, err := f.st.CreateNotifier(context.Background(), n)
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

// alert records a status change with its alert, as the scheduler does.
func (f *fixture) alert(status monitor.Status, held bool, maxAge time.Duration) monitor.Event {
	f.t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	e, err := f.st.InsertEventAlert(context.Background(),
		monitor.Event{MonitorID: f.monitor.ID, Time: now, Status: status},
		notify.Alert{MonitorID: f.monitor.ID, MonitorName: "web", Kind: "healthcheck", Status: string(status), Time: now},
		held, maxAge)
	if err != nil {
		f.t.Fatal(err)
	}
	f.d.Wake()
	return e
}

// deliveries returns the event's deliveries by channel name.
func (f *fixture) deliveries(e monitor.Event) map[string]notify.Delivery {
	f.t.Helper()
	all, err := f.st.EventDeliveries(context.Background(), []int64{e.ID})
	if err != nil {
		f.t.Fatal(err)
	}
	out := map[string]notify.Delivery{}
	for _, d := range all[e.ID] {
		out[d.NotifierName] = d
	}
	return out
}

func (f *fixture) waitState(e monitor.Event, channel string, want notify.DeliveryState) notify.Delivery {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		d := f.deliveries(e)[channel]
		if d.State == want {
			return d
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("%s on %s: state %q, want %q (detail %q)", e.Status, channel, d.State, want, d.Detail)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRetriesUntilDelivered(t *testing.T) {
	f := newFixture(t)
	flaky := newSink(t, 502, 503, 200)
	ok := newSink(t)
	f.channel("flaky", flaky.URL)
	f.channel("ok", ok.URL)

	e := f.alert(monitor.StatusDown, false, time.Hour)
	d := f.waitState(e, "flaky", notify.DeliverySent)
	if d.Attempts != 3 {
		t.Fatalf("attempts = %d, want 3", d.Attempts)
	}
	f.waitState(e, "ok", notify.DeliverySent)
	// A failing channel doesn't make the others send twice.
	if got := ok.received(); len(got) != 1 || got[0].Status != "down" || got[0].ID == 0 {
		t.Fatalf("ok channel got %+v", got)
	}
	if got := flaky.received(); len(got) != 1 || got[0].ID != ok.received()[0].ID {
		t.Fatalf("flaky channel got %+v; the alert ID must be the same everywhere", got)
	}
}

func TestPermanentErrorFailsAtOnce(t *testing.T) {
	f := newFixture(t)
	gone := newSink(t, 404)
	f.channel("gone", gone.URL)

	e := f.alert(monitor.StatusDown, false, time.Hour)
	d := f.waitState(e, "gone", notify.DeliveryFailed)
	if d.Attempts != 1 || d.Detail == "" {
		t.Fatalf("delivery %+v: want one attempt and the error", d)
	}
	time.Sleep(100 * time.Millisecond)
	gone.mu.Lock()
	defer gone.mu.Unlock()
	if gone.calls != 1 {
		t.Fatalf("a 404 was retried: %d calls", gone.calls)
	}
}

func TestRateLimitIsRetried(t *testing.T) {
	f := newFixture(t)
	limited := newSink(t, 429, 200)
	f.channel("limited", limited.URL)
	e := f.alert(monitor.StatusDown, false, time.Hour)
	if d := f.waitState(e, "limited", notify.DeliverySent); d.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", d.Attempts)
	}
}

func TestRecoveryWaitsForItsOutage(t *testing.T) {
	f := newFixture(t)
	s := newSink(t, 500)
	f.channel("slack", s.URL)

	down := f.alert(monitor.StatusDown, false, time.Hour)
	up := f.alert(monitor.StatusUp, false, time.Hour)
	time.Sleep(150 * time.Millisecond) // several retries of the down alert
	if got := f.deliveries(up)["slack"]; got.State != notify.DeliveryPending || got.Attempts != 0 {
		t.Fatalf("the recovery was tried before its outage was delivered: %+v", got)
	}

	s.set(200)
	f.waitState(down, "slack", notify.DeliverySent)
	f.waitState(up, "slack", notify.DeliverySent)
	got := s.received()
	if len(got) != 2 || got[0].Status != "down" || got[1].Status != "up" {
		t.Fatalf("received %+v, want down then up", got)
	}
}

func TestExpiredOutageSuppressesItsRecovery(t *testing.T) {
	f := newFixture(t)
	s := newSink(t, 500)
	f.channel("slack", s.URL)

	down := f.alert(monitor.StatusDown, false, 50*time.Millisecond)
	f.waitState(down, "slack", notify.DeliveryExpired)
	s.set(200)
	up := f.alert(monitor.StatusUp, false, time.Hour)
	d := f.waitState(up, "slack", notify.DeliverySuppressed)
	if d.Detail == "" {
		t.Fatal("a suppressed delivery says why")
	}
	if got := s.received(); len(got) != 0 {
		t.Fatalf("sent %+v", got)
	}
}

func TestPendingAlertsSurviveARestart(t *testing.T) {
	f := newFixture(t)
	s := newSink(t, 503)
	f.channel("slack", s.URL)
	down := f.alert(monitor.StatusDown, false, time.Hour)
	f.waitState(down, "slack", notify.DeliveryPending)
	eventually(t, "a failed attempt", func() bool { return f.deliveries(down)["slack"].Attempts > 0 })

	f.stop()
	s.set(200)
	f.start()
	f.waitState(down, "slack", notify.DeliverySent)
	if got := s.received(); len(got) != 1 {
		t.Fatalf("received %d alerts, want 1", len(got))
	}
}

func TestDisabledChannelIsSkipped(t *testing.T) {
	f := newFixture(t)
	s := newSink(t, 500)
	n := f.channel("slack", s.URL)
	down := f.alert(monitor.StatusDown, false, time.Hour)
	n.Enabled = false
	if _, err := f.st.UpdateNotifier(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	f.waitState(down, "slack", notify.DeliverySuppressed)
}

func TestHeldOutageAndRecoveryAreBothSuppressed(t *testing.T) {
	f := newFixture(t)
	s := newSink(t)
	f.channel("slack", s.URL)
	down := f.alert(monitor.StatusDown, true, time.Hour)
	up := f.alert(monitor.StatusUp, false, time.Hour)
	f.waitState(down, "slack", notify.DeliverySuppressed)
	f.waitState(up, "slack", notify.DeliverySuppressed)
	time.Sleep(50 * time.Millisecond)
	if got := s.received(); len(got) != 0 {
		t.Fatalf("sent %+v", got)
	}
}

func TestRetryNow(t *testing.T) {
	f := newFixture(t)
	s := newSink(t, 404)
	f.channel("slack", s.URL)
	down := f.alert(monitor.StatusDown, false, time.Hour)
	d := f.waitState(down, "slack", notify.DeliveryFailed)

	s.set(200)
	if err := f.st.RetryDelivery(context.Background(), d.AlertID, d.NotifierID, time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	f.d.Wake()
	f.waitState(down, "slack", notify.DeliverySent)
	if err := f.st.RetryDelivery(context.Background(), d.AlertID, d.NotifierID, time.Now(), time.Hour); err == nil {
		t.Fatal("a sent delivery can't be retried")
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// reminder queues a reminder about the outage that began with down.
func (f *fixture) reminder(down monitor.Event, notifierID int64) {
	f.t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	a := notify.Alert{MonitorID: f.monitor.ID, MonitorName: "web", Kind: "healthcheck", Status: "down",
		Type: notify.AlertReminder, Time: now, DownSince: down.Time}
	if err := f.st.InsertAlert(context.Background(), down.ID, a, []int64{notifierID}, time.Hour); err != nil {
		f.t.Fatal(err)
	}
	f.d.Wake()
}

// The down alert expired during a channel's outage, but a later reminder got
// through: the channel knows, so it hears the recovery too.
func TestRecoveryAfterAReminderGotThrough(t *testing.T) {
	f := newFixture(t)
	s := newSink(t, 500)
	n := f.channel("slack", s.URL)
	down := f.alert(monitor.StatusDown, false, 50*time.Millisecond)
	f.waitState(down, "slack", notify.DeliveryExpired)

	s.set(200)
	f.reminder(down, n.ID)
	eventually(t, "the reminder", func() bool { return len(s.received()) == 1 })
	up := f.alert(monitor.StatusUp, false, time.Hour)
	f.waitState(up, "slack", notify.DeliverySent)
	got := s.received()
	if len(got) != 2 || got[0].Type != notify.AlertReminder || got[1].Status != "up" {
		t.Fatalf("received %+v", got)
	}
}

// A reminder still waiting when the monitor recovers would be wrong by the
// time it arrives: it's dropped, and the recovery goes out.
func TestStatusChangeDropsWaitingReminders(t *testing.T) {
	f := newFixture(t)
	// A quick first retry, then a long wait: once the reminder has failed
	// twice it's waiting, not in flight, when the monitor recovers.
	f.stop()
	f.startWith([]time.Duration{20 * time.Millisecond, 10 * time.Minute})
	s := newSink(t)
	n := f.channel("slack", s.URL)
	down := f.alert(monitor.StatusDown, false, time.Hour)
	f.waitState(down, "slack", notify.DeliverySent)

	s.set(503)
	f.reminder(down, n.ID)
	eventually(t, "a reminder that failed twice", func() bool {
		ds, _ := f.st.EventDeliveries(context.Background(), []int64{down.ID})
		for _, d := range ds[down.ID] {
			if d.AlertType == notify.AlertReminder && d.Attempts >= 2 {
				return true
			}
		}
		return false
	})
	// Recover while the channel still fails, so the reminder is still waiting
	// (a retry can't slip through first); then let the recovery through.
	up := f.alert(monitor.StatusUp, false, time.Hour)
	s.set(200)
	f.waitState(up, "slack", notify.DeliverySent)
	ds, _ := f.st.EventDeliveries(context.Background(), []int64{down.ID})
	for _, d := range ds[down.ID] {
		if d.AlertType == notify.AlertReminder && d.State != notify.DeliverySuppressed {
			t.Fatalf("reminder: %+v", d)
		}
	}
	got := s.received()
	if len(got) != 2 || got[1].Status != "up" {
		t.Fatalf("received %+v", got)
	}
}
