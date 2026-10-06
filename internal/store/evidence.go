package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/uptimy/agent/internal/incident"
	"github.com/uptimy/agent/internal/monitor"
)

func validateEvidencePage(limit, offset int) error {
	if limit < 1 || limit > 501 {
		return errors.New("evidence limit must be between 1 and 501")
	}
	if offset < 0 || offset > 10000 {
		return errors.New("evidence offset must be between 0 and 10000")
	}
	return nil
}

func evidenceTimeBound(t time.Time) int64 {
	ms := t.UnixMilli()
	if t.Nanosecond()%int(time.Millisecond) != 0 {
		ms++
	}
	return ms
}

// MonitorPage returns at most limit monitors after afterID, ordered by ID.
func (s *Store) MonitorPage(ctx context.Context, afterID int64, limit int) ([]monitor.Monitor, error) {
	if err := validateEvidencePage(limit, 0); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, selectMonitors+" WHERE m.id > ? ORDER BY m.id ASC LIMIT ?", afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []monitor.Monitor{}
	for rows.Next() {
		m, err := scanMonitor(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ResultsWindow returns a page in [from, to), oldest first with stable ties.
func (s *Store) ResultsWindow(ctx context.Context, id int64, from, to time.Time, limit, offset int) ([]monitor.Result, error) {
	if err := validateEvidencePage(limit, offset); err != nil {
		return nil, err
	}
	// Results have no explicit ID; rowid is their insertion-order tie breaker.
	rows, err := s.db.QueryContext(ctx, `SELECT monitor_id, ts, ok, latency_ms, message FROM results
		WHERE monitor_id = ? AND ts >= ? AND ts < ? ORDER BY ts ASC, rowid ASC LIMIT ? OFFSET ?`,
		id, evidenceTimeBound(from), evidenceTimeBound(to), limit, offset)
	if err != nil {
		return nil, err
	}
	return scanResults(rows)
}

// RunsWindow returns a page in [from, to), ordered by timestamp and ID.
func (s *Store) RunsWindow(ctx context.Context, id int64, from, to time.Time, limit, offset int) ([]monitor.Run, error) {
	if err := validateEvidencePage(limit, offset); err != nil {
		return nil, err
	}
	return s.queryRuns(ctx, "SELECT "+runColumns+` FROM runs
		WHERE monitor_id = ? AND ts >= ? AND ts < ? ORDER BY ts ASC, id ASC LIMIT ? OFFSET ?`,
		id, evidenceTimeBound(from), evidenceTimeBound(to), limit, offset)
}

// EventsWindow returns a page in [from, to), ordered by timestamp and ID.
func (s *Store) EventsWindow(ctx context.Context, id int64, from, to time.Time, limit, offset int) ([]monitor.Event, error) {
	if err := validateEvidencePage(limit, offset); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, monitor_id, ts, status, message FROM events
		WHERE monitor_id = ? AND ts >= ? AND ts < ? ORDER BY ts ASC, id ASC LIMIT ? OFFSET ?`,
		id, evidenceTimeBound(from), evidenceTimeBound(to), limit, offset)
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

const selectIncidentEvidence = `SELECT i.id, i.title, i.severity, i.created_at, i.resolved_at,
	COALESCE((SELECT u.status FROM incident_updates u WHERE u.incident_id = i.id
		ORDER BY u.created_at DESC, u.id DESC LIMIT 1), '') FROM incidents i`

func scanIncidentEvidence(row scanner) (incident.Incident, error) {
	var in incident.Incident
	var created int64
	var resolved sql.NullInt64
	err := row.Scan(&in.ID, &in.Title, &in.Severity, &created, &resolved, &in.Status)
	if err != nil {
		return in, err
	}
	in.CreatedAt, in.ResolvedAt = fromMillis(created), optTime(resolved)
	in.MonitorIDs, in.Updates = []int64{}, []incident.Update{}
	return in, nil
}

// ListActiveIncidentPage returns open incident summaries, without timelines or links.
func (s *Store) ListActiveIncidentPage(ctx context.Context, afterID int64, limit int) ([]incident.Incident, error) {
	if err := validateEvidencePage(limit, 0); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, selectIncidentEvidence+" WHERE i.resolved_at IS NULL AND i.id > ? ORDER BY i.id ASC LIMIT ?", afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []incident.Incident{}
	for rows.Next() {
		in, err := scanIncidentEvidence(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// IncidentEvidence returns an oldest-first timeline page and up to 501 monitor
// links. Status is taken from the latest update, independently of the page.
// Callers can request 501 rows to detect truncation at 500.
func (s *Store) IncidentEvidence(ctx context.Context, id int64, limit, offset int) (incident.Incident, error) {
	if err := validateEvidencePage(limit, offset); err != nil {
		return incident.Incident{}, err
	}
	in, err := scanIncidentEvidence(s.db.QueryRowContext(ctx, selectIncidentEvidence+" WHERE i.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return in, ErrNotFound
	}
	if err != nil {
		return in, err
	}
	if in.Updates, err = s.incidentEvidenceUpdates(ctx, id, limit, offset); err != nil {
		return in, err
	}
	// The timeline's rows are closed before acquiring the single connection again.
	rows, err := s.db.QueryContext(ctx, `SELECT monitor_id FROM incident_monitors
		WHERE incident_id = ? ORDER BY monitor_id ASC LIMIT 501`, id)
	if err != nil {
		return in, err
	}
	defer rows.Close()
	for rows.Next() {
		var mid int64
		if err := rows.Scan(&mid); err != nil {
			return in, err
		}
		in.MonitorIDs = append(in.MonitorIDs, mid)
	}
	return in, rows.Err()
}

func (s *Store) incidentEvidenceUpdates(ctx context.Context, id int64, limit, offset int) ([]incident.Update, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, status, message, created_at FROM incident_updates
		WHERE incident_id = ? ORDER BY created_at ASC, id ASC LIMIT ? OFFSET ?`, id, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []incident.Update{}
	for rows.Next() {
		var u incident.Update
		var at int64
		if err := rows.Scan(&u.ID, &u.Status, &u.Message, &at); err != nil {
			return nil, err
		}
		u.CreatedAt = fromMillis(at)
		out = append(out, u)
	}
	return out, rows.Err()
}
