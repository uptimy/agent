package store

import (
	"context"
	"database/sql"
	"errors"
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

// nextSeq is the seq of a run being written: above every other run's, so
// the Cloud sync's cursor sees each change once (migration 9).
const nextSeq = "(SELECT COALESCE(MAX(seq), 0) + 1 FROM runs)"

// InsertRun records a heartbeat run and returns it with its ID.
func (s *Store) InsertRun(ctx context.Context, r monitor.Run) (monitor.Run, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO runs
		(monitor_id, ts, due_at, started_at, finished_at, outcome, on_time, duration_ms, message, seq)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, `+nextSeq+`)`,
		r.MonitorID, runTime(r).UnixMilli(), millis(r.DueAt), millis(r.StartedAt), millis(r.FinishedAt),
		r.Outcome, r.OnTime, r.DurationMS, r.Message)
	if err != nil {
		return r, err
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return r, err
	}
	// A run missed before midnight is recorded after it, once its grace is up.
	return r, s.changedAt(ctx, r.MonitorID, runTime(r))
}

// UpdateRun saves a run, e.g. one that was running and has now finished.
func (s *Store) UpdateRun(ctx context.Context, r monitor.Run) error {
	before, err := s.runTS(ctx, r.ID)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE runs SET ts = ?, due_at = ?, started_at = ?, finished_at = ?,
		outcome = ?, on_time = ?, duration_ms = ?, message = ?, seq = `+nextSeq+` WHERE id = ?`,
		runTime(r).UnixMilli(), millis(r.DueAt), millis(r.StartedAt), millis(r.FinishedAt),
		r.Outcome, r.OnTime, r.DurationMS, r.Message, r.ID)
	if err != nil {
		return err
	}
	// A late finish can move a run to another day.
	if err := s.changedAt(ctx, r.MonitorID, before); err != nil {
		return err
	}
	return s.changedAt(ctx, r.MonitorID, runTime(r))
}

// DeleteRun removes a run, e.g. a start folded into the run it belonged to.
func (s *Store) DeleteRun(ctx context.Context, id int64) error {
	var monitorID, ts int64
	err := s.db.QueryRowContext(ctx, "SELECT monitor_id, ts FROM runs WHERE id = ?", id).Scan(&monitorID, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM runs WHERE id = ?", id); err != nil {
		return err
	}
	return s.changedAt(ctx, monitorID, fromMillis(ts))
}

// runTS is when a saved run happened (its ts), or zero if it doesn't exist.
func (s *Store) runTS(ctx context.Context, id int64) (time.Time, error) {
	var ts int64
	err := s.db.QueryRowContext(ctx, "SELECT ts FROM runs WHERE id = ?", id).Scan(&ts)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return fromMillis(ts), err
}

// AbandonRuns closes the heartbeat's runs that started and never finished,
// all but keepID (0 for none): they count as missed, with message. A run
// left running would show as running for good.
func (s *Store) AbandonRuns(ctx context.Context, monitorID, keepID int64, message string) error {
	var times []time.Time
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT id, ts FROM runs WHERE monitor_id = ? AND outcome = ? AND id != ? ORDER BY id",
			monitorID, monitor.OutcomeRunning, keepID)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id, ts int64
			if err := rows.Scan(&id, &ts); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
			times = append(times, fromMillis(ts))
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		// One at a time: each closed run gets a seq of its own.
		for _, id := range ids {
			if _, err := tx.ExecContext(ctx, "UPDATE runs SET outcome = ?, message = ?, seq = "+nextSeq+" WHERE id = ?",
				monitor.OutcomeMissed, message, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, t := range times {
		if err := s.changedAt(ctx, monitorID, t); err != nil {
			return err
		}
	}
	return nil
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

// RunStatsSince summarizes the heartbeat's runs since a time, from raw runs:
// for the last 24 hours. Longer windows use History.
func (s *Store) RunStatsSince(ctx context.Context, monitorID int64, since time.Time) (RunStats, error) {
	var st RunStats
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FILTER (WHERE NOT maint),
			COALESCE(SUM(outcome = 'success' AND on_time) FILTER (WHERE NOT maint), 0),
			COALESCE(SUM(outcome = 'missed') FILTER (WHERE NOT maint), 0),
			COALESCE(SUM(outcome = 'failure') FILTER (WHERE NOT maint), 0),
			AVG(outcome = 'success' AND on_time) FILTER (WHERE NOT maint),
			AVG(duration_ms)
		FROM (SELECT x.outcome, x.on_time, x.duration_ms, `+inPublicMaintenance+` AS maint
			FROM runs x WHERE x.monitor_id = ? AND x.ts >= ? AND x.outcome != 'running')`,
		monitorID, since.UnixMilli()).Scan(&st.Runs, &st.OnTime, &st.Missed, &st.Failed, &st.Ratio, &st.AvgDurationMS)
	return st, err
}
