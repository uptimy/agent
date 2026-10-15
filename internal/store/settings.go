package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/uptimy/agent/internal/connect"
	"github.com/uptimy/agent/internal/statuspage"
)

// Settings that aren't rows of their own (the status page, the Uptimy
// connection) are kept as JSON in the settings table, under these keys.
const (
	keyStatusPage        = "status_page"
	keyUptimyConnection  = "uptimy_connection"
	keyInstallID         = "install_id"
	keyStatusPageLogoFmt = "status_page_logo_" // + variant
	// keyAnnouncement is apart from the page's settings so the editor's save
	// never overwrites it.
	keyAnnouncement = "status_page_announcement"
)

// execer is a *sql.DB or a *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func (s *Store) getSetting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

func setSetting(ctx context.Context, db execer, key, value string) error {
	_, err := db.ExecContext(ctx,
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value", key, value)
	return err
}

func deleteSetting(ctx context.Context, db execer, key string) error {
	_, err := db.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key)
	return err
}

// getJSON decodes the setting into v and reports whether it was set.
func (s *Store) getJSON(ctx context.Context, key string, v any) (bool, error) {
	raw, ok, err := s.getSetting(ctx, key)
	if !ok || err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

func setJSON(ctx context.Context, db execer, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return setSetting(ctx, db, key, string(raw))
}

// ── Status page ───────────────────────────────────────────────────────────

// StatusPage returns the status page settings; statuspage.Defaults until
// they're first saved.
func (s *Store) StatusPage(ctx context.Context) (statuspage.Settings, error) {
	p := statuspage.Defaults()
	_, err := s.getJSON(ctx, keyStatusPage, &p)
	return p, err
}

// StatusPageEntry is one monitor's presentation on the status page.
type StatusPageEntry struct {
	ID      int64  `json:"id"`
	Public  bool   `json:"public"`
	Label   string `json:"label"`
	Section string `json:"section"`
}

// SaveStatusPage stores the settings and the monitors' visibility, labels and
// order (the slice order) together. Monitors not listed keep theirs.
func (s *Store) SaveStatusPage(ctx context.Context, p statuspage.Settings, layout []StatusPageEntry) error {
	return s.inTx(ctx, func(tx *sql.Tx) error {
		if err := setJSON(ctx, tx, keyStatusPage, p); err != nil {
			return err
		}
		for i, e := range layout {
			res, err := tx.ExecContext(ctx,
				"UPDATE monitors SET public = ?, status_label = ?, status_order = ?, status_section = ? WHERE id = ?",
				e.Public, e.Label, i+1, e.Section, e.ID)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrNotFound
			}
		}
		return nil
	})
}

// StatusPageLogo returns a logo, or nil if there is none.
func (s *Store) StatusPageLogo(ctx context.Context, v statuspage.Variant) (*statuspage.Logo, error) {
	var l statuspage.Logo
	ok, err := s.getJSON(ctx, keyStatusPageLogoFmt+string(v), &l)
	if !ok || err != nil {
		return nil, err
	}
	return &l, nil
}

// SetStatusPageLogo stores a logo; nil removes it.
func (s *Store) SetStatusPageLogo(ctx context.Context, v statuspage.Variant, l *statuspage.Logo) error {
	if l == nil {
		return deleteSetting(ctx, s.db, keyStatusPageLogoFmt+string(v))
	}
	return setJSON(ctx, s.db, keyStatusPageLogoFmt+string(v), l)
}

// ── Uptimy ("Watch the watcher") ──────────────────────────────────────────

// UptimyConnection returns the account connection, or nil if there is none.
func (s *Store) UptimyConnection(ctx context.Context) (*connect.Connection, error) {
	var c connect.Connection
	ok, err := s.getJSON(ctx, keyUptimyConnection, &c)
	if !ok || err != nil {
		return nil, err
	}
	return &c, nil
}

// SaveUptimyConnection stores the account connection.
func (s *Store) SaveUptimyConnection(ctx context.Context, c connect.Connection) error {
	return setJSON(ctx, s.db, keyUptimyConnection, c)
}

// UpdateUptimyConnection stores a changed connection, e.g. paused or in
// maintenance.
func (s *Store) UpdateUptimyConnection(ctx context.Context, c connect.Connection) error {
	return setJSON(ctx, s.db, keyUptimyConnection, c)
}

// DisconnectUptimy forgets the account connection.
func (s *Store) DisconnectUptimy(ctx context.Context) error {
	return deleteSetting(ctx, s.db, keyUptimyConnection)
}

// InstallID is this agent's permanent identity towards Uptimy, created on
// first use. It makes heartbeat creation idempotent, so reconnecting reuses
// the same heartbeat.
func (s *Store) InstallID(ctx context.Context) (string, error) {
	id, ok, err := s.getSetting(ctx, keyInstallID)
	if ok || err != nil {
		return id, err
	}
	b := make([]byte, 12)
	_, _ = rand.Read(b) // never fails (crypto/rand panics instead)
	id = "agent-" + base64.RawURLEncoding.EncodeToString(b)
	return id, setSetting(ctx, s.db, keyInstallID, id)
}

// Announcement returns the status page's announcement, or nil.
func (s *Store) Announcement(ctx context.Context) (*statuspage.Announcement, error) {
	var a statuspage.Announcement
	ok, err := s.getJSON(ctx, keyAnnouncement, &a)
	if !ok || err != nil {
		return nil, err
	}
	return &a, nil
}

// SetAnnouncement stores the announcement; nil removes it.
func (s *Store) SetAnnouncement(ctx context.Context, a *statuspage.Announcement) error {
	if a == nil {
		return deleteSetting(ctx, s.db, keyAnnouncement)
	}
	return setJSON(ctx, s.db, keyAnnouncement, a)
}
