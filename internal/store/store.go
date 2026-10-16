// Package store persists monitors, their history and settings in SQLite.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: no CGO, so static builds and distroless images work
)

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
	// Removed says what this Open's migrations deleted (settings or
	// channels an upgrade dropped), for the startup log. Empty after the
	// first start on a version.
	Removed []string
}

// Open opens (and creates, if needed) the database at path and migrates it.
func Open(path string) (*Store, error) {
	// The database holds password hashes, webhook URLs and keys: keep the
	// directory from other users on the machine.
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// A single connection serializes writes and avoids SQLITE_BUSY; the
	// workload is small enough that this is never the bottleneck.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// migrations are applied in order, each once; the database's user_version
// records how many have run. Append new ones; never edit a released one.
var migrations = []string{
	// 1: initial schema.
	`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT NOT NULL UNIQUE COLLATE NOCASE,
		password_hash TEXT NOT NULL,
		role TEXT NOT NULL,
		must_change_password INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		last_login_at INTEGER
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		expires_at INTEGER NOT NULL
	);
	CREATE INDEX sessions_user ON sessions(user_id);

	CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);

	CREATE TABLE notifiers (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		type TEXT NOT NULL,
		enabled INTEGER NOT NULL DEFAULT 1,
		config TEXT NOT NULL DEFAULT '{}',
		created_at INTEGER NOT NULL
	);

	-- What's common to healthchecks and heartbeats; their settings live in
	-- the table for their kind. status_* is the status page presentation,
	-- owned by the status page editor (the monitors file never writes it).
	CREATE TABLE monitors (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL CHECK (kind IN ('healthcheck', 'heartbeat')),
		name TEXT NOT NULL,
		paused INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT 'ui',
		public INTEGER NOT NULL DEFAULT 1,
		status_label TEXT NOT NULL DEFAULT '',
		status_order INTEGER NOT NULL DEFAULT 0,
		status_section TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		updated_at INTEGER NOT NULL
	);
	CREATE TABLE healthchecks (
		monitor_id INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
		type TEXT NOT NULL,
		target TEXT NOT NULL,
		interval_seconds INTEGER NOT NULL,
		timeout_seconds INTEGER NOT NULL,
		failure_threshold INTEGER NOT NULL,
		config TEXT NOT NULL DEFAULT '{}'
	);
	CREATE TABLE heartbeats (
		monitor_id INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
		token TEXT NOT NULL UNIQUE,
		every_seconds INTEGER NOT NULL DEFAULT 0,
		cron TEXT NOT NULL DEFAULT '',
		timezone TEXT NOT NULL DEFAULT '',
		grace_seconds INTEGER NOT NULL
	);

	-- Healthcheck probe results.
	CREATE TABLE results (
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		ts INTEGER NOT NULL,
		ok INTEGER NOT NULL,
		latency_ms INTEGER NOT NULL,
		message TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX results_monitor_ts ON results(monitor_id, ts);

	-- Heartbeat runs. ts orders them: when the run finished, started (still
	-- running) or was due (missed).
	CREATE TABLE runs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		ts INTEGER NOT NULL,
		due_at INTEGER,
		started_at INTEGER,
		finished_at INTEGER,
		outcome TEXT NOT NULL,
		on_time INTEGER NOT NULL DEFAULT 0,
		duration_ms INTEGER,
		message TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX runs_monitor_ts ON runs(monitor_id, ts);

	-- Status changes, for both kinds.
	CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		ts INTEGER NOT NULL,
		status TEXT NOT NULL,
		message TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX events_monitor_ts ON events(monitor_id, ts);`,

	// 2: alert routing, maintenance windows and API tokens.
	`-- A notifier alerts for every monitor, or only for those listed.
	ALTER TABLE notifiers ADD COLUMN all_monitors INTEGER NOT NULL DEFAULT 1;
	CREATE TABLE notifier_monitors (
		notifier_id INTEGER NOT NULL REFERENCES notifiers(id) ON DELETE CASCADE,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		PRIMARY KEY (notifier_id, monitor_id)
	);
	CREATE INDEX notifier_monitors_monitor ON notifier_monitors(monitor_id);

	-- Planned work: no down alerts for the monitors it covers, and a notice
	-- on the status page when public.
	CREATE TABLE maintenance (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		description TEXT NOT NULL DEFAULT '',
		starts_at INTEGER NOT NULL,
		ends_at INTEGER NOT NULL,
		all_monitors INTEGER NOT NULL DEFAULT 1,
		public INTEGER NOT NULL DEFAULT 1,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX maintenance_ends ON maintenance(ends_at);
	CREATE TABLE maintenance_monitors (
		maintenance_id INTEGER NOT NULL REFERENCES maintenance(id) ON DELETE CASCADE,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		PRIMARY KEY (maintenance_id, monitor_id)
	);

	-- Personal API tokens. Only a hash is kept; hint is the last characters,
	-- to tell tokens apart.
	CREATE TABLE api_tokens (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		name TEXT NOT NULL,
		token_hash TEXT NOT NULL UNIQUE,
		hint TEXT NOT NULL,
		read_only INTEGER NOT NULL DEFAULT 0,
		created_at INTEGER NOT NULL,
		last_used_at INTEGER
	);
	CREATE INDEX api_tokens_user ON api_tokens(user_id);`,

	// 3: what a monitor from outside the UI was made from (a Kubernetes
	// object), so renaming it keeps its history.
	`ALTER TABLE monitors ADD COLUMN source_ref TEXT NOT NULL DEFAULT '';`,

	// 4: two-factor sign-in. totp_secret is set while setting up (enabled 0)
	// and while on; totp_last_step stops a code being used twice. Recovery
	// codes are kept as SHA-256 hashes and deleted when used.
	`ALTER TABLE users ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
	ALTER TABLE users ADD COLUMN totp_enabled INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE users ADD COLUMN totp_last_step INTEGER NOT NULL DEFAULT 0;
	CREATE TABLE recovery_codes (
		user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		code_hash TEXT NOT NULL,
		PRIMARY KEY (user_id, code_hash)
	);`,

	// 5: incidents posted on the status page, with their timeline and the
	// monitors they affect.
	`CREATE TABLE incidents (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		title TEXT NOT NULL,
		severity TEXT NOT NULL DEFAULT '',
		created_at INTEGER NOT NULL,
		resolved_at INTEGER
	);
	CREATE INDEX incidents_resolved ON incidents(resolved_at);
	CREATE TABLE incident_updates (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
		status TEXT NOT NULL DEFAULT '',
		message TEXT NOT NULL,
		created_at INTEGER NOT NULL
	);
	CREATE INDEX incident_updates_incident ON incident_updates(incident_id);
	CREATE TABLE incident_monitors (
		incident_id INTEGER NOT NULL REFERENCES incidents(id) ON DELETE CASCADE,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		PRIMARY KEY (incident_id, monitor_id)
	);`,

	// 6: the alert outbox. An alert is written with the event that caused
	// it, and delivered to each channel separately, with retries.
	`CREATE TABLE alerts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		monitor_id INTEGER NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
		event_id INTEGER REFERENCES events(id) ON DELETE SET NULL,
		status TEXT NOT NULL,
		-- '' for a status change, 'reminder' or 'acknowledged'.
		type TEXT NOT NULL DEFAULT '',
		ts INTEGER NOT NULL,
		-- The notify.Alert as sent, so a retry sends what happened then.
		payload TEXT NOT NULL
	);
	CREATE INDEX alerts_monitor ON alerts(monitor_id, id);
	CREATE INDEX alerts_event ON alerts(event_id);
	CREATE INDEX alerts_ts ON alerts(ts);
	CREATE TABLE alert_deliveries (
		alert_id INTEGER NOT NULL REFERENCES alerts(id) ON DELETE CASCADE,
		notifier_id INTEGER NOT NULL REFERENCES notifiers(id) ON DELETE CASCADE,
		state TEXT NOT NULL,
		attempts INTEGER NOT NULL DEFAULT 0,
		next_at INTEGER NOT NULL,
		expires_at INTEGER NOT NULL,
		detail TEXT NOT NULL DEFAULT '',
		done_at INTEGER,
		PRIMARY KEY (alert_id, notifier_id)
	);
	CREATE INDEX alert_deliveries_state ON alert_deliveries(state, next_at);
	CREATE INDEX alert_deliveries_notifier ON alert_deliveries(notifier_id, done_at);

	-- Consecutive successes before a down healthcheck is up again.
	ALTER TABLE healthchecks ADD COLUMN recovery_threshold INTEGER NOT NULL DEFAULT 1;

	-- Remind a channel every repeat_seconds while a monitor stays down; 0 is off.
	ALTER TABLE notifiers ADD COLUMN repeat_seconds INTEGER NOT NULL DEFAULT 0;

	-- Someone is on it: no more reminders for this outage. Cleared by the
	-- monitor's next status change.
	CREATE TABLE acknowledgements (
		monitor_id INTEGER PRIMARY KEY REFERENCES monitors(id) ON DELETE CASCADE,
		event_id INTEGER NOT NULL,
		by_user TEXT NOT NULL,
		ts INTEGER NOT NULL
	);`,

	// 7: Uptimy Cloud sync. Each monitor mirrored to Cloud, with what was last
	// sent (hash), so only changes are pushed. No foreign key: a monitor
	// deleted here keeps its row until its mirror is deleted in Cloud.
	// The "Uptimy" alert channel is gone (the sync replaces it).
	`CREATE TABLE cloud_monitors (
		monitor_id INTEGER PRIMARY KEY,
		kind TEXT NOT NULL,
		client_ref TEXT NOT NULL,
		cloud_uuid TEXT NOT NULL,
		hash TEXT NOT NULL
	);
	DELETE FROM notifiers WHERE type = 'uptimy';
	-- Pasted check-in URLs are gone too: connecting is Connect to Uptimy Cloud.
	DELETE FROM settings WHERE key = 'uptimy_heartbeat_url';`,

	// 8: why a healthcheck failed: the HTTP status code it got (0 for none or
	// another check type) and the failure's class (timeout, dns, tls,
	// connect, http_5xx, http_4xx, assertion, other; '' when it was up).
	`ALTER TABLE results ADD COLUMN status_code INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE results ADD COLUMN error_class TEXT NOT NULL DEFAULT '';`,

	// 9: runs change after they're recorded (a late finish turns a missed
	// run into a success; a start finishes or is abandoned), so the Cloud
	// sync follows seq, which every insert and update raises, not id.
	// Starting from id keeps an upload cursor taken from ids valid.
	`ALTER TABLE runs ADD COLUMN seq INTEGER NOT NULL DEFAULT 0;
	UPDATE runs SET seq = id;
	CREATE INDEX runs_seq ON runs(seq);`,
}

// migrationNotes, by migration number, say what a migration is about to
// delete, so an upgrade logs it once.
var migrationNotes = map[int]func(context.Context, *sql.Tx) ([]string, error){
	7: func(ctx context.Context, tx *sql.Tx) ([]string, error) {
		var removed []string
		rows, err := tx.QueryContext(ctx, "SELECT name FROM notifiers WHERE type = 'uptimy'")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return nil, err
			}
			removed = append(removed, fmt.Sprintf("the Uptimy alert channel %q (Uptimy Cloud sync replaces it)", name))
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		var n int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings WHERE key = 'uptimy_heartbeat_url'").Scan(&n); err != nil {
			return nil, err
		}
		if n > 0 {
			removed = append(removed, "the pasted Uptimy check-in URL (connect via Settings → Uptimy Cloud instead)")
		}
		return removed, nil
	},
}

func (s *Store) migrate(ctx context.Context) error {
	var version int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("the database is from a newer version of Uptimy Agent (schema %d, this version knows %d); upgrade the agent", version, len(migrations))
	}
	for i := version; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if notes := migrationNotes[i+1]; notes != nil {
			removed, err := notes(ctx, tx)
			if err != nil {
				tx.Rollback()
				return fmt.Errorf("migration %d: %w", i+1, err)
			}
			s.Removed = append(s.Removed, removed...)
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// Prune deletes history (results, runs, events) older than before.
func (s *Store) Prune(ctx context.Context, before time.Time) error {
	ms := before.UnixMilli()
	for _, table := range []string{"results", "runs"} {
		if _, err := s.db.ExecContext(ctx, "DELETE FROM "+table+" WHERE ts < ?", ms); err != nil { //nolint:gosec // G202: table names are constants
			return err
		}
	}
	// A monitor's newest event is its current status, which a restart
	// restores: keep it however old, or a monitor down for longer than the
	// retention would come back pending and alert again.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE ts < ?
		AND id NOT IN (SELECT MAX(id) FROM events GROUP BY monitor_id)`, ms); err != nil {
		return err
	}
	// Alerts still waiting to go out are kept.
	if _, err := s.db.ExecContext(ctx, `DELETE FROM alerts WHERE ts < ? AND NOT EXISTS
		(SELECT 1 FROM alert_deliveries WHERE alert_id = alerts.id AND state IN ('pending', 'held'))`, ms); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM maintenance WHERE ends_at < ?", ms); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at < ?", time.Now().UnixMilli())
	return err
}

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
