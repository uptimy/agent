package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/uptimy/agent/internal/connect"
	"github.com/uptimy/agent/internal/statuspage"
)

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestStatusPageSettings(t *testing.T) {
	ctx := context.Background()
	st := open(t)

	p, err := st.StatusPage(ctx)
	if err != nil || p.Title != "Service Status" || !p.Enabled || len(p.Sections) != 1 {
		t.Fatalf("defaults: %+v %v", p, err)
	}
	p.Title, p.Enabled = "Acme", false
	if err := st.SaveStatusPage(ctx, p, nil); err != nil {
		t.Fatal(err)
	}
	if p, _ = st.StatusPage(ctx); p.Title != "Acme" || p.Enabled {
		t.Fatalf("saved: %+v", p)
	}
	// A layout naming a deleted monitor saves nothing, settings included.
	p.Title = "Changed"
	if err := st.SaveStatusPage(ctx, p, []StatusPageEntry{{ID: 99}}); !IsNotFound(err) {
		t.Fatalf("unknown monitor: %v", err)
	}
	if p, _ = st.StatusPage(ctx); p.Title != "Acme" {
		t.Fatal("settings saved without their layout")
	}

	logo, _ := statuspage.NewLogo([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	if err := st.SetStatusPageLogo(ctx, statuspage.Dark, &logo); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.StatusPageLogo(ctx, statuspage.Dark); got == nil || string(got.Data) != string(logo.Data) {
		t.Fatalf("logo: %+v", got)
	}
	if got, _ := st.StatusPageLogo(ctx, statuspage.Light); got != nil {
		t.Fatal("the light logo was never set")
	}
	if err := st.SetStatusPageLogo(ctx, statuspage.Dark, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.StatusPageLogo(ctx, statuspage.Dark); got != nil {
		t.Fatal("logo not removed")
	}
}

func TestUptimySettings(t *testing.T) {
	ctx := context.Background()
	st := open(t)

	id, err := st.InstallID(ctx)
	if err != nil || len(id) < 10 {
		t.Fatalf("install id: %q %v", id, err)
	}
	if again, _ := st.InstallID(ctx); again != id {
		t.Fatal("the install ID changed")
	}

	if c, err := st.UptimyConnection(ctx); c != nil || err != nil {
		t.Fatalf("connection before connecting: %+v %v", c, err)
	}
	conn := connect.Connection{APIKey: "upt_key", WorkspaceName: "Acme", HeartbeatUUID: "hb-1"}
	if err := st.SaveUptimyConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}
	conn.Paused = true
	if err := st.SaveUptimyConnection(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.UptimyConnection(ctx); c == nil || c.WorkspaceName != "Acme" || !c.Paused {
		t.Fatalf("connection: %+v", c)
	}

	if err := st.DisconnectUptimy(ctx); err != nil {
		t.Fatal(err)
	}
	if c, _ := st.UptimyConnection(ctx); c != nil {
		t.Fatalf("still connected: %+v", c)
	}
	if again, _ := st.InstallID(ctx); again != id {
		t.Fatal("disconnecting must keep the install ID, so reconnecting reuses the heartbeat")
	}
}
