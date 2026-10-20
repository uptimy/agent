package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"
)

// History. Raw results and runs are kept RETENTION_DAYS. Each completed UTC
// day is also rolled up into one daily_rollups row per monitor, kept
// HISTORY_DAYS, so windows longer than the raw retention stay exact. A row
// holds counts, not ratios, so any range of days sums exactly.
//
// The rollups_through setting is the newest day rolled up. Readers take
// days up to it from rollups and later days (today, and yesterday until the
// rollup has run) from raw. Prune never deletes raw rows after it, so every
// day is in one place or the other.
//
// Checks and runs inside a public maintenance window that covers the
// monitor don't count towards uptime: the status page shows that time as
// maintenance. They still count towards response times.

const (
	keyRollupsThrough = "rollups_through"
	// keyRawFrom is where raw history starts since the last prune: a day
	// that starts before it can't be rolled up again.
	keyRawFrom = "raw_from"
	dateLayout = "2006-01-02"
	day        = 24 * time.Hour
)

// inPublicMaintenance is true for a results or runs row (alias x) inside a
// public maintenance window that covers its monitor.
const inPublicMaintenance = `EXISTS (SELECT 1 FROM maintenance w WHERE w.public AND w.starts_at <= x.ts AND x.ts < w.ends_at
	AND (w.all_monitors OR EXISTS (SELECT 1 FROM maintenance_monitors mm WHERE mm.maintenance_id = w.id AND mm.monitor_id = x.monitor_id)))`

// Daily is one UTC day of a monitor's history. For a healthcheck, Count is
// its checks, Good the ones that were up, and the latencies are of those
// that were up. For a heartbeat, Count is the runs that were due, Good the
// ones that succeeded on time, and the latencies are run durations.
type Daily struct {
	Date  string   `json:"date"` // YYYY-MM-DD
	Count int      `json:"count"`
	Ratio *float64 `json:"ratio"` // Good / Count; nil when there's no data

	Good   int `json:"-"`
	Failed int `json:"-"` // heartbeats
	Missed int `json:"-"` // heartbeats

	AvgMS *float64 `json:"avg_ms,omitempty"`
	P50MS *int64   `json:"p50_ms,omitempty"`
	P95MS *int64   `json:"p95_ms,omitempty"`
	P99MS *int64   `json:"p99_ms,omitempty"`
	MaxMS *int64   `json:"max_ms,omitempty"`

	latencySum, latencyN int64
}

func (d *Daily) finish() {
	if d.Count > 0 {
		r := float64(d.Good) / float64(d.Count)
		d.Ratio = &r
	}
	if d.latencyN > 0 {
		avg := float64(d.latencySum) / float64(d.latencyN)
		d.AvgMS = &avg
	}
}

// Days is a run of days, oldest first.
type Days []Daily

// Last returns the newest n days.
func (ds Days) Last(n int) Days {
	if n >= len(ds) {
		return ds
	}
	return ds[len(ds)-n:]
}

// Uptime sums a healthcheck's days.
func (ds Days) Uptime() Uptime {
	t := ds.total()
	return Uptime{Checks: t.Count, Ratio: t.Ratio, AvgLatencyMS: t.AvgMS}
}

// RunStats sums a heartbeat's days.
func (ds Days) RunStats() RunStats {
	t := ds.total()
	return RunStats{Runs: t.Count, OnTime: t.Good, Missed: t.Missed, Failed: t.Failed, Ratio: t.Ratio, AvgDurationMS: t.AvgMS}
}

func (ds Days) total() Daily {
	var t Daily
	for _, d := range ds {
		t.Count += d.Count
		t.Good += d.Good
		t.Failed += d.Failed
		t.Missed += d.Missed
		t.latencySum += d.latencySum
		t.latencyN += d.latencyN
	}
	t.finish()
	return t
}

// History returns a monitor's last days UTC days, today included, oldest
// first, with an entry for every day.
func (s *Store) History(ctx context.Context, monitorID int64, days int) (Days, error) {
	today := time.Now().UTC().Truncate(day)
	start := today.AddDate(0, 0, -(days - 1))
	through, err := s.rolledThrough(ctx)
	if err != nil {
		return nil, err
	}
	byDate := map[string]Daily{}
	// Days up to through come from rollups, later ones from raw.
	rawFrom := start
	if !through.IsZero() && !through.Before(start) {
		rolled, err := s.rollups(ctx, monitorID, start.Format(dateLayout), through.Format(dateLayout))
		if err != nil {
			return nil, err
		}
		for _, d := range rolled {
			byDate[d.Date] = d
		}
		rawFrom = through.Add(day)
	}
	if !rawFrom.After(today) {
		raw, err := s.aggregate(ctx, monitorID, rawFrom, today.Add(day))
		if err != nil {
			return nil, err
		}
		for date, d := range raw {
			byDate[date] = *d
		}
	}
	out := make(Days, 0, days)
	for i := range days {
		date := start.AddDate(0, 0, i).Format(dateLayout)
		d, ok := byDate[date]
		if !ok {
			d = Daily{Date: date}
		}
		out = append(out, d)
	}
	return out, nil
}

func (s *Store) rollups(ctx context.Context, monitorID int64, from, to string) ([]Daily, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT date, count, good, failed, missed, latency_sum, latency_n, p50, p95, p99, max
		FROM daily_rollups WHERE monitor_id = ? AND date >= ? AND date <= ?`, monitorID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Daily
	for rows.Next() {
		var (
			d                  Daily
			p50, p95, p99, max sql.NullInt64
		)
		if err := rows.Scan(&d.Date, &d.Count, &d.Good, &d.Failed, &d.Missed, &d.latencySum, &d.latencyN, &p50, &p95, &p99, &max); err != nil {
			return nil, err
		}
		d.P50MS, d.P95MS, d.P99MS, d.MaxMS = optInt(p50), optInt(p95), optInt(p99), optInt(max)
		d.finish()
		out = append(out, d)
	}
	return out, rows.Err()
}

func optInt(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// aggregate computes a monitor's days in [from, to) from raw results and
// runs. Days without data are left out.
func (s *Store) aggregate(ctx context.Context, monitorID int64, from, to time.Time) (map[string]*Daily, error) {
	out := map[string]*Daily{}
	get := func(date string) *Daily {
		d, ok := out[date]
		if !ok {
			d = &Daily{Date: date}
			out[date] = d
		}
		return d
	}
	args := []any{monitorID, from.UnixMilli(), to.UnixMilli()}
	const dateOf = "date(x.ts / 1000, 'unixepoch')"

	// Healthchecks: uptime, then the latencies of checks that were up.
	rows, err := s.db.QueryContext(ctx, `SELECT `+dateOf+` AS d, COUNT(*), COALESCE(SUM(x.ok), 0)
		FROM results x WHERE x.monitor_id = ? AND x.ts >= ? AND x.ts < ? AND NOT `+inPublicMaintenance+` GROUP BY d`, args...)
	if err != nil {
		return nil, err
	}
	err = scanEach(rows, func(r *sql.Rows) error {
		var date string
		var n, good int
		if err := r.Scan(&date, &n, &good); err != nil {
			return err
		}
		d := get(date)
		d.Count, d.Good = n, good
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.latencies(ctx, get, `SELECT `+dateOf+`, x.latency_ms
		FROM results x WHERE x.monitor_id = ? AND x.ts >= ? AND x.ts < ? AND x.ok ORDER BY x.ts`, args); err != nil {
		return nil, err
	}

	// Heartbeats: runs that were due, then their durations.
	rows, err = s.db.QueryContext(ctx, `SELECT `+dateOf+` AS d, COUNT(*),
			COALESCE(SUM(x.outcome = 'success' AND x.on_time), 0),
			COALESCE(SUM(x.outcome = 'failure'), 0),
			COALESCE(SUM(x.outcome = 'missed'), 0)
		FROM runs x WHERE x.monitor_id = ? AND x.ts >= ? AND x.ts < ? AND x.outcome != 'running'
			AND NOT `+inPublicMaintenance+` GROUP BY d`, args...)
	if err != nil {
		return nil, err
	}
	err = scanEach(rows, func(r *sql.Rows) error {
		var date string
		var n, good, failed, missed int
		if err := r.Scan(&date, &n, &good, &failed, &missed); err != nil {
			return err
		}
		d := get(date)
		d.Count, d.Good, d.Failed, d.Missed = n, good, failed, missed
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err := s.latencies(ctx, get, `SELECT `+dateOf+`, x.duration_ms
		FROM runs x WHERE x.monitor_id = ? AND x.ts >= ? AND x.ts < ? AND x.outcome != 'running' AND x.duration_ms IS NOT NULL ORDER BY x.ts`, args); err != nil {
		return nil, err
	}

	for _, d := range out {
		d.finish()
	}
	return out, nil
}

// latencies runs query (date, milliseconds) and fills in each day's
// average, percentiles and maximum.
func (s *Store) latencies(ctx context.Context, get func(string) *Daily, query string, args []any) error {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	byDate := map[string][]int64{}
	err = scanEach(rows, func(r *sql.Rows) error {
		var date string
		var ms int64
		if err := r.Scan(&date, &ms); err != nil {
			return err
		}
		byDate[date] = append(byDate[date], ms)
		return nil
	})
	if err != nil {
		return err
	}
	for date, ms := range byDate {
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		d := get(date)
		for _, v := range ms {
			d.latencySum += v
		}
		d.latencyN = int64(len(ms))
		d.P50MS, d.P95MS, d.P99MS = percentile(ms, 0.50), percentile(ms, 0.95), percentile(ms, 0.99)
		d.MaxMS = &ms[len(ms)-1]
	}
	return nil
}

// percentile is the nearest-rank percentile of sorted values.
func percentile(sorted []int64, p float64) *int64 {
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	v := sorted[max(i, 0)]
	return &v
}

func scanEach(rows *sql.Rows, fn func(*sql.Rows) error) error {
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Rollup rolls up every completed day that isn't yet, for every monitor:
// after a restart or an upgrade, all the days still in raw. It's idempotent.
func (s *Store) Rollup(ctx context.Context, now time.Time) error {
	s.rollupMu.Lock()
	defer s.rollupMu.Unlock()
	yesterday := now.UTC().Truncate(day).Add(-day)
	through, err := s.rolledThrough(ctx)
	if err != nil {
		return err
	}
	var from time.Time
	if through.IsZero() {
		// First run: from the oldest raw day.
		var oldest sql.NullInt64
		if err := s.db.QueryRowContext(ctx,
			"SELECT MIN(ts) FROM (SELECT MIN(ts) AS ts FROM results UNION ALL SELECT MIN(ts) FROM runs)").Scan(&oldest); err != nil {
			return err
		}
		from = yesterday
		if oldest.Valid {
			from = earlier(fromMillis(oldest.Int64).Truncate(day), yesterday)
		}
	} else {
		from = through.Add(day)
	}
	if from.After(yesterday) {
		return nil
	}
	ids, err := s.monitorIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.reroll(ctx, id, from, yesterday); err != nil {
			return err
		}
	}
	return setSetting(ctx, s.db, keyRollupsThrough, yesterday.Format(dateLayout))
}

// reroll rolls up a monitor's days from first to last (both included) from
// raw again, replacing their rows.
func (s *Store) reroll(ctx context.Context, monitorID int64, first, last time.Time) error {
	days, err := s.aggregate(ctx, monitorID, first, last.Add(day))
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM daily_rollups WHERE monitor_id = ? AND date >= ? AND date <= ?",
			monitorID, first.Format(dateLayout), last.Format(dateLayout)); err != nil {
			return err
		}
		for _, d := range days {
			if _, err := tx.ExecContext(ctx, `INSERT INTO daily_rollups
				(monitor_id, date, count, good, failed, missed, latency_sum, latency_n, p50, p95, p99, max)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				monitorID, d.Date, d.Count, d.Good, d.Failed, d.Missed, d.latencySum, d.latencyN,
				d.P50MS, d.P95MS, d.P99MS, d.MaxMS); err != nil {
				return err
			}
		}
		return nil
	})
}

// changed re-rolls the days from first to last that are already rolled up
// and still in raw, after raw history in them changed: a run recorded late,
// or a maintenance window that moved. monitorID 0 is every monitor.
func (s *Store) changed(ctx context.Context, monitorID int64, first, last time.Time) error {
	s.rollupMu.Lock()
	defer s.rollupMu.Unlock()
	through, err := s.rolledThrough(ctx)
	if err != nil || through.IsZero() {
		return err
	}
	rawFrom, err := s.rawFrom(ctx)
	if err != nil {
		return err
	}
	first = first.UTC().Truncate(day)
	if first.Before(rawFrom) {
		// Part of that day was pruned: rolling it up again would lose it.
		first = rawFrom.Truncate(day)
		if first.Before(rawFrom) {
			first = first.Add(day)
		}
	}
	last = earlier(last.UTC().Truncate(day), through)
	if first.After(last) {
		return nil
	}
	ids := []int64{monitorID}
	if monitorID == 0 {
		if ids, err = s.monitorIDs(ctx); err != nil {
			return err
		}
	}
	for _, id := range ids {
		if err := s.reroll(ctx, id, first, last); err != nil {
			return err
		}
	}
	return nil
}

// changedAt is changed for one moment. Today is never rolled up, so the
// usual write, about now, costs nothing.
func (s *Store) changedAt(ctx context.Context, monitorID int64, t time.Time) error {
	if !t.Before(time.Now().UTC().Truncate(day)) {
		return nil
	}
	return s.changed(ctx, monitorID, t, t)
}

func (s *Store) monitorIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id FROM monitors ORDER BY id")
	if err != nil {
		return nil, err
	}
	var ids []int64
	err = scanEach(rows, func(r *sql.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
		return nil
	})
	return ids, err
}

// rolledThrough is the newest day rolled up; zero before the first rollup.
func (s *Store) rolledThrough(ctx context.Context) (time.Time, error) {
	return s.dateSetting(ctx, keyRollupsThrough)
}

// rawFrom is where raw history starts; zero when nothing was pruned.
func (s *Store) rawFrom(ctx context.Context) (time.Time, error) {
	v, ok, err := s.getSetting(ctx, keyRawFrom)
	if err != nil || !ok {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, v)
}

func (s *Store) dateSetting(ctx context.Context, key string) (time.Time, error) {
	v, ok, err := s.getSetting(ctx, key)
	if err != nil || !ok {
		return time.Time{}, err
	}
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		return time.Time{}, errors.New("invalid " + key + " setting: " + v)
	}
	return t, nil
}

func earlier(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
