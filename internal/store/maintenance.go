package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/uptimy/agent/internal/maintenance"
)

const maintenanceColumns = "id, title, description, starts_at, ends_at, all_monitors, public, created_at"

// ListMaintenance returns the windows that end after since, soonest first.
func (s *Store) ListMaintenance(ctx context.Context, since time.Time) (maintenance.Schedule, error) {
	return s.queryMaintenance(ctx, "WHERE ends_at > ?", since.UnixMilli())
}

// GetMaintenance returns one window.
func (s *Store) GetMaintenance(ctx context.Context, id int64) (maintenance.Window, error) {
	ws, err := s.queryMaintenance(ctx, "WHERE id = ?", id)
	if err != nil {
		return maintenance.Window{}, err
	}
	if len(ws) == 0 {
		return maintenance.Window{}, ErrNotFound
	}
	return ws[0], nil
}

func (s *Store) queryMaintenance(ctx context.Context, where string, args ...any) (maintenance.Schedule, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+maintenanceColumns+" FROM maintenance "+where+" ORDER BY starts_at, id", args...) //nolint:gosec // G202: where is a constant
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := maintenance.Schedule{}
	byID := map[int64]int{}
	for rows.Next() {
		var (
			w                         maintenance.Window
			starts, ends, createdAtMs int64
		)
		if err := rows.Scan(&w.ID, &w.Title, &w.Description, &starts, &ends, &w.AllMonitors, &w.Public, &createdAtMs); err != nil {
			return nil, err
		}
		w.StartsAt, w.EndsAt, w.CreatedAt = fromMillis(starts), fromMillis(ends), fromMillis(createdAtMs)
		w.MonitorIDs = []int64{}
		byID[w.ID] = len(out)
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}
	links, err := s.db.QueryContext(ctx, "SELECT maintenance_id, monitor_id FROM maintenance_monitors ORDER BY monitor_id")
	if err != nil {
		return nil, err
	}
	defer links.Close()
	for links.Next() {
		var wid, mid int64
		if err := links.Scan(&wid, &mid); err != nil {
			return nil, err
		}
		if i, ok := byID[wid]; ok {
			out[i].MonitorIDs = append(out[i].MonitorIDs, mid)
		}
	}
	return out, links.Err()
}

// CreateMaintenance stores a new window.
func (s *Store) CreateMaintenance(ctx context.Context, w maintenance.Window) (maintenance.Window, error) {
	w.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	err := s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO maintenance (title, description, starts_at, ends_at, all_monitors, public, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			w.Title, w.Description, w.StartsAt.UnixMilli(), w.EndsAt.UnixMilli(), w.AllMonitors, w.Public, w.CreatedAt.UnixMilli())
		if err != nil {
			return err
		}
		if w.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return setMaintenanceMonitors(ctx, tx, w)
	})
	if err != nil {
		return w, err
	}
	if err := s.maintenanceChanged(ctx, w); err != nil {
		return w, err
	}
	return s.GetMaintenance(ctx, w.ID)
}

// maintenanceChanged rolls up again the past days a window covers (or
// covered): public windows don't count towards uptime.
func (s *Store) maintenanceChanged(ctx context.Context, w maintenance.Window) error {
	return s.changed(ctx, 0, w.StartsAt, w.EndsAt)
}

// UpdateMaintenance saves a window.
func (s *Store) UpdateMaintenance(ctx context.Context, w maintenance.Window) (maintenance.Window, error) {
	old, err := s.GetMaintenance(ctx, w.ID)
	if err != nil {
		return w, err
	}
	err = s.inTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE maintenance SET title = ?, description = ?, starts_at = ?, ends_at = ?,
			all_monitors = ?, public = ? WHERE id = ?`,
			w.Title, w.Description, w.StartsAt.UnixMilli(), w.EndsAt.UnixMilli(), w.AllMonitors, w.Public, w.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return setMaintenanceMonitors(ctx, tx, w)
	})
	if err != nil {
		return w, err
	}
	if err := s.maintenanceChanged(ctx, old); err != nil {
		return w, err
	}
	if err := s.maintenanceChanged(ctx, w); err != nil {
		return w, err
	}
	return s.GetMaintenance(ctx, w.ID)
}

func setMaintenanceMonitors(ctx context.Context, tx *sql.Tx, w maintenance.Window) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM maintenance_monitors WHERE maintenance_id = ?", w.ID); err != nil {
		return err
	}
	if w.AllMonitors {
		return nil
	}
	for _, mid := range w.MonitorIDs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO maintenance_monitors (maintenance_id, monitor_id) VALUES (?, ?)", w.ID, mid); err != nil {
			return unknownMonitor(err)
		}
	}
	return nil
}

// DeleteMaintenance removes a window.
func (s *Store) DeleteMaintenance(ctx context.Context, id int64) error {
	old, err := s.GetMaintenance(ctx, id)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx, "DELETE FROM maintenance WHERE id = ?", id); err != nil {
		return err
	}
	return s.maintenanceChanged(ctx, old)
}
