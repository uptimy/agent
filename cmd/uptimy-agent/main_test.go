package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/uptimy/agent/internal/config"
	"github.com/uptimy/agent/internal/store"
)

// A monitors file that can't be read or doesn't parse doesn't stop the agent.
func TestSyncMonitorsFileProblems(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	path := filepath.Join(dir, "monitors.yaml")
	cfg := config.Config{MonitorsFile: path}
	if p, err := syncMonitorsFile(ctx, cfg, st, log); err != nil || p == nil || p.File != "monitors.yaml" || p.Applied {
		t.Fatalf("missing file: %+v %v", p, err)
	}
	if err := os.WriteFile(path, []byte("healthchecks:\n  - {name: a, type: tcp, target: x:1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if p, err := syncMonitorsFile(ctx, cfg, st, log); err != nil || p != nil {
		t.Fatalf("valid file: %+v %v", p, err)
	}
	cfg = config.Config{MonitorsYAML: "healthchecks: [\n"}
	p, err := syncMonitorsFile(ctx, cfg, st, log)
	if err != nil || p == nil || p.File != "MONITORS_YAML" || p.Applied || len(p.Errors) != 1 {
		t.Fatalf("syntax error: %+v %v", p, err)
	}
	if all, _ := st.ListMonitors(ctx); len(all) != 1 {
		t.Fatalf("the held file changed the monitors: %d", len(all))
	}
}

func TestUpgradeNotices(t *testing.T) {
	if n := upgradeNotices(nil, ""); len(n) != 0 {
		t.Fatalf("nothing to say: %q", n)
	}
	n := upgradeNotices([]string{"the pasted Uptimy check-in URL"}, "https://heartbeats.upti.my/abc")
	if len(n) != 2 || n[0] != "upgrade: removed the pasted Uptimy check-in URL" || !strings.Contains(n[1], "UPTIMY_HEARTBEAT_URL is no longer supported") || strings.Contains(n[1], "abc") {
		t.Fatalf("notices: %q", n)
	}
}
