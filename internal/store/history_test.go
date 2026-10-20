package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/maintenance"
	"github.com/uptimy/agent/internal/monitor"
)

func createHealthcheck(t *testing.T, st *Store, name string) monitor.Monitor {
	t.Helper()
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: name, Check: &monitor.Check{Type: monitor.TypeTCP, Target: "x:1"}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := st.CreateMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func createHeartbeat(t *testing.T, st *Store, name string) monitor.Monitor {
	t.Helper()
	m := monitor.Monitor{Kind: monitor.KindHeartbeat, Name: name,
		Heartbeat: &monitor.Heartbeat{Token: monitor.NewToken(), EverySeconds: 3600, GraceSeconds: 300}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := st.CreateMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// dayStart is the start of the UTC day offset days from today.
func dayStart(offset int) time.Time {
	return time.Now().UTC().Truncate(day).AddDate(0, 0, offset)
}

// insertDay records checks on a day: latencies of the ones that were up,
// and down checks, a minute apart from start.
func insertDay(t *testing.T, st *Store, id int64, start time.Time, up []int64, down int) {
	t.Helper()
	ts := start
	for _, ms := range up {
		if err := st.InsertResult(context.Background(), monitor.Result{MonitorID: id, Time: ts, OK: true, LatencyMS: ms}); err != nil {
			t.Fatal(err)
		}
		ts = ts.Add(time.Minute)
	}
	for range down {
		if err := st.InsertResult(context.Background(), monitor.Result{MonitorID: id, Time: ts, LatencyMS: 9999, Message: "refused"}); err != nil {
			t.Fatal(err)
		}
		ts = ts.Add(time.Minute)
	}
}

func history(t *testing.T, st *Store, id int64, days int) Days {
	t.Helper()
	h, err := st.History(context.Background(), id, days)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != days {
		t.Fatalf("%d days, want %d", len(h), days)
	}
	return h
}

// summary is what a day shows, for comparing them.
func summary(d Daily) string {
	f := func(p *int64) string {
		if p == nil {
			return "-"
		}
		return fmt.Sprint(*p)
	}
	ratio, avg := "-", "-"
	if d.Ratio != nil {
		ratio = fmt.Sprintf("%.4f", *d.Ratio)
	}
	if d.AvgMS != nil {
		avg = fmt.Sprintf("%.1f", *d.AvgMS)
	}
	return fmt.Sprintf("%s n=%d good=%d failed=%d missed=%d ratio=%s avg=%s p50=%s p95=%s p99=%s max=%s",
		d.Date, d.Count, d.Good, d.Failed, d.Missed, ratio, avg, f(d.P50MS), f(d.P95MS), f(d.P99MS), f(d.MaxMS))
}

func sameDays(t *testing.T, label string, got, want Days) {
	t.Helper()
	for i := range want {
		if summary(got[i]) != summary(want[i]) {
			t.Errorf("%s: day %d\n got  %s\n want %s", label, i, summary(got[i]), summary(want[i]))
		}
	}
}

// Rolled-up days read the same as the raw days they came from, and the
// rollup is idempotent.
func TestRollupMatchesRaw(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	m := createHealthcheck(t, st, "web")
	for offset := -3; offset <= 0; offset++ {
		up := []int64{}
		for i := range 100 {
			up = append(up, int64(10+i*(4+offset)))
		}
		insertDay(t, st, m.ID, dayStart(offset), up, 3-offset)
	}
	raw := history(t, st, m.ID, 5)
	if raw[0].Ratio != nil || raw[0].Count != 0 {
		t.Fatalf("day without data: %s", summary(raw[0]))
	}
	if d := raw[1]; d.Count != 106 || d.Good != 100 || *d.P50MS != 10+49*1 || *d.P95MS != 10+94*1 || *d.MaxMS != 10+99*1 {
		t.Fatalf("raw day: %s", summary(d))
	}

	for range 2 {
		if err := st.Rollup(ctx, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM daily_rollups").Scan(&n); err != nil || n != 3 {
		t.Fatalf("%d rollup rows (%v), want the 3 past days", n, err)
	}
	sameDays(t, "after rollup", history(t, st, m.ID, 5), raw)

	// Sums over days are exact, from counts.
	u := history(t, st, m.ID, 5).Uptime()
	if u.Checks != 106+105+104+103 || *u.Ratio != 400.0/418 {
		t.Fatalf("uptime: %+v ratio %v", u, *u.Ratio)
	}
}

// Raw rows of days not rolled up yet are never pruned, and rolled-up days
// read the same once their raw rows are gone.
func TestPruneKeepsDaysNotRolledUp(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	m := createHealthcheck(t, st, "web")
	for offset := -5; offset <= -1; offset++ {
		insertDay(t, st, m.ID, dayStart(offset), []int64{100, 200, 300}, 1)
	}
	before := history(t, st, m.ID, 6)

	// Nothing rolled up: a retention of zero days deletes nothing.
	if err := st.Prune(ctx, time.Now(), time.Now().AddDate(0, 0, -400)); err != nil {
		t.Fatal(err)
	}
	sameDays(t, "prune before any rollup", history(t, st, m.ID, 6), before)

	// Rolled up through two days ago (as if the agent was stopped since).
	if err := st.Rollup(ctx, time.Now().AddDate(0, 0, -1)); err != nil {
		t.Fatal(err)
	}
	if err := st.Prune(ctx, time.Now(), time.Now().AddDate(0, 0, -400)); err != nil {
		t.Fatal(err)
	}
	var raw int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM results").Scan(&raw); err != nil || raw != 4 {
		t.Fatalf("%d raw rows left (%v), want yesterday's 4", raw, err)
	}
	sameDays(t, "after prune", history(t, st, m.ID, 6), before)

	// A late write to a day whose raw rows are gone doesn't roll it up again
	// from nothing.
	insertDay(t, st, m.ID, dayStart(-4).Add(12*time.Hour), nil, 1)
	sameDays(t, "late write to a pruned day", history(t, st, m.ID, 6), before)

	// Rollups past HISTORY_DAYS go.
	if err := st.Prune(ctx, time.Now(), dayStart(-3)); err != nil {
		t.Fatal(err)
	}
	h := history(t, st, m.ID, 6)
	if h[0].Count != 0 || h[1].Count != 0 || h[2].Count != 4 {
		t.Fatalf("after history pruning: %s / %s / %s", summary(h[1]), summary(h[2]), summary(h[3]))
	}
}

// A run recorded after its day was rolled up (a miss found once the grace
// period ran out after midnight, a late finish) updates that day.
func TestLateRunUpdatesRollup(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	m := createHeartbeat(t, st, "backup")
	yesterday := dayStart(-1)
	at := func(h int) *time.Time { v := yesterday.Add(time.Duration(h) * time.Hour); return &v }
	dur := int64(1500)
	for h := range 3 {
		if _, err := st.InsertRun(ctx, monitor.Run{MonitorID: m.ID, DueAt: at(h), StartedAt: at(h), FinishedAt: at(h),
			Outcome: monitor.OutcomeSuccess, OnTime: true, DurationMS: &dur}); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.Rollup(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	missed, err := st.InsertRun(ctx, monitor.Run{MonitorID: m.ID, DueAt: at(23), Outcome: monitor.OutcomeMissed})
	if err != nil {
		t.Fatal(err)
	}
	st1 := history(t, st, m.ID, 2)[:1].RunStats()
	if st1.Runs != 4 || st1.Missed != 1 || st1.OnTime != 3 || *st1.AvgDurationMS != 1500 {
		t.Fatalf("after a late miss: %+v", st1)
	}

	// It finishes after all, today: yesterday loses it, today has it.
	now := time.Now().UTC()
	missed.FinishedAt, missed.Outcome = &now, monitor.OutcomeSuccess
	if err := st.UpdateRun(ctx, missed); err != nil {
		t.Fatal(err)
	}
	h := history(t, st, m.ID, 2)
	if h[0].Count != 3 || h[0].Missed != 0 || h[1].Count != 1 || h[1].Good != 0 {
		t.Fatalf("after the late finish: %s / %s", summary(h[0]), summary(h[1]))
	}
}

// Checks inside a public maintenance window that covers the monitor don't
// count towards uptime; a private window, or one for another monitor,
// changes nothing. Saving or deleting a window updates rolled-up days.
func TestMaintenanceExcludedFromUptime(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	m := createHealthcheck(t, st, "web")
	other := createHealthcheck(t, st, "other")
	yesterday := dayStart(-1)
	insertDay(t, st, m.ID, yesterday, []int64{100, 100}, 0)  // 00:00, 00:01
	insertDay(t, st, m.ID, yesterday.Add(time.Hour), nil, 2) // 01:00, 01:01: down during the window
	insertDay(t, st, m.ID, yesterday.Add(2*time.Hour), []int64{100}, 0)
	if err := st.Rollup(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	ratio := func() float64 {
		t.Helper()
		r := history(t, st, m.ID, 2)[0].Ratio
		if r == nil {
			t.Fatal("no data")
		}
		return *r
	}
	if r := ratio(); r != 3.0/5 {
		t.Fatalf("before maintenance: %v", r)
	}

	w := maintenance.Window{Title: "db upgrade", StartsAt: yesterday.Add(time.Hour), EndsAt: yesterday.Add(time.Hour + 30*time.Minute),
		MonitorIDs: []int64{other.ID}, Public: true}
	w, err := st.CreateMaintenance(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	if r := ratio(); r != 3.0/5 {
		t.Fatalf("window for another monitor: %v", r)
	}
	w.MonitorIDs = []int64{m.ID}
	if w, err = st.UpdateMaintenance(ctx, w); err != nil {
		t.Fatal(err)
	}
	if r := ratio(); r != 1 {
		t.Fatalf("public window covering it: %v", r)
	}
	// Response times still count every check that was up.
	if d := history(t, st, m.ID, 2)[0]; d.Count != 3 || *d.MaxMS != 100 {
		t.Fatalf("day in maintenance: %s", summary(d))
	}
	w.Public = false
	if w, err = st.UpdateMaintenance(ctx, w); err != nil {
		t.Fatal(err)
	}
	if r := ratio(); r != 3.0/5 {
		t.Fatalf("private window: %v", r)
	}
	w.Public, w.AllMonitors = true, true
	if w, err = st.UpdateMaintenance(ctx, w); err != nil {
		t.Fatal(err)
	}
	if r := ratio(); r != 1 {
		t.Fatalf("public window for all monitors: %v", r)
	}
	// The last 24 hours, from raw, agree.
	if u, err := st.UptimeSince(ctx, m.ID, yesterday); err != nil || u.Checks != 3 || *u.Ratio != 1 {
		t.Fatalf("raw uptime: %+v %v", u, err)
	}
	if err := st.DeleteMaintenance(ctx, w.ID); err != nil {
		t.Fatal(err)
	}
	if r := ratio(); r != 3.0/5 {
		t.Fatalf("after deleting the window: %v", r)
	}
}

// The first rollup after an upgrade covers every day still in raw.
func TestRollupBackfillsOnUpgrade(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	m := createHealthcheck(t, st, "web")
	for _, offset := range []int{-29, -10, -1} {
		insertDay(t, st, m.ID, dayStart(offset), []int64{50}, 1)
	}
	if err := st.Rollup(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	through, err := st.rolledThrough(ctx)
	if err != nil || !through.Equal(dayStart(-1)) {
		t.Fatalf("rolled through %v (%v)", through, err)
	}
	var n int
	if err := st.db.QueryRow("SELECT COUNT(*) FROM daily_rollups WHERE monitor_id = ?", m.ID).Scan(&n); err != nil || n != 3 {
		t.Fatalf("%d rows (%v)", n, err)
	}
	if u := history(t, st, m.ID, 30).Uptime(); u.Checks != 6 || *u.Ratio != 0.5 {
		t.Fatalf("30 days: %+v", u)
	}
}
