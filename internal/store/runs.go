package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

const runColumns = "id, monitor_id, due_at, started_at, finished_at, outcome, on_time, duration_ms, message"

// runTime is when a run happened, for ordering and time windows: when it
// finished, or started (still running), or was due (missed).
func runTime(r monitor.Run) time.Time {
	for _, t := range []*time.Time{r.FinishedAt, r.StartedAt, r.DueAt} {
		if t != nil {
			return *t
		}
	}
	return time.Now().UTC()
}

func millis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return t.UnixMilli()
}

// InsertRun records a heartbeat run and returns it with its ID.
func (s *Store) InsertRun(ctx context.Context, r monitor.Run) (monitor.Run, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO runs
		(monitor_id, ts, due_at, started_at, finished_at, outcome, on_time, duration_ms, message)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.MonitorID, runTime(r).UnixMilli(), millis(r.DueAt), millis(r.StartedAt), millis(r.FinishedAt),
		r.Outcome, r.OnTime, r.DurationMS, r.Message)
	if err != nil {
		return r, err
	}
	r.ID, err = res.LastInsertId()
	return r, err
}

// UpdateRun saves a run, e.g. one that was running and has now finished.
func (s *Store) UpdateRun(ctx context.Context, r monitor.Run) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET ts = ?, due_at = ?, started_at = ?, finished_at = ?,
		outcome = ?, on_time = ?, duration_ms = ?, message = ? WHERE id = ?`,
		runTime(r).UnixMilli(), millis(r.DueAt), millis(r.StartedAt), millis(r.FinishedAt),
		r.Outcome, r.OnTime, r.DurationMS, r.Message, r.ID)
	return err
}

// DeleteRun removes a run, e.g. a start folded into the run it belonged to.
func (s *Store) DeleteRun(ctx context.Context, id int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM runs WHERE id = ?", id)
	return err
}

// AbandonRuns closes the heartbeat's runs that started and never finished,
// all but keepID (0 for none): they count as missed, with message. A run
// left running would show as running for good.
func (s *Store) AbandonRuns(ctx context.Context, monitorID, keepID int64, message string) error {
	_, err := s.db.ExecContext(ctx, "UPDATE runs SET outcome = ?, message = ? WHERE monitor_id = ? AND outcome = ? AND id != ?",
		monitor.OutcomeMissed, message, monitorID, monitor.OutcomeRunning, keepID)
	return err
}

// OpenRun returns the heartbeat's run that started and hasn't finished, or
// ErrNotFound.
func (s *Store) OpenRun(ctx context.Context, monitorID int64) (monitor.Run, error) {
	return s.oneRun(ctx, "SELECT "+runColumns+" FROM runs WHERE monitor_id = ? AND outcome = ? ORDER BY ts DESC LIMIT 1",
		monitorID, monitor.OutcomeRunning)
}

// LastRun returns the heartbeat's newest finished or missed run, or ErrNotFound.
func (s *Store) LastRun(ctx context.Context, monitorID int64) (monitor.Run, error) {
	return s.oneRun(ctx, "SELECT "+runColumns+" FROM runs WHERE monitor_id = ? AND outcome != ? ORDER BY ts DESC, id DESC LIMIT 1",
		monitorID, monitor.OutcomeRunning)
}

func (s *Store) oneRun(ctx context.Context, query string, args ...any) (monitor.Run, error) {
	runs, err := s.queryRuns(ctx, query, args...)
	if err != nil {
		return monitor.Run{}, err
	}
	if len(runs) == 0 {
		return monitor.Run{}, ErrNotFound
	}
	return runs[0], nil
}

// RecentRuns returns the heartbeat's newest limit runs, oldest first.
func (s *Store) RecentRuns(ctx context.Context, monitorID int64, limit int) ([]monitor.Run, error) {
	return s.queryRuns(ctx, `SELECT `+runColumns+` FROM (
		SELECT * FROM runs WHERE monitor_id = ? ORDER BY ts DESC, id DESC LIMIT ?
	) ORDER BY ts, id`, monitorID, limit)
}

func (s *Store) queryRuns(ctx context.Context, query string, args ...any) ([]monitor.Run, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []monitor.Run{}
	for rows.Next() {
		var (
			r                      monitor.Run
			due, started, finished sql.NullInt64
			duration               sql.NullInt64
		)
		if err := rows.Scan(&r.ID, &r.MonitorID, &due, &started, &finished, &r.Outcome, &r.OnTime, &duration, &r.Message); err != nil {
			return nil, err
		}
		r.DueAt, r.StartedAt, r.FinishedAt = optTime(due), optTime(started), optTime(finished)
		if duration.Valid {
			r.DurationMS = &duration.Int64
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func optTime(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMillis(v.Int64)
	return &t
}

// RunStats summarizes a heartbeat's runs over a period.
type RunStats struct {
	Runs   int `json:"runs"` // runs that were due: on time, late, failed or missed
	OnTime int `json:"on_time"`
	Missed int `json:"missed"`
	Failed int `json:"failed"`
	// Ratio is the share of runs that succeeded on time; nil without runs.
	Ratio         *float64 `json:"ratio"`
	AvgDurationMS *float64 `json:"avg_duration_ms"` // runs that pinged /start
}

// RunStatsSince summarizes the heartbeat's runs since a time.
func (s *Store) RunStatsSince(ctx context.Context, monitorID int64, since time.Time) (RunStats, error) {
	var st RunStats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*),
			COALESCE(SUM(outcome = 'success' AND on_time), 0),
			COALESCE(SUM(outcome = 'missed'), 0),
			COALESCE(SUM(outcome = 'failure'), 0),
			AVG(outcome = 'success' AND on_time),
			AVG(duration_ms)
		FROM runs WHERE monitor_id = ? AND ts >= ? AND outcome != 'running'`,
		monitorID, since.UnixMilli()).Scan(&st.Runs, &st.OnTime, &st.Missed, &st.Failed, &st.Ratio, &st.AvgDurationMS)
	return st, err
}

// DailyOnTime returns the share of a heartbeat's runs that succeeded on time,
// per day for the last days days, oldest first.
func (s *Store) DailyOnTime(ctx context.Context, monitorID int64, days int) ([]Daily, error) {
	return s.daily(ctx, days, `SELECT date(ts / 1000, 'unixepoch') AS d, COUNT(*), AVG(outcome = 'success' AND on_time)
		FROM runs WHERE monitor_id = ? AND ts >= ? AND outcome != 'running' GROUP BY d`, monitorID)
}
