package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/uptimy/agent/internal/monitor"
)

// A monitor is a row in monitors plus its settings in healthchecks or
// heartbeats; reads join them back together.
const selectMonitors = `SELECT m.id, m.kind, m.name, m.paused, m.source, m.source_ref,
		m.public, m.status_label, m.status_order, m.status_section, m.created_at, m.updated_at,
		h.type, h.target, h.interval_seconds, h.timeout_seconds, h.failure_threshold, h.recovery_threshold, h.config,
		b.token, b.every_seconds, b.cron, b.timezone, b.grace_seconds
	FROM monitors m
	LEFT JOIN healthchecks h ON h.monitor_id = m.id
	LEFT JOIN heartbeats b ON b.monitor_id = m.id`

type scanner interface{ Scan(dest ...any) error }

func scanMonitor(row scanner) (monitor.Monitor, error) {
	var (
		m                monitor.Monitor
		created, updated int64
		// healthchecks
		hType, hTarget, hConfig                     sql.NullString
		hInterval, hTimeout, hThresholds, hRecovery sql.NullInt64
		// heartbeats
		bToken, bCron, bZone sql.NullString
		bEvery, bGrace       sql.NullInt64
	)
	err := row.Scan(&m.ID, &m.Kind, &m.Name, &m.Paused, &m.Source, &m.SourceRef,
		&m.Public, &m.StatusLabel, &m.StatusOrder, &m.StatusSection, &created, &updated,
		&hType, &hTarget, &hInterval, &hTimeout, &hThresholds, &hRecovery, &hConfig,
		&bToken, &bEvery, &bCron, &bZone, &bGrace)
	if err != nil {
		return m, err
	}
	m.CreatedAt, m.UpdatedAt = fromMillis(created), fromMillis(updated)
	switch m.Kind {
	case monitor.KindHealthcheck:
		c := &monitor.Check{
			Type: monitor.Type(hType.String), Target: hTarget.String,
			IntervalSeconds: int(hInterval.Int64), TimeoutSeconds: int(hTimeout.Int64), FailureThreshold: int(hThresholds.Int64), RecoveryThreshold: int(hRecovery.Int64),
		}
		if err := json.Unmarshal([]byte(hConfig.String), &c.Config); err != nil {
			return m, err
		}
		m.Check = c
	case monitor.KindHeartbeat:
		m.Heartbeat = &monitor.Heartbeat{
			Token: bToken.String, EverySeconds: int(bEvery.Int64), Cron: bCron.String,
			Timezone: bZone.String, GraceSeconds: int(bGrace.Int64),
		}
	}
	return m, nil
}

// ListMonitors returns all monitors ordered by name.
func (s *Store) ListMonitors(ctx context.Context) ([]monitor.Monitor, error) {
	rows, err := s.db.QueryContext(ctx, selectMonitors+" ORDER BY m.name COLLATE NOCASE, m.id")
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

// GetMonitor returns one monitor.
func (s *Store) GetMonitor(ctx context.Context, id int64) (monitor.Monitor, error) {
	m, err := scanMonitor(s.db.QueryRowContext(ctx, selectMonitors+" WHERE m.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return m, ErrNotFound
	}
	return m, err
}

// CreateMonitor inserts m and returns it with its ID and timestamps set.
func (s *Store) CreateMonitor(ctx context.Context, m monitor.Monitor) (monitor.Monitor, error) {
	now := time.Now().UTC()
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO monitors
			(kind, name, paused, source, source_ref, public, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.Kind, m.Name, m.Paused, m.Source, m.SourceRef, m.Public, now.UnixMilli(), now.UnixMilli())
		if err != nil {
			return err
		}
		if m.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return saveSettings(ctx, tx, m)
	})
	m.CreatedAt, m.UpdatedAt = now.Truncate(time.Millisecond), now.Truncate(time.Millisecond)
	return m, err
}

// UpdateMonitor saves m's name, state and settings. Its kind can't change,
// and its status page presentation is left alone (see SaveStatusPageLayout).
func (s *Store) UpdateMonitor(ctx context.Context, m monitor.Monitor) (monitor.Monitor, error) {
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE monitors SET name = ?, paused = ?, source = ?, source_ref = ?, public = ?, updated_at = ?
			WHERE id = ? AND kind = ?`,
			m.Name, m.Paused, m.Source, m.SourceRef, m.Public, time.Now().UTC().UnixMilli(), m.ID, m.Kind)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return saveSettings(ctx, tx, m)
	})
	if err != nil {
		return m, err
	}
	return s.GetMonitor(ctx, m.ID)
}

// saveSettings writes the settings for m's kind.
func saveSettings(ctx context.Context, tx *sql.Tx, m monitor.Monitor) error {
	switch m.Kind {
	case monitor.KindHealthcheck:
		c := m.Check
		cfg, err := json.Marshal(c.Config)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO healthchecks
			(monitor_id, type, target, interval_seconds, timeout_seconds, failure_threshold, recovery_threshold, config)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (monitor_id) DO UPDATE SET type = excluded.type, target = excluded.target,
				interval_seconds = excluded.interval_seconds, timeout_seconds = excluded.timeout_seconds,
				failure_threshold = excluded.failure_threshold, recovery_threshold = excluded.recovery_threshold, config = excluded.config`,
			m.ID, c.Type, c.Target, c.IntervalSeconds, c.TimeoutSeconds, c.FailureThreshold, c.RecoveryThreshold, string(cfg))
		return err
	case monitor.KindHeartbeat:
		h := m.Heartbeat
		_, err := tx.ExecContext(ctx, `INSERT INTO heartbeats
			(monitor_id, token, every_seconds, cron, timezone, grace_seconds) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (monitor_id) DO UPDATE SET token = excluded.token, every_seconds = excluded.every_seconds,
				cron = excluded.cron, timezone = excluded.timezone, grace_seconds = excluded.grace_seconds`,
			m.ID, h.Token, h.EverySeconds, h.Cron, h.Timezone, h.GraceSeconds)
		return err
	}
	return errors.New("unknown monitor kind")
}

// DeleteMonitor removes a monitor and, via cascade, its settings and history.
func (s *Store) DeleteMonitor(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM monitors WHERE id = ?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// inTx runs fn in a transaction, committing if it returns nil.
func (s *Store) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
