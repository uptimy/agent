package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/notify"
)

const notifierColumns = "id, name, type, enabled, config, created_at, all_monitors, repeat_seconds"

func scanNotifier(row scanner) (notify.Notifier, error) {
	var (
		n       notify.Notifier
		cfg     string
		created int64
	)
	if err := row.Scan(&n.ID, &n.Name, &n.Type, &n.Enabled, &cfg, &created, &n.AllMonitors, &n.RepeatSeconds); err != nil {
		return n, err
	}
	n.CreatedAt = fromMillis(created)
	n.MonitorIDs = []int64{}
	return n, json.Unmarshal([]byte(cfg), &n.Config)
}

func (s *Store) queryNotifiers(ctx context.Context, where string, args ...any) ([]notify.Notifier, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+notifierColumns+" FROM notifiers "+where+" ORDER BY id", args...) //nolint:gosec // G202: where is a constant
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []notify.Notifier{}
	for rows.Next() {
		n, err := scanNotifier(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, s.attachMonitorIDs(ctx, out)
}

// attachMonitorIDs fills in the monitors each notifier is limited to.
func (s *Store) attachMonitorIDs(ctx context.Context, ns []notify.Notifier) error {
	rows, err := s.db.QueryContext(ctx, "SELECT notifier_id, monitor_id FROM notifier_monitors ORDER BY monitor_id")
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := make(map[int64]*notify.Notifier, len(ns))
	for i := range ns {
		byID[ns[i].ID] = &ns[i]
	}
	for rows.Next() {
		var nid, mid int64
		if err := rows.Scan(&nid, &mid); err != nil {
			return err
		}
		if n := byID[nid]; n != nil && !n.AllMonitors {
			n.MonitorIDs = append(n.MonitorIDs, mid)
		}
	}
	return rows.Err()
}

// ListNotifiers returns all notifiers.
func (s *Store) ListNotifiers(ctx context.Context) ([]notify.Notifier, error) {
	return s.queryNotifiers(ctx, "")
}

// NotifiersFor returns the enabled notifiers that alert for a monitor.
func (s *Store) NotifiersFor(ctx context.Context, monitorID int64) ([]notify.Notifier, error) {
	return s.queryNotifiers(ctx, `WHERE enabled = 1 AND (all_monitors = 1 OR id IN
		(SELECT notifier_id FROM notifier_monitors WHERE monitor_id = ?))`, monitorID)
}

// GetNotifier returns one notifier.
func (s *Store) GetNotifier(ctx context.Context, id int64) (notify.Notifier, error) {
	ns, err := s.queryNotifiers(ctx, "WHERE id = ?", id)
	if err != nil {
		return notify.Notifier{}, err
	}
	if len(ns) == 0 {
		return notify.Notifier{}, ErrNotFound
	}
	return ns[0], nil
}

// CreateNotifier inserts a notifier.
func (s *Store) CreateNotifier(ctx context.Context, n notify.Notifier) (notify.Notifier, error) {
	cfg, err := json.Marshal(n.Config)
	if err != nil {
		return n, err
	}
	n.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "INSERT INTO notifiers (name, type, enabled, config, created_at, all_monitors, repeat_seconds) VALUES (?, ?, ?, ?, ?, ?, ?)",
			n.Name, n.Type, n.Enabled, string(cfg), n.CreatedAt.UnixMilli(), n.AllMonitors, n.RepeatSeconds)
		if err != nil {
			return err
		}
		if n.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return setNotifierMonitors(ctx, tx, n)
	})
	if err != nil {
		return n, err
	}
	return s.GetNotifier(ctx, n.ID)
}

// UpdateNotifier saves a notifier.
func (s *Store) UpdateNotifier(ctx context.Context, n notify.Notifier) (notify.Notifier, error) {
	cfg, err := json.Marshal(n.Config)
	if err != nil {
		return n, err
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE notifiers SET name = ?, type = ?, enabled = ?, config = ?, all_monitors = ?, repeat_seconds = ? WHERE id = ?",
			n.Name, n.Type, n.Enabled, string(cfg), n.AllMonitors, n.RepeatSeconds, n.ID)
		if err != nil {
			return err
		}
		if c, _ := res.RowsAffected(); c == 0 {
			return ErrNotFound
		}
		return setNotifierMonitors(ctx, tx, n)
	})
	if err != nil {
		return n, err
	}
	return s.GetNotifier(ctx, n.ID)
}

func setNotifierMonitors(ctx context.Context, tx *sql.Tx, n notify.Notifier) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM notifier_monitors WHERE notifier_id = ?", n.ID); err != nil {
		return err
	}
	if n.AllMonitors {
		return nil
	}
	for _, mid := range n.MonitorIDs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO notifier_monitors (notifier_id, monitor_id) VALUES (?, ?)", n.ID, mid); err != nil {
			return unknownMonitor(err)
		}
	}
	return nil
}

// ErrUnknownMonitor means a list names a monitor that doesn't exist (it may
// have just been deleted).
var ErrUnknownMonitor = errors.New("unknown monitor")

func unknownMonitor(err error) error {
	if err != nil && strings.Contains(err.Error(), "FOREIGN KEY") {
		return ErrUnknownMonitor
	}
	return err
}

// DeleteNotifier removes a notifier.
func (s *Store) DeleteNotifier(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, "DELETE FROM notifiers WHERE id = ?", id)
	if err != nil {
		return err
	}
	if c, _ := res.RowsAffected(); c == 0 {
		return ErrNotFound
	}
	return nil
}

// MonitorNotifierIDs returns the notifiers limited to a monitor that include
// it. Notifiers for every monitor aren't listed.
func (s *Store) MonitorNotifierIDs(ctx context.Context, monitorID int64) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT nm.notifier_id FROM notifier_monitors nm
		JOIN notifiers n ON n.id = nm.notifier_id
		WHERE nm.monitor_id = ? AND n.all_monitors = 0 ORDER BY nm.notifier_id`, monitorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetMonitorNotifiers sets which of the notifiers limited to some monitors
// include this one. Notifiers for every monitor are unaffected.
func (s *Store) SetMonitorNotifiers(ctx context.Context, monitorID int64, notifierIDs []int64) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM notifier_monitors WHERE monitor_id = ?", monitorID); err != nil {
			return err
		}
		for _, nid := range notifierIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO notifier_monitors (notifier_id, monitor_id)
				SELECT id, ? FROM notifiers WHERE id = ? AND all_monitors = 0`, monitorID, nid); err != nil {
				return unknownMonitor(err)
			}
		}
		return nil
	})
}
