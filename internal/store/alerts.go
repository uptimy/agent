package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
)

// The alert outbox: an alert is stored in the same transaction as the event
// that caused it, with one delivery per channel that covers its monitor.
// internal/delivery sends them, so an alert survives a failing channel and a
// restart.

// InsertEventAlert records a status change and the alert it causes. Held
// alerts wait for their maintenance window to end (ReleaseAlert). maxAge is
// how long a failing delivery is retried.
//
// A recovery whose down alert a channel is still holding for maintenance is
// news to nobody: both are suppressed on that channel.
func (s *Store) InsertEventAlert(ctx context.Context, e monitor.Event, a notify.Alert, held bool, maxAge time.Duration) (monitor.Event, error) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		var err error
		if e.ID, err = insertEvent(ctx, tx, e); err != nil {
			return err
		}
		if a.ID, err = insertAlert(ctx, tx, e.ID, a); err != nil {
			return err
		}

		notifierIDs, err := queryIDs(ctx, tx, `SELECT id FROM notifiers WHERE enabled = 1 AND (all_monitors = 1 OR id IN
			(SELECT notifier_id FROM notifier_monitors WHERE monitor_id = ?)) ORDER BY id`, e.MonitorID)
		if err != nil {
			return err
		}
		for _, nid := range notifierIDs {
			state, detail := notify.DeliveryPending, ""
			var done *int64
			switch {
			case held:
				state = notify.DeliveryHeld
			case a.Status == string(monitor.StatusUp):
				heldDown, err := heldDownAlert(ctx, tx, nid, e.MonitorID, a.ID)
				if err != nil {
					return err
				}
				if heldDown != 0 {
					state, detail = notify.DeliverySuppressed, "recovered during maintenance"
					done = new(now.UnixMilli())
					if _, err := tx.ExecContext(ctx, `UPDATE alert_deliveries SET state = ?, detail = ?, done_at = ?
						WHERE alert_id = ? AND notifier_id = ?`, notify.DeliverySuppressed, detail, *done, heldDown, nid); err != nil {
						return err
					}
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO alert_deliveries
				(alert_id, notifier_id, state, next_at, expires_at, detail, done_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
				a.ID, nid, state, now.UnixMilli(), now.Add(maxAge).UnixMilli(), detail, done); err != nil {
				return err
			}
		}
		return nil
	})
	return e, err
}

// insertAlert stores an alert about event eventID, without deliveries.
func insertAlert(ctx context.Context, tx *sql.Tx, eventID int64, a notify.Alert) (int64, error) {
	res, err := tx.ExecContext(ctx, "INSERT INTO alerts (monitor_id, event_id, status, type, ts, payload) VALUES (?, ?, ?, ?, ?, '')",
		a.MonitorID, eventID, a.Status, a.Type, a.Time.UnixMilli())
	if err != nil {
		return 0, err
	}
	if a.ID, err = res.LastInsertId(); err != nil {
		return 0, err
	}
	payload, err := json.Marshal(a)
	if err != nil {
		return 0, err
	}
	_, err = tx.ExecContext(ctx, "UPDATE alerts SET payload = ? WHERE id = ?", string(payload), a.ID)
	return a.ID, err
}

// InsertAlert stores a reminder or an acknowledgement about the outage that
// began with event eventID, for the given channels only.
func (s *Store) InsertAlert(ctx context.Context, eventID int64, a notify.Alert, notifierIDs []int64, maxAge time.Duration) error {
	now := time.Now().UTC().Truncate(time.Millisecond)
	return s.inTx(ctx, func(tx *sql.Tx) error {
		id, err := insertAlert(ctx, tx, eventID, a)
		if err != nil {
			return err
		}
		for _, nid := range notifierIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO alert_deliveries (alert_id, notifier_id, state, next_at, expires_at)
				VALUES (?, ?, 'pending', ?, ?)`, id, nid, now.UnixMilli(), now.Add(maxAge).UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
}

// heldDownAlert returns the down alert a channel holds for a monitor, or 0.
func heldDownAlert(ctx context.Context, tx *sql.Tx, notifierID, monitorID, before int64) (int64, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT a.id FROM alert_deliveries d JOIN alerts a ON a.id = d.alert_id
		WHERE d.notifier_id = ? AND a.monitor_id = ? AND a.id < ? AND a.status = 'down' AND a.type = '' AND d.state = ?
		ORDER BY a.id DESC LIMIT 1`, notifierID, monitorID, before, notify.DeliveryHeld).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func queryIDs(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Outgoing is a delivery that's due, with what it needs to be sent.
type Outgoing struct {
	Delivery  notify.Delivery
	Alert     notify.Alert
	Notifier  notify.Notifier
	MonitorID int64
	// Unheard is set for a recovery whose outage this channel was alerted
	// about, but never got any of those alerts: failed, expired or
	// suppressed. A channel with no alerts about the outage at all (added
	// during it) still hears the recovery.
	Unheard bool
}

// DueDeliveries returns pending deliveries whose time has come, oldest
// first. Alerts about one monitor reach a channel in order: a delivery
// waits while an earlier one for the same channel and monitor is pending
// or held.
func (s *Store) DueDeliveries(ctx context.Context, now time.Time, limit int) ([]Outgoing, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT d.alert_id, a.type, d.notifier_id, d.attempts, d.next_at, d.expires_at, d.detail,
			a.monitor_id, a.payload, n.name, n.type, n.enabled, n.config,
			-- For a recovery: alerts about the outage on this channel, none of which got through.
			CASE WHEN a.status = 'up' THEN (SELECT COUNT(*) > 0 AND SUM(d2.state = 'sent') = 0
				FROM alert_deliveries d2 JOIN alerts a2 ON a2.id = d2.alert_id
				WHERE d2.notifier_id = d.notifier_id AND a2.monitor_id = a.monitor_id AND a2.id < a.id AND a2.status = 'down'
				AND a2.id > COALESCE((SELECT MAX(a4.id) FROM alerts a4
					WHERE a4.monitor_id = a.monitor_id AND a4.id < a.id AND a4.status = 'up'), 0)) END
		FROM alert_deliveries d
		JOIN alerts a ON a.id = d.alert_id
		JOIN notifiers n ON n.id = d.notifier_id
		WHERE d.state = 'pending' AND d.next_at <= ?
		AND NOT EXISTS (SELECT 1 FROM alert_deliveries d3 JOIN alerts a3 ON a3.id = d3.alert_id
			WHERE d3.notifier_id = d.notifier_id AND a3.monitor_id = a.monitor_id AND a3.id < a.id
			AND d3.state IN ('pending', 'held'))
		ORDER BY a.id, d.notifier_id LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Outgoing
	for rows.Next() {
		var (
			o                 Outgoing
			nextAt, expiresAt int64
			payload, cfg      string
			unheard           sql.NullBool
		)
		if err := rows.Scan(&o.Delivery.AlertID, &o.Delivery.AlertType, &o.Delivery.NotifierID, &o.Delivery.Attempts, &nextAt, &expiresAt, &o.Delivery.Detail,
			&o.MonitorID, &payload, &o.Notifier.Name, &o.Notifier.Type, &o.Notifier.Enabled, &cfg, &unheard); err != nil {
			return nil, err
		}
		o.Delivery.State = notify.DeliveryPending
		o.Delivery.NotifierName, o.Delivery.NotifierType = o.Notifier.Name, o.Notifier.Type
		o.Delivery.NextAttempt, o.Delivery.ExpiresAt = fromMillis(nextAt), fromMillis(expiresAt)
		o.Notifier.ID = o.Delivery.NotifierID
		o.Unheard = unheard.Bool
		if err := json.Unmarshal([]byte(payload), &o.Alert); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(cfg), &o.Notifier.Config); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// NextDeliveryAt returns when the next pending delivery is due, if any.
func (s *Store) NextDeliveryAt(ctx context.Context) (time.Time, bool, error) {
	var next sql.NullInt64
	err := s.db.QueryRowContext(ctx, "SELECT MIN(next_at) FROM alert_deliveries WHERE state = 'pending'").Scan(&next)
	if err != nil || !next.Valid {
		return time.Time{}, false, err
	}
	return fromMillis(next.Int64), true, nil
}

// UpdateDelivery saves the outcome of an attempt.
func (s *Store) UpdateDelivery(ctx context.Context, d notify.Delivery) error {
	var done any
	if d.DoneAt != nil {
		done = d.DoneAt.UnixMilli()
	}
	_, err := s.db.ExecContext(ctx, `UPDATE alert_deliveries SET state = ?, attempts = ?, next_at = ?, detail = ?, done_at = ?
		WHERE alert_id = ? AND notifier_id = ?`,
		d.State, d.Attempts, d.NextAttempt.UnixMilli(), d.Detail, done, d.AlertID, d.NotifierID)
	return err
}

// HeldAlert is an alert waiting for a maintenance window to end.
type HeldAlert struct {
	AlertID   int64
	MonitorID int64
}

// HeldAlerts returns the alerts some channel holds, newest first.
func (s *Store) HeldAlerts(ctx context.Context) ([]HeldAlert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT a.id, a.monitor_id FROM alerts a
		JOIN alert_deliveries d ON d.alert_id = a.id WHERE d.state = 'held' ORDER BY a.id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HeldAlert
	for rows.Next() {
		var h HeldAlert
		if err := rows.Scan(&h.AlertID, &h.MonitorID); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// ReleaseAlert sends a held alert after all, as of at and with a new
// message ("still down after maintenance").
func (s *Store) ReleaseAlert(ctx context.Context, alertID int64, message string, at time.Time, maxAge time.Duration) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		var payload string
		if err := tx.QueryRowContext(ctx, "SELECT payload FROM alerts WHERE id = ?", alertID).Scan(&payload); err != nil {
			return err
		}
		var a notify.Alert
		if err := json.Unmarshal([]byte(payload), &a); err != nil {
			return err
		}
		a.Message, a.Time = message, at.UTC()
		raw, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE alerts SET payload = ?, ts = ? WHERE id = ?", string(raw), at.UnixMilli(), alertID); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE alert_deliveries SET state = 'pending', next_at = ?, expires_at = ?
			WHERE alert_id = ? AND state = 'held'`, at.UnixMilli(), at.Add(maxAge).UnixMilli(), alertID)
		return err
	})
}

// SuppressHeld drops a held alert: the monitor recovered, was paused or
// the alert was superseded.
func (s *Store) SuppressHeld(ctx context.Context, alertID int64, detail string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `UPDATE alert_deliveries SET state = 'suppressed', detail = ?, done_at = ?
		WHERE alert_id = ? AND state = 'held'`, detail, at.UnixMilli(), alertID)
	return err
}

// RetryDelivery tries a failed, expired or waiting delivery again now.
func (s *Store) RetryDelivery(ctx context.Context, alertID, notifierID int64, now time.Time, maxAge time.Duration) error {
	res, err := s.db.ExecContext(ctx, `UPDATE alert_deliveries SET state = 'pending', next_at = ?, expires_at = ?, done_at = NULL
		WHERE alert_id = ? AND notifier_id = ? AND state IN ('pending', 'failed', 'expired')`,
		now.UnixMilli(), now.Add(maxAge).UnixMilli(), alertID, notifierID)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return nil
}

// AlertMonitor returns the monitor an alert is about.
func (s *Store) AlertMonitor(ctx context.Context, alertID int64) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT monitor_id FROM alerts WHERE id = ?", alertID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, ErrNotFound
	}
	return id, err
}

// deliveryColumns are scanned by scanDelivery; the query joins alerts as a
// and notifiers as n.
const deliveryColumns = `d.alert_id, a.type, d.notifier_id, n.name, n.type, d.state, d.attempts, d.next_at, d.detail, d.done_at`

func scanDelivery(row scanner) (notify.Delivery, error) {
	var (
		d      notify.Delivery
		nextAt int64
		doneAt sql.NullInt64
	)
	if err := row.Scan(&d.AlertID, &d.AlertType, &d.NotifierID, &d.NotifierName, &d.NotifierType, &d.State, &d.Attempts, &nextAt, &d.Detail, &doneAt); err != nil {
		return d, err
	}
	if d.State == notify.DeliveryPending {
		d.NextAttempt = fromMillis(nextAt)
	}
	if doneAt.Valid {
		d.DoneAt = new(fromMillis(doneAt.Int64))
	}
	return d, nil
}

// EventDeliveries returns, for each of the events, how its alert went on
// each channel.
func (s *Store) EventDeliveries(ctx context.Context, eventIDs []int64) (map[int64][]notify.Delivery, error) {
	out := map[int64][]notify.Delivery{}
	if len(eventIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(eventIDs))
	for i, id := range eventIDs {
		args[i] = id
	}
	placeholders := "?" + strings.Repeat(", ?", len(eventIDs)-1)
	//nolint:gosec // G202: only placeholders are added
	query := `SELECT a.event_id, ` + deliveryColumns + ` FROM alert_deliveries d
		JOIN alerts a ON a.id = d.alert_id JOIN notifiers n ON n.id = d.notifier_id
		WHERE a.event_id IN (` + placeholders + `) ORDER BY n.name, n.id, a.id`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var eventID int64
		d, err := scanDelivery(prefixScanner{rows, &eventID})
		if err != nil {
			return nil, err
		}
		out[eventID] = append(out[eventID], d)
	}
	return out, rows.Err()
}

// prefixScanner scans a leading column into first, then the rest.
type prefixScanner struct {
	row   scanner
	first any
}

func (p prefixScanner) Scan(dest ...any) error { return p.row.Scan(append([]any{p.first}, dest...)...) }

// ChannelHealth summarizes recent deliveries to one channel.
type ChannelHealth struct {
	// Last is the newest delivery that finished (sent, failed or expired).
	Last *notify.Delivery `json:"last"`
	// Failed counts deliveries that failed or expired since the given time.
	Failed int `json:"failed"`
	// Pending counts deliveries waiting for an attempt, retries included.
	Pending int `json:"pending"`
}

// ChannelHealth returns each channel's recent deliveries, by notifier ID.
func (s *Store) ChannelHealth(ctx context.Context, since time.Time) (map[int64]ChannelHealth, error) {
	out := map[int64]ChannelHealth{}
	rows, err := s.db.QueryContext(ctx, `SELECT notifier_id,
		SUM(state IN ('failed', 'expired') AND done_at >= ?), SUM(state = 'pending')
		FROM alert_deliveries GROUP BY notifier_id`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var h ChannelHealth
		if err := rows.Scan(&id, &h.Failed, &h.Pending); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = h
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// The newest finished delivery per channel; suppressed ones say
	// nothing about the channel.
	rows, err = s.db.QueryContext(ctx, `SELECT `+deliveryColumns+` FROM alert_deliveries d
		JOIN alerts a ON a.id = d.alert_id JOIN notifiers n ON n.id = d.notifier_id
		WHERE d.done_at = (SELECT MAX(done_at) FROM alert_deliveries
			WHERE notifier_id = d.notifier_id AND state IN ('sent', 'failed', 'expired'))
		AND d.state IN ('sent', 'failed', 'expired')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, err
		}
		h := out[d.NotifierID]
		if h.Last == nil || d.AlertID > h.Last.AlertID {
			h.Last = &d
		}
		out[d.NotifierID] = h
	}
	return out, rows.Err()
}

// LastAlertAt returns when a channel was last alerted that a monitor is
// down (a status change or a reminder, whatever became of it), and whether
// one is still waiting to go out.
func (s *Store) LastAlertAt(ctx context.Context, notifierID, monitorID int64) (last time.Time, waiting bool, err error) {
	var ts sql.NullInt64
	err = s.db.QueryRowContext(ctx, `SELECT
		(SELECT MAX(a.ts) FROM alerts a JOIN alert_deliveries d ON d.alert_id = a.id
			WHERE d.notifier_id = ?1 AND a.monitor_id = ?2 AND a.status = 'down' AND a.type IN ('', 'reminder')),
		EXISTS (SELECT 1 FROM alerts a JOIN alert_deliveries d ON d.alert_id = a.id
			WHERE d.notifier_id = ?1 AND a.monitor_id = ?2 AND d.state IN ('pending', 'held'))`,
		notifierID, monitorID).Scan(&ts, &waiting)
	if ts.Valid {
		last = fromMillis(ts.Int64)
	}
	return last, waiting, err
}

// Acknowledge records that someone is on a monitor's outage, which began
// with event eventID.
func (s *Store) Acknowledge(ctx context.Context, monitorID, eventID int64, by string, at time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO acknowledgements (monitor_id, event_id, by_user, ts) VALUES (?, ?, ?, ?)
		ON CONFLICT (monitor_id) DO UPDATE SET event_id = excluded.event_id, by_user = excluded.by_user, ts = excluded.ts`,
		monitorID, eventID, by, at.UnixMilli())
	return err
}

// Acknowledgements returns the current acknowledgements, by monitor ID.
func (s *Store) Acknowledgements(ctx context.Context) (map[int64]monitor.Acknowledgement, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT monitor_id, by_user, ts FROM acknowledgements")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]monitor.Acknowledgement{}
	for rows.Next() {
		var (
			id int64
			a  monitor.Acknowledgement
			ts int64
		)
		if err := rows.Scan(&id, &a.By, &ts); err != nil {
			return nil, err
		}
		a.Time = fromMillis(ts)
		out[id] = a
	}
	return out, rows.Err()
}

// Acknowledgement returns who's on a monitor's current outage, or nil.
func (s *Store) Acknowledgement(ctx context.Context, monitorID int64) (*monitor.Acknowledgement, error) {
	var (
		a  monitor.Acknowledgement
		ts int64
	)
	err := s.db.QueryRowContext(ctx, "SELECT by_user, ts FROM acknowledgements WHERE monitor_id = ?", monitorID).Scan(&a.By, &ts)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Time = fromMillis(ts)
	return &a, nil
}
