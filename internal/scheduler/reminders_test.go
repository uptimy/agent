package scheduler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
)

func TestRemindersAndAcknowledgement(t *testing.T) {
	ctx := context.Background()
	var healthy atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if !healthy.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer target.Close()

	f := newFixture(t)
	// Remind every second (the API allows 5 minutes at least).
	ns, err := f.store.ListNotifiers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sink := ns[0]
	sink.RepeatSeconds = 1
	if _, err := f.store.UpdateNotifier(ctx, sink); err != nil {
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
	down := f.alert(t)
	if down.Status != "down" || down.Type != "" {
		t.Fatalf("first alert: %+v", down)
	}

	// Still down a second later: a reminder.
	r := f.alert(t)
	if r.Type != notify.AlertReminder || r.Status != "down" || !r.DownSince.Equal(down.Time) || r.ID == down.ID {
		t.Fatalf("reminder: %+v", r)
	}
	if !strings.HasPrefix(r.Title(), "web is still down (for ") {
		t.Fatalf("reminder title: %q", r.Title())
	}

	// Acknowledged: the channel hears who's on it, and reminders stop.
	if err := f.sched.Acknowledge(m, "dana"); err != nil {
		t.Fatal(err)
	}
	var ack notify.Alert
	for ack.Type != notify.AlertAcknowledged { // a reminder may have been on its way
		ack = f.alert(t)
	}
	if ack.AcknowledgedBy != "dana" || ack.Title() != "web: dana is on it" {
		t.Fatalf("acknowledgement: %+v", ack)
	}
	select {
	case a := <-f.alerts:
		t.Fatalf("reminded after the acknowledgement: %+v", a)
	case <-time.After(1500 * time.Millisecond):
	}
	if a, err := f.store.Acknowledgement(ctx, m.ID); err != nil || a == nil || a.By != "dana" {
		t.Fatalf("acknowledgement stored: %+v %v", a, err)
	}

	// Recovery ends the outage and its acknowledgement.
	healthy.Store(true)
	f.sched.CheckNow(m.ID)
	if a := f.alert(t); a.Status != "up" {
		t.Fatalf("recovery: %+v", a)
	}
	if a, _ := f.store.Acknowledgement(ctx, m.ID); a != nil {
		t.Fatalf("acknowledgement outlived the outage: %+v", a)
	}
	if err := f.sched.Acknowledge(m, "dana"); !errors.Is(err, ErrNotDown) {
		t.Fatalf("acknowledging an up monitor: %v", err)
	}
}
