package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
)

func TestAlertRouting(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	mk := func(name string) int64 {
		m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: name, Check: &monitor.Check{Type: monitor.TypeTCP, Target: "x:1"}}
		if err := m.Normalize(); err != nil {
			t.Fatal(err)
		}
		m, err := st.CreateMonitor(ctx, m)
		if err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	api, db := mk("API"), mk("DB")
	notifier := func(n notify.Notifier) notify.Notifier {
		n.Type, n.Config = "webhook", notify.Config{URL: "https://example.com/hook"}
		if err := n.Normalize(); err != nil {
			t.Fatal(err)
		}
		n, err := st.CreateNotifier(ctx, n)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	everyone := notifier(notify.Notifier{Name: "Slack", Enabled: true, AllMonitors: true})
	pager := notifier(notify.Notifier{Name: "PagerDuty", Enabled: true, MonitorIDs: []int64{db}})
	notifier(notify.Notifier{Name: "Off", Enabled: false, AllMonitors: true})

	names := func(monitorID int64) []string {
		ns, err := st.NotifiersFor(ctx, monitorID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, n := range ns {
			out = append(out, n.Name)
		}
		return out
	}
	if got := names(api); len(got) != 1 || got[0] != "Slack" {
		t.Fatalf("API alerts %v", got)
	}
	if got := names(db); len(got) != 2 {
		t.Fatalf("DB alerts %v", got)
	}

	// From the monitor's side: add the API to PagerDuty. Notifiers for
	// every monitor can't be limited from there.
	if err := st.SetMonitorNotifiers(ctx, api, []int64{pager.ID, everyone.ID}); err != nil {
		t.Fatal(err)
	}
	if ids, _ := st.MonitorNotifierIDs(ctx, api); len(ids) != 1 || ids[0] != pager.ID {
		t.Fatalf("API's notifiers %v", ids)
	}
	if n, _ := st.GetNotifier(ctx, pager.ID); len(n.MonitorIDs) != 2 {
		t.Fatalf("PagerDuty's monitors %v", n.MonitorIDs)
	}

	// Switching to every monitor forgets the list.
	pager.AllMonitors = true
	if _, err := st.UpdateNotifier(ctx, pager); err != nil {
		t.Fatal(err)
	}
	if ids, _ := st.MonitorNotifierIDs(ctx, db); len(ids) != 0 {
		t.Fatalf("DB still lists %v", ids)
	}
	if got := names(api); len(got) != 2 {
		t.Fatalf("API alerts %v", got)
	}

	if _, err := st.CreateNotifier(ctx, notify.Notifier{Name: "x", Type: "webhook", MonitorIDs: []int64{999}}); !errors.Is(err, ErrUnknownMonitor) {
		t.Fatalf("unknown monitor: %v", err)
	}
}

func TestPruneKeepsCurrentStatus(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "web", Check: &monitor.Check{Type: monitor.TypeTCP, Target: "x:1"}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := st.CreateMonitor(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().AddDate(0, 0, -60).UTC().Truncate(time.Millisecond)
	for i, s := range []monitor.Status{monitor.StatusUp, monitor.StatusDown} {
		if _, err := st.InsertEvent(ctx, monitor.Event{MonitorID: m.ID, Time: old.Add(time.Duration(i) * time.Minute), Status: s}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Prune(ctx, time.Now().AddDate(0, 0, -30)); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListEvents(ctx, m.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Status != monitor.StatusDown {
		t.Fatalf("after pruning: %+v; the newest event is the monitor's status and must stay", events)
	}
}
