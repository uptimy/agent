package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

// Uptimy Cloud sync (internal/cloudsync): which monitors are mirrored, and how
// far results and runs have been uploaded.

const (
	keySyncResultsCursor = "cloud_sync_results_cursor" // results.rowid
	keySyncRunsCursor    = "cloud_sync_runs_cursor"    // runs.id
	// Runs the runs cursor passed that weren't uploaded yet (JSON ids), e.g.
	// still running: a run that finishes is the same row, updated, so the
	// cursor alone would never see it again.
	keySyncRunsPending = "cloud_sync_runs_pending"
)

// CloudMonitor is a monitor mirrored to Cloud.
type CloudMonitor struct {
	MonitorID int64
	Kind      monitor.Kind
	ClientRef string
	CloudUUID string
	// Hash of what was last sent, so unchanged monitors aren't sent again.
	Hash string
}

// CloudMonitors returns every mirrored monitor by monitor id, including
// monitors deleted here whose mirror still has to be deleted.
func (s *Store) CloudMonitors(ctx context.Context) (map[int64]CloudMonitor, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT monitor_id, kind, client_ref, cloud_uuid, hash FROM cloud_monitors")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]CloudMonitor{}
	for rows.Next() {
		var c CloudMonitor
		if err := rows.Scan(&c.MonitorID, &c.Kind, &c.ClientRef, &c.CloudUUID, &c.Hash); err != nil {
			return nil, err
		}
		out[c.MonitorID] = c
	}
	return out, rows.Err()
}

// SaveCloudMonitor records a mirror after Cloud accepted it.
func (s *Store) SaveCloudMonitor(ctx context.Context, c CloudMonitor) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO cloud_monitors (monitor_id, kind, client_ref, cloud_uuid, hash)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(monitor_id) DO UPDATE SET
		kind = excluded.kind, client_ref = excluded.client_ref, cloud_uuid = excluded.cloud_uuid, hash = excluded.hash`,
		c.MonitorID, c.Kind, c.ClientRef, c.CloudUUID, c.Hash)
	return err
}

// DeleteCloudMonitor forgets a mirror after it was deleted in Cloud.
func (s *Store) DeleteCloudMonitor(ctx context.Context, monitorID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM cloud_monitors WHERE monitor_id = ?", monitorID)
	return err
}

// MarkCloudMonitorsStale makes the next reconcile send the mirrors with
// these Cloud uuids again, or every mirror without any (e.g. to a new
// location).
func (s *Store) MarkCloudMonitorsStale(ctx context.Context, cloudUUIDs ...string) error {
	if len(cloudUUIDs) == 0 {
		_, err := s.db.ExecContext(ctx, "UPDATE cloud_monitors SET hash = ''")
		return err
	}
	args := make([]any, len(cloudUUIDs))
	for i, u := range cloudUUIDs {
		args[i] = u
	}
	_, err := s.db.ExecContext(ctx, "UPDATE cloud_monitors SET hash = '' WHERE cloud_uuid IN (?"+strings.Repeat(", ?", len(cloudUUIDs)-1)+")", args...) //nolint:gosec // only placeholders are added
	return err
}

// ResetCloudSync forgets every mirror and both cursors, on disconnect: a
// later connection mirrors anew and uploads from when it was made.
func (s *Store) ResetCloudSync(ctx context.Context) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM cloud_monitors"); err != nil {
			return err
		}
		for _, key := range []string{keySyncResultsCursor, keySyncRunsCursor, keySyncRunsPending} {
			if err := deleteSetting(ctx, tx, key); err != nil {
				return err
			}
		}
		return nil
	})
}

// SyncCursors returns how far results and runs were uploaded. ok is false
// before the first upload.
func (s *Store) SyncCursors(ctx context.Context) (results, runs int64, ok bool, err error) {
	r, okR, err := s.getSetting(ctx, keySyncResultsCursor)
	if err != nil {
		return 0, 0, false, err
	}
	n, okN, err := s.getSetting(ctx, keySyncRunsCursor)
	if err != nil || !okR || !okN {
		return 0, 0, false, err
	}
	results, _ = strconv.ParseInt(r, 10, 64)
	runs, _ = strconv.ParseInt(n, 10, 64)
	return results, runs, true, nil
}

// SyncPendingRuns returns the runs at or before the runs cursor that
// weren't uploaded when it passed them.
func (s *Store) SyncPendingRuns(ctx context.Context) ([]int64, error) {
	var ids []int64
	_, err := s.getJSON(ctx, keySyncRunsPending, &ids)
	return ids, err
}

// SetSyncCursors records an accepted upload: the cursors, and the runs
// before the runs cursor not uploaded yet.
func (s *Store) SetSyncCursors(ctx context.Context, results, runs int64, pending []int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := setSetting(ctx, tx, keySyncResultsCursor, strconv.FormatInt(results, 10)); err != nil {
			return err
		}
		if err := setSetting(ctx, tx, keySyncRunsCursor, strconv.FormatInt(runs, 10)); err != nil {
			return err
		}
		if pending == nil {
			pending = []int64{}
		}
		return setJSON(ctx, tx, keySyncRunsPending, pending)
	})
}

// SyncStartCursors are where a new connection starts uploading: after the
// newest result and the newest change to a run. A run still running gets a
// newer seq when it finishes, so it's uploaded then.
func (s *Store) SyncStartCursors(ctx context.Context) (results, runs int64, err error) {
	if results, err = s.MaxResultRowID(ctx); err != nil {
		return 0, 0, err
	}
	runs, err = s.MaxRunSeq(ctx)
	return results, runs, err
}

// MaxRunSeq is the newest change to a run (migration 9), 0 without runs. A
// seq can come back once the newest runs are deleted, so the upload cursor
// is kept at most this.
func (s *Store) MaxRunSeq(ctx context.Context) (int64, error) {
	var seq int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(seq), 0) FROM runs").Scan(&seq)
	return seq, err
}

// MaxResultRowID is the newest result's rowid, 0 without results. Rowids
// are reused once the newest results are deleted (results has no
// AUTOINCREMENT, unlike runs), so the upload cursor is kept at most this.
func (s *Store) MaxResultRowID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(rowid), 0) FROM results").Scan(&id)
	return id, err
}

// ResultRow is a result with its position for the upload cursor.
type ResultRow struct {
	RowID int64
	monitor.Result
}

// ResultsAfter returns up to limit results after rowid, in insertion order.
func (s *Store) ResultsAfter(ctx context.Context, rowid int64, limit int) ([]ResultRow, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT rowid, monitor_id, ts, ok, latency_ms, message, status_code, error_class FROM results WHERE rowid > ? ORDER BY rowid LIMIT ?", rowid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ResultRow
	for rows.Next() {
		var r ResultRow
		var ts int64
		if err := rows.Scan(&r.RowID, &r.MonitorID, &ts, &r.OK, &r.LatencyMS, &r.Message, &r.StatusCode, &r.ErrorClass); err != nil {
			return nil, err
		}
		r.Time = fromMillis(ts)
		out = append(out, r)
	}
	return out, rows.Err()
}

const syncRunColumns = "id, monitor_id, due_at, started_at, finished_at, outcome, on_time, duration_ms, message, ts, seq"

// RunsChangedAfter returns up to limit runs written (inserted or updated)
// after seq, in the order they were written, the ones still running
// included: a run is the same row from its start to its finish.
func (s *Store) RunsChangedAfter(ctx context.Context, seq int64, limit int) ([]SyncRun, error) {
	return s.syncRuns(ctx, "SELECT "+syncRunColumns+" FROM runs WHERE seq > ? ORDER BY seq LIMIT ?", seq, limit)
}

// SyncRun is a run with its position for the upload cursor.
type SyncRun struct {
	Seq int64
	monitor.Run
}

// RunsByID returns the runs with these ids that still exist, in id order.
func (s *Store) RunsByID(ctx context.Context, ids []int64) ([]SyncRun, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return s.syncRuns(ctx, "SELECT "+syncRunColumns+" FROM runs WHERE id IN (?"+strings.Repeat(", ?", len(ids)-1)+") ORDER BY id", args...)
}

func (s *Store) syncRuns(ctx context.Context, query string, args ...any) ([]SyncRun, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncRun
	for rows.Next() {
		var r SyncRun
		var due, started, finished sql.NullInt64
		var ts int64
		if err := rows.Scan(&r.ID, &r.MonitorID, &due, &started, &finished, &r.Outcome, &r.OnTime, &r.DurationMS, &r.Message, &ts, &r.Seq); err != nil {
			return nil, err
		}
		r.DueAt, r.StartedAt, r.FinishedAt = optMillis(due), optMillis(started), optMillis(finished)
		if r.FinishedAt == nil && r.Outcome != monitor.OutcomeRunning {
			t := fromMillis(ts) // missed runs: when they were judged
			r.FinishedAt = &t
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// StatusAt is the monitor's status at t (its latest status change at or
// before t) and since when; ok is false before its first status change.
func (s *Store) StatusAt(ctx context.Context, monitorID int64, t time.Time) (status monitor.Status, since time.Time, ok bool, err error) {
	var ts int64
	err = s.db.QueryRowContext(ctx,
		"SELECT status, ts FROM events WHERE monitor_id = ? AND ts <= ? ORDER BY ts DESC, id DESC LIMIT 1",
		monitorID, t.UnixMilli()).Scan(&status, &ts)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	return status, fromMillis(ts), true, nil
}

func optMillis(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromMillis(v.Int64)
	return &t
}
