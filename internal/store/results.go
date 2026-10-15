package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

// InsertResult records a healthcheck result.
func (s *Store) InsertResult(ctx context.Context, r monitor.Result) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO results (monitor_id, ts, ok, latency_ms, message) VALUES (?, ?, ?, ?, ?)",
		r.MonitorID, r.Time.UnixMilli(), r.OK, r.LatencyMS, r.Message)
	return err
}

// RecentResults returns the newest limit results, oldest first.
func (s *Store) RecentResults(ctx context.Context, monitorID int64, limit int) ([]monitor.Result, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT monitor_id, ts, ok, latency_ms, message FROM (
		SELECT * FROM results WHERE monitor_id = ? ORDER BY ts DESC LIMIT ?
	) ORDER BY ts`, monitorID, limit)
	if err != nil {
		return nil, err
	}
	return scanResults(rows)
}

// ResultsSince returns results newer than since, oldest first.
func (s *Store) ResultsSince(ctx context.Context, monitorID int64, since time.Time) ([]monitor.Result, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT monitor_id, ts, ok, latency_ms, message FROM results WHERE monitor_id = ? AND ts >= ? ORDER BY ts",
		monitorID, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	return scanResults(rows)
}

func scanResults(rows *sql.Rows) ([]monitor.Result, error) {
	defer rows.Close()
	out := []monitor.Result{}
	for rows.Next() {
		var r monitor.Result
		var ts int64
		if err := rows.Scan(&r.MonitorID, &ts, &r.OK, &r.LatencyMS, &r.Message); err != nil {
			return nil, err
		}
		r.Time = fromMillis(ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Uptime summarizes results newer than since.
type Uptime struct {
	Checks       int      `json:"checks"`
	Ratio        *float64 `json:"ratio"` // nil when there are no checks
	AvgLatencyMS *float64 `json:"avg_latency_ms"`
}

// UptimeSince computes the uptime ratio and average latency since a time.
func (s *Store) UptimeSince(ctx context.Context, monitorID int64, since time.Time) (Uptime, error) {
	var u Uptime
	err := s.db.QueryRowContext(ctx,
		"SELECT COUNT(*), AVG(ok), AVG(CASE WHEN ok THEN latency_ms END) FROM results WHERE monitor_id = ? AND ts >= ?",
		monitorID, since.UnixMilli()).Scan(&u.Checks, &u.Ratio, &u.AvgLatencyMS)
	return u, err
}

// Daily is one UTC day of history: how many checks (or runs) there were and
// the share that went well (up, or on time).
type Daily struct {
	Date  string   `json:"date"` // YYYY-MM-DD
	Count int      `json:"count"`
	Ratio *float64 `json:"ratio"` // nil when there's no data
}

// DailyUptimes returns a healthcheck's uptime per day for the last days days,
// including days without data, oldest first.
func (s *Store) DailyUptimes(ctx context.Context, monitorID int64, days int) ([]Daily, error) {
	return s.daily(ctx, days, `SELECT date(ts / 1000, 'unixepoch') AS d, COUNT(*), AVG(ok)
		FROM results WHERE monitor_id = ? AND ts >= ? GROUP BY d`, monitorID)
}

// daily runs query (which must select date, count, ratio for monitorID and a
// start time) and fills in the days without data.
func (s *Store) daily(ctx context.Context, days int, query string, monitorID int64) ([]Daily, error) {
	start := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -(days - 1))
	rows, err := s.db.QueryContext(ctx, query, monitorID, start.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byDate := map[string]Daily{}
	for rows.Next() {
		var d Daily
		if err := rows.Scan(&d.Date, &d.Count, &d.Ratio); err != nil {
			return nil, err
		}
		byDate[d.Date] = d
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Daily, 0, days)
	for i := 0; i < days; i++ {
		date := start.AddDate(0, 0, i).Format("2006-01-02")
		d, ok := byDate[date]
		if !ok {
			d = Daily{Date: date}
		}
		out = append(out, d)
	}
	return out, nil
}

// InsertEvent records a status transition.
func (s *Store) InsertEvent(ctx context.Context, e monitor.Event) (monitor.Event, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		e.ID, err = insertEvent(ctx, tx, e)
		return err
	})
	return e, err
}

// insertEvent records a status transition. Acknowledgements and reminders
// are about one outage, so any change ends them: a reminder still waiting
// to go out would now be wrong.
func insertEvent(ctx context.Context, tx *sql.Tx, e monitor.Event) (int64, error) {
	res, err := tx.ExecContext(ctx, "INSERT INTO events (monitor_id, ts, status, message) VALUES (?, ?, ?, ?)",
		e.MonitorID, e.Time.UnixMilli(), e.Status, e.Message)
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM acknowledgements WHERE monitor_id = ?", e.MonitorID); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE alert_deliveries SET state = 'suppressed', detail = ?, done_at = ?
		WHERE state = 'pending' AND alert_id IN
			(SELECT id FROM alerts WHERE monitor_id = ? AND type IN ('reminder', 'acknowledged'))`,
		"its status changed first", e.Time.UnixMilli(), e.MonitorID); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListEvents returns the newest events, newest first. monitorID 0 means all monitors.
func (s *Store) ListEvents(ctx context.Context, monitorID int64, limit int) ([]monitor.Event, error) {
	q := "SELECT id, monitor_id, ts, status, message FROM events"
	args := []any{}
	if monitorID != 0 {
		q += " WHERE monitor_id = ?"
		args = append(args, monitorID)
	}
	q += " ORDER BY ts DESC, id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []monitor.Event{}
	for rows.Next() {
		var e monitor.Event
		var ts int64
		if err := rows.Scan(&e.ID, &e.MonitorID, &ts, &e.Status, &e.Message); err != nil {
			return nil, err
		}
		e.Time = fromMillis(ts)
		out = append(out, e)
	}
	return out, rows.Err()
}

// LastEvent returns the newest event for a monitor, or ErrNotFound.
func (s *Store) LastEvent(ctx context.Context, monitorID int64) (monitor.Event, error) {
	events, err := s.ListEvents(ctx, monitorID, 1)
	if err != nil {
		return monitor.Event{}, err
	}
	if len(events) == 0 {
		return monitor.Event{}, ErrNotFound
	}
	return events[0], nil
}

// IsNotFound reports whether err is ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
