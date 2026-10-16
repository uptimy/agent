package scheduler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/maintenance"
	"github.com/uptimy/agent/internal/monitor"
)

func TestMaintenanceHoldsAlerts(t *testing.T) {
	ctx := context.Background()
	var healthy atomic.Bool
	healthy.Store(true)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer target.Close()

	f := newFixture(t)
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "web", Check: &monitor.Check{
		Type: monitor.TypeHTTP, Target: target.URL, IntervalSeconds: 3600, FailureThreshold: 1,
	}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m = f.create(t, m)
	status := func(want monitor.Status) func() bool {
		return func() bool { return f.sched.Status(m) == want }
	}
	eventually(t, "up", status(monitor.StatusUp))

	w, err := f.store.CreateMaintenance(ctx, maintenance.Window{
		Title: "Upgrade", StartsAt: time.Now().Add(-time.Minute), EndsAt: time.Now().Add(time.Hour), MonitorIDs: []int64{m.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sched.ReloadMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if !f.sched.InMaintenance(m.ID) {
		t.Fatal("not in maintenance")
	}

	// Down and back up during maintenance: recorded, nobody alerted.
	healthy.Store(false)
	f.sched.CheckNow(m.ID)
	eventually(t, "down", status(monitor.StatusDown))
	healthy.Store(true)
	f.sched.CheckNow(m.ID)
	eventually(t, "up again", status(monitor.StatusUp))
	healthy.Store(false)
	f.sched.CheckNow(m.ID)
	eventually(t, "down again", status(monitor.StatusDown))
	select {
	case a := <-f.alerts:
		t.Fatalf("alerted during maintenance: %+v", a)
	case <-time.After(300 * time.Millisecond):
	}

	// Ending the window early sends the held alert: it's still down.
	w.EndsAt = time.Now()
	if _, err := f.store.UpdateMaintenance(ctx, w); err != nil {
		t.Fatal(err)
	}
	if err := f.sched.ReloadMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	if a := f.alert(t); a.Status != "down" || a.Message != "still down after maintenance" {
		t.Fatalf("held alert: %+v", a)
	}
	// And its recovery is announced, since the down alert went out.
	healthy.Store(true)
	f.sched.CheckNow(m.ID)
	if a := f.alert(t); a.Status != "up" {
		t.Fatalf("recovery: %+v", a)
	}
}

// A restart during maintenance used to forget held alerts: they're in the
// outbox now.
func TestHeldAlertSurvivesRestart(t *testing.T) {
	ctx := context.Background()
	var healthy atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer target.Close()

	f := newFixture(t)
	w, err := f.store.CreateMaintenance(ctx, maintenance.Window{
		Title: "Upgrade", StartsAt: time.Now().Add(-time.Minute), EndsAt: time.Now().Add(time.Hour), AllMonitors: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.sched.ReloadMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "web", Check: &monitor.Check{
		Type: monitor.TypeHTTP, Target: target.URL, IntervalSeconds: 3600, FailureThreshold: 1,
	}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m = f.create(t, m)
	eventually(t, "down", func() bool { return f.sched.Status(m) == monitor.StatusDown })

	// Restart, then end the window: the held alert goes out.
	f.stop()
	w.EndsAt = time.Now()
	if _, err := f.store.UpdateMaintenance(ctx, w); err != nil {
		t.Fatal(err)
	}
	f.start(t)
	if a := f.alert(t); a.Status != "down" || a.Message != "still down after maintenance" {
		t.Fatalf("held alert after restart: %+v", a)
	}
	select {
	case a := <-f.alerts:
		t.Fatalf("sent twice: %+v", a)
	case <-time.After(300 * time.Millisecond):
	}
}
