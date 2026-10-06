package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/incident"
	"github.com/uptimy/agent/internal/monitor"
)

func evidenceMonitor(t *testing.T, st *Store, name string, heartbeat bool) monitor.Monitor {
	t.Helper()
	m := monitor.Monitor{Name: name, Kind: monitor.KindHealthcheck, Source: monitor.SourceUI, Public: true,
		Check: &monitor.Check{Type: "http", Target: "https://example.com", IntervalSeconds: 60, TimeoutSeconds: 5, FailureThreshold: 2}}
	if heartbeat {
		m.Kind, m.Check = monitor.KindHeartbeat, nil
		m.Heartbeat = &monitor.Heartbeat{Token: name, EverySeconds: 60, GraceSeconds: 10, Timezone: "UTC"}
	}
	m, err := st.CreateMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func evidenceExec(t *testing.T, st *Store, query string, args ...any) int64 {
	t.Helper()
	res, err := st.db.ExecContext(context.Background(), query, args...)
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestEvidenceMonitorPage(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	first := evidenceMonitor(t, st, "Zulu", false)
	second := evidenceMonitor(t, st, "Alpha", true)
	third := evidenceMonitor(t, st, "Bravo", false)
	page, err := st.MonitorPage(ctx, 0, 2)
	if err != nil || len(page) != 2 {
		t.Fatalf("first page: %+v %v", page, err)
	}
	if page[0].ID != first.ID || page[1].ID != second.ID || page[0].Check == nil || page[1].Heartbeat == nil {
		t.Fatalf("order/settings: %+v", page)
	}
	if page[0].Check.Target != first.Check.Target || page[1].Heartbeat.Token != second.Heartbeat.Token {
		t.Fatalf("settings not hydrated: %+v", page)
	}
	page, err = st.MonitorPage(ctx, second.ID, 2)
	if err != nil || len(page) != 1 || page[0].ID != third.ID {
		t.Fatalf("next page: %+v %v", page, err)
	}
	page, err = st.MonitorPage(ctx, third.ID, 2)
	if err != nil || page == nil || len(page) != 0 {
		t.Fatalf("empty page: %+v %v", page, err)
	}
}

type evidenceWindow func(context.Context, int64, time.Time, time.Time, int, int) ([]string, error)

func evidenceWindows(st *Store) map[string]evidenceWindow {
	return map[string]evidenceWindow{
		"results": func(ctx context.Context, id int64, from, to time.Time, limit, offset int) ([]string, error) {
			rows, err := st.ResultsWindow(ctx, id, from, to, limit, offset)
			out := []string{}
			for _, row := range rows {
				out = append(out, row.Message)
			}
			return out, err
		},
		"runs": func(ctx context.Context, id int64, from, to time.Time, limit, offset int) ([]string, error) {
			rows, err := st.RunsWindow(ctx, id, from, to, limit, offset)
			out := []string{}
			for _, row := range rows {
				out = append(out, row.Message)
			}
			return out, err
		},
		"events": func(ctx context.Context, id int64, from, to time.Time, limit, offset int) ([]string, error) {
			rows, err := st.EventsWindow(ctx, id, from, to, limit, offset)
			out := []string{}
			for _, row := range rows {
				out = append(out, row.Message)
			}
			return out, err
		},
	}
}

func TestEvidenceWindows(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	m := evidenceMonitor(t, st, "target", false)
	other := evidenceMonitor(t, st, "other", true)
	from := time.UnixMilli(1000).UTC()
	to := from.Add(3 * time.Millisecond)
	times := []time.Time{to, from.Add(time.Millisecond), from.Add(-time.Millisecond), from, from.Add(time.Millisecond), from.Add(2 * time.Millisecond)}
	for _, id := range []int64{m.ID, other.ID} {
		for i, at := range times {
			message := fmt.Sprint(i)
			if err := st.InsertResult(ctx, monitor.Result{MonitorID: id, Time: at, OK: true, LatencyMS: 13, Message: message}); err != nil {
				t.Fatal(err)
			}
			r := monitor.Run{MonitorID: id, Outcome: monitor.OutcomeSuccess, OnTime: true, Message: message}
			switch i % 3 {
			case 0:
				r.DueAt = &at
			case 1:
				r.StartedAt = &at
			case 2:
				r.FinishedAt = &at
			}
			if _, err := st.InsertRun(ctx, r); err != nil {
				t.Fatal(err)
			}
			if _, err := st.InsertEvent(ctx, monitor.Event{MonitorID: id, Time: at, Status: monitor.StatusUp, Message: message}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, window := range evidenceWindows(st) {
		t.Run(name, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				id            int64
				from, to      time.Time
				limit, offset int
				want          []string
			}{
				{"all", m.ID, from, to, 501, 0, []string{"3", "1", "4", "5"}},
				{"first", m.ID, from, to, 2, 0, []string{"3", "1"}},
				{"next", m.ID, from, to, 2, 2, []string{"4", "5"}},
				{"tie", m.ID, from.Add(time.Millisecond), from.Add(2 * time.Millisecond), 1, 1, []string{"4"}},
				{"fractional start", m.ID, from.Add(time.Nanosecond), to, 501, 0, []string{"1", "4", "5"}},
				{"fractional end", m.ID, from, from.Add(time.Millisecond + time.Nanosecond), 501, 0, []string{"3", "1", "4"}},
				{"offset empty", m.ID, from, to, 501, 10000, []string{}},
				{"missing", 99999, from, to, 1, 0, []string{}},
				{"zero window", m.ID, from, from, 1, 0, []string{}},
				{"reversed", m.ID, to, from, 1, 0, []string{}},
			} {
				t.Run(tc.name, func(t *testing.T) {
					got, err := window(ctx, tc.id, tc.from, tc.to, tc.limit, tc.offset)
					if err != nil || !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("got %v %v, want %v", got, err, tc.want)
					}
				})
			}
		})
	}
	results, err := st.ResultsWindow(ctx, m.ID, from, to, 1, 0)
	if err != nil || len(results) != 1 || results[0].MonitorID != m.ID || !results[0].Time.Equal(from) || !results[0].OK || results[0].LatencyMS != 13 {
		t.Fatalf("result fields: %+v %v", results, err)
	}
	runs, err := st.RunsWindow(ctx, m.ID, from, to, 1, 0)
	if err != nil || len(runs) != 1 || runs[0].MonitorID != m.ID || runs[0].DueAt == nil || !runs[0].DueAt.Equal(from) || runs[0].Outcome != monitor.OutcomeSuccess || !runs[0].OnTime {
		t.Fatalf("run fields: %+v %v", runs, err)
	}
	events, err := st.EventsWindow(ctx, m.ID, from, to, 1, 0)
	if err != nil || len(events) != 1 || events[0].ID == 0 || events[0].MonitorID != m.ID || !events[0].Time.Equal(from) || events[0].Status != monitor.StatusUp {
		t.Fatalf("event fields: %+v %v", events, err)
	}
}

func TestEvidenceIncidents(t *testing.T) {
	st := open(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := evidenceMonitor(t, st, "linked", false)
	first := evidenceExec(t, st, "INSERT INTO incidents (title, severity, created_at) VALUES ('first', 'high', 100)")
	resolved := evidenceExec(t, st, "INSERT INTO incidents (title, severity, created_at, resolved_at) VALUES ('resolved', 'low', 200, 400)")
	last := evidenceExec(t, st, "INSERT INTO incidents (title, severity, created_at) VALUES ('empty', 'medium', 0)")
	evidenceExec(t, st, "INSERT INTO incident_monitors (incident_id, monitor_id) VALUES (?, ?)", first, m.ID)
	u1 := evidenceExec(t, st, "INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES (?, 'identified', 'newer', 300)", first)
	u2 := evidenceExec(t, st, "INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES (?, 'investigating', 'oldest', 100)", first)
	u3 := evidenceExec(t, st, "INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES (?, 'monitoring', 'latest tie', 300)", first)
	evidenceExec(t, st, "INSERT INTO incident_updates (incident_id, status, message, created_at) VALUES (?, 'resolved', 'done', 400)", resolved)
	page, err := st.ListActiveIncidentPage(ctx, 0, 1)
	if err != nil || len(page) != 1 || page[0].ID != first || page[0].Status != incident.Monitoring || len(page[0].Updates) != 0 || len(page[0].MonitorIDs) != 0 {
		t.Fatalf("summary: %+v %v", page, err)
	}
	page, err = st.ListActiveIncidentPage(ctx, first, 501)
	if err != nil || len(page) != 1 || page[0].ID != last || page[0].Status != "" {
		t.Fatalf("open pagination: %+v %v", page, err)
	}
	page, err = st.ListActiveIncidentPage(ctx, last, 1)
	if err != nil || page == nil || len(page) != 0 {
		t.Fatalf("empty summaries: %+v %v", page, err)
	}
	in, err := st.IncidentEvidence(ctx, first, 1, 0)
	if err != nil || in.Status != incident.Monitoring || len(in.Updates) != 1 || in.Updates[0].ID != u2 || !reflect.DeepEqual(in.MonitorIDs, []int64{m.ID}) {
		t.Fatalf("oldest page/current status: %+v %v", in, err)
	}
	if in.Title != "first" || in.Severity != incident.High || !in.CreatedAt.Equal(time.UnixMilli(100)) || in.ResolvedAt != nil || !in.Updates[0].CreatedAt.Equal(time.UnixMilli(100)) {
		t.Fatalf("incident fields: %+v", in)
	}
	in, err = st.IncidentEvidence(ctx, first, 2, 1)
	if err != nil || len(in.Updates) != 2 || in.Updates[0].ID != u1 || in.Updates[1].ID != u3 || in.Status != incident.Monitoring {
		t.Fatalf("timeline ties: %+v %v", in, err)
	}
	in, err = st.IncidentEvidence(ctx, first, 501, 10000)
	if err != nil || len(in.Updates) != 0 || in.Status != incident.Monitoring || len(in.MonitorIDs) != 1 {
		t.Fatalf("empty timeline still has current status/links: %+v %v", in, err)
	}
	in, err = st.IncidentEvidence(ctx, resolved, 1, 0)
	if err != nil || in.Status != incident.Resolved || in.ResolvedAt == nil || !in.ResolvedAt.Equal(time.UnixMilli(400)) {
		t.Fatalf("resolved evidence: %+v %v", in, err)
	}
	in, err = st.IncidentEvidence(ctx, last, 1, 0)
	if err != nil || in.Status != "" || in.Updates == nil || len(in.Updates) != 0 || in.MonitorIDs == nil || len(in.MonitorIDs) != 0 {
		t.Fatalf("empty incident: %+v %v", in, err)
	}
	if _, err := st.IncidentEvidence(ctx, 99999, 1, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing incident: %v", err)
	}
}

func TestEvidenceBounds(t *testing.T) {
	st := open(t)
	ctx := context.Background()
	err := st.inTx(ctx, func(tx *sql.Tx) error {
		for i := 1; i <= 502; i++ {
			for _, query := range []string{
				"INSERT INTO monitors (id, kind, name, created_at, updated_at) VALUES (?, 'heartbeat', 'bounded', 0, 0)",
				"INSERT INTO heartbeats (monitor_id, token, every_seconds, grace_seconds) VALUES (?1, CAST(?1 AS TEXT), 60, 10)",
				"INSERT INTO incidents (id, title, created_at) VALUES (?, 'bounded', 0)",
				"INSERT INTO incident_monitors (incident_id, monitor_id) VALUES (1, ?)",
				"INSERT INTO incident_updates (incident_id, message, created_at) VALUES (1, CAST(? AS TEXT), 1000)",
				"INSERT INTO results (monitor_id, ts, ok, latency_ms, message) VALUES (1, 1000, 1, 1, CAST(? AS TEXT))",
				"INSERT INTO runs (monitor_id, ts, outcome, message) VALUES (1, 1000, 'missed', CAST(? AS TEXT))",
				"INSERT INTO events (monitor_id, ts, status, message) VALUES (1, 1000, 'up', CAST(? AS TEXT))",
			} {
				if _, err := tx.ExecContext(ctx, query, i); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	monitors, err := st.MonitorPage(ctx, 0, 501)
	if err != nil || len(monitors) != 501 || monitors[500].ID != 501 {
		t.Fatalf("monitor sentinel: len %d %v", len(monitors), err)
	}
	monitors, err = st.MonitorPage(ctx, 501, 501)
	if err != nil || len(monitors) != 1 || monitors[0].ID != 502 {
		t.Fatalf("monitor tail: %+v %v", monitors, err)
	}
	incidents, err := st.ListActiveIncidentPage(ctx, 0, 501)
	if err != nil || len(incidents) != 501 || incidents[500].ID != 501 {
		t.Fatalf("incident sentinel: len %d %v", len(incidents), err)
	}
	for name, window := range evidenceWindows(st) {
		t.Run(name, func(t *testing.T) {
			from, to := time.UnixMilli(1000), time.UnixMilli(1001)
			rows, err := window(ctx, 1, from, to, 501, 0)
			if err != nil || len(rows) != 501 || rows[500] != "501" {
				t.Fatalf("window sentinel: len %d %v", len(rows), err)
			}
			rows, err = window(ctx, 1, from, to, 501, 500)
			if err != nil || !reflect.DeepEqual(rows, []string{"501", "502"}) {
				t.Fatalf("window tail: %v %v", rows, err)
			}
		})
	}
	in, err := st.IncidentEvidence(ctx, 1, 501, 0)
	if err != nil || len(in.Updates) != 501 || len(in.MonitorIDs) != 501 || in.Updates[500].Message != "501" || in.MonitorIDs[500] != 501 {
		t.Fatalf("incident bounds: updates %d links %d %v", len(in.Updates), len(in.MonitorIDs), err)
	}
	in, err = st.IncidentEvidence(ctx, 1, 1, 501)
	if err != nil || len(in.Updates) != 1 || in.Updates[0].Message != "502" || len(in.MonitorIDs) != 501 {
		t.Fatalf("independent link bound: updates %+v links %d %v", in.Updates, len(in.MonitorIDs), err)
	}
}

func TestEvidenceValidationAndCancellation(t *testing.T) {
	st := open(t)
	evidenceExec(t, st, "INSERT INTO incidents (id, title, created_at) VALUES (1, 'test', 0)")
	from, to := time.UnixMilli(1000), time.UnixMilli(2000)
	methods := map[string]func(context.Context, int, int) error{
		"monitors": func(ctx context.Context, limit, offset int) error {
			_, err := st.MonitorPage(ctx, 0, limit)
			return err
		},
		"incidents": func(ctx context.Context, limit, offset int) error {
			_, err := st.ListActiveIncidentPage(ctx, 0, limit)
			return err
		},
		"incident evidence": func(ctx context.Context, limit, offset int) error {
			_, err := st.IncidentEvidence(ctx, 1, limit, offset)
			return err
		},
	}
	for name, window := range evidenceWindows(st) {
		methods[name] = func(ctx context.Context, limit, offset int) error {
			_, err := window(ctx, 1, from, to, limit, offset)
			return err
		}
	}
	for name, method := range methods {
		t.Run(name, func(t *testing.T) {
			for _, limit := range []int{-1, 0, 502} {
				if err := method(context.Background(), limit, 0); err == nil {
					t.Fatalf("accepted limit %d", limit)
				}
			}
			if name != "monitors" && name != "incidents" {
				for _, offset := range []int{-1, 10001} {
					if err := method(context.Background(), 1, offset); err == nil {
						t.Fatalf("accepted offset %d", offset)
					}
				}
			}
			if err := method(context.Background(), 501, 10000); err != nil {
				t.Fatalf("valid maximums: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if err := method(ctx, 1, 0); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation: %v", err)
			}
			ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			defer cancel()
			if err := method(ctx, 1, 0); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("deadline: %v", err)
			}
		})
	}
}
