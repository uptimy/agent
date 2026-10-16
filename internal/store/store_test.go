package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

// Upgrading past migration 7 says what it removed, once.
func TestMigrationRemoved(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "t.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range migrations[:6] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		"PRAGMA user_version = 6",
		"INSERT INTO notifiers (name, type, created_at) VALUES ('Uptimy', 'uptimy', 0), ('Ops', 'slack', 0)",
		"INSERT INTO settings (key, value) VALUES ('uptimy_heartbeat_url', 'https://heartbeats.upti.my/x')",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(st.Removed)
	if len(st.Removed) != 2 || !strings.Contains(got, `"Uptimy"`) || !strings.Contains(got, "check-in URL") || strings.Contains(got, "heartbeats.upti.my") {
		t.Fatalf("removed: %q", st.Removed)
	}
	st.Close()

	if st, err = Open(path); err != nil || len(st.Removed) != 0 {
		t.Fatalf("second start: %q %v", st.Removed, err)
	}
	st.Close()
}
