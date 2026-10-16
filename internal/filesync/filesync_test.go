package filesync

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

const doc = `
healthchecks:
  - name: API
    type: http
    target: https://api.example.com/health
    interval: 30s
    config:
      keyword: ok
  - name: Postgres
    type: tcp
    target: postgres.railway.internal:5432
heartbeats:
  - name: Nightly backup
    cron: "0 3 * * *"
    timezone: Europe/Berlin
    grace: 30m
  - name: Queue worker
    every: 5m
    token: pinned-token-for-the-worker
`

func TestParse(t *testing.T) {
	ds, err := Parse([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 4 {
		t.Fatalf("got %d monitors", len(ds))
	}
	api := ds[0]
	if api.Kind != monitor.KindHealthcheck || api.Check.IntervalSeconds != 30 || api.Check.Config.Keyword != "ok" || api.Public || api.Source != monitor.SourceFile {
		t.Fatalf("unexpected healthcheck %+v", api.Monitor)
	}
	backup, worker := ds[2], ds[3]
	if backup.Kind != monitor.KindHeartbeat || backup.Heartbeat.Cron != "0 3 * * *" || backup.Heartbeat.GraceSeconds != 1800 || !backup.GeneratedToken {
		t.Fatalf("unexpected heartbeat %+v", backup.Heartbeat)
	}
	if worker.Heartbeat.EverySeconds != 300 || worker.Heartbeat.Token != "pinned-token-for-the-worker" || worker.GeneratedToken {
		t.Fatalf("unexpected heartbeat %+v", worker.Heartbeat)
	}

	for name, bad := range map[string]string{
		"duplicate name": "healthchecks:\n  - {name: a, type: tcp, target: x:1}\nheartbeats:\n  - {name: a, every: 1h}\n",
		"no schedule":    "heartbeats:\n  - {name: a}\n",
		"bad duration":   "heartbeats:\n  - {name: a, every: often}\n",
		"unknown type":   "healthchecks:\n  - {name: a, type: icmp, target: x}\n",
		"invalid cron":   "heartbeats:\n  - {name: a, cron: daily}\n",
		"unknown key":    "healthchecks:\n  - {name: a, type: tcp, target: x:1, intervall: 30s}\n",
		"public":         "healthchecks:\n  - {name: a, type: tcp, target: x:1, public: true}\n",
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSync(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ui := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "Made in UI", Check: &monitor.Check{Type: monitor.TypeTCP, Target: "x:1"}}
	if err := ui.Normalize(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMonitor(ctx, ui); err != nil {
		t.Fatal(err)
	}

	ds, _ := Parse([]byte(doc))
	if c, u, d, err := Sync(ctx, st, ds); err != nil || c != 4 || u != 0 || d != 0 {
		t.Fatalf("first sync: %d %d %d %v", c, u, d, err)
	}
	backup := find(t, st, "Nightly backup")

	// Starting again with the same file writes nothing, and a heartbeat
	// without a pinned token keeps its ping URL.
	ds, _ = Parse([]byte(doc))
	if c, u, d, err := Sync(ctx, st, ds); err != nil || c+u+d != 0 {
		t.Fatalf("unchanged file: %d %d %d %v", c, u, d, err)
	}
	if again := find(t, st, "Nightly backup"); again.Heartbeat.Token != backup.Heartbeat.Token || !again.UpdatedAt.Equal(backup.UpdatedAt) {
		t.Fatalf("an unchanged heartbeat was rewritten: %+v", again)
	}

	// Adding a file monitor to the status page sticks: the file doesn't own
	// that.
	api := find(t, st, "API")
	page, _ := st.StatusPage(ctx)
	if err := st.SaveStatusPage(ctx, page, []store.StatusPageEntry{{ID: api.ID, Public: true, Label: "Public API", Section: "services"}}); err != nil {
		t.Fatal(err)
	}
	ds, _ = Parse([]byte(doc))
	if c, u, d, err := Sync(ctx, st, ds); err != nil || c+u+d != 0 {
		t.Fatalf("after status page edit: %d %d %d %v", c, u, d, err)
	}
	if api = find(t, st, "API"); !api.Public || api.StatusLabel != "Public API" {
		t.Fatalf("status page placement lost: %+v", api)
	}

	// Drop Postgres and change the API: one deleted, one updated.
	ds[0].Check.IntervalSeconds = 60
	if c, u, d, err := Sync(ctx, st, append(ds[:1:1], ds[2:]...)); err != nil || c != 0 || u != 1 || d != 1 {
		t.Fatalf("second sync: %d %d %d %v", c, u, d, err)
	}
	all, _ := st.ListMonitors(ctx)
	if len(all) != 4 { // API, two heartbeats, and the one from the UI
		t.Fatalf("expected 4 monitors, got %d", len(all))
	}
}

func find(t *testing.T, st *store.Store, name string) monitor.Monitor {
	t.Helper()
	all, err := st.ListMonitors(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range all {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("no monitor named %q", name)
	return monitor.Monitor{}
}

// The example file documents every check type and heartbeats; keep it valid.
func TestExampleFileParses(t *testing.T) {
	data, err := os.ReadFile("../../examples/monitors.yaml")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[monitor.Type]bool{}
	heartbeats := 0
	for _, d := range ds {
		if d.Check != nil {
			seen[d.Check.Type] = true
		} else {
			heartbeats++
		}
	}
	for _, ct := range monitor.CheckTypes() {
		if !seen[ct.Type] {
			t.Errorf("examples/monitors.yaml has no %s healthcheck", ct.Type)
		}
	}
	if heartbeats < 2 {
		t.Error("examples/monitors.yaml should show heartbeats with an interval and with cron")
	}
}

// A file with mistakes never stops the agent: it applies what it can.
func TestApply(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if res, err := Apply(ctx, st, []byte(doc)); err != nil || res.Created != 4 || len(res.Problems) > 0 {
		t.Fatalf("first apply: %+v %v", res, err)
	}
	api := find(t, st, "API")

	// A syntax error holds the whole file back: nothing changes.
	res, err := Apply(ctx, st, []byte("healthchecks:\n  - name: API\n   type: http\n"))
	if err != nil || !res.Held || len(res.Problems) != 1 || res.Created+res.Updated+res.Deleted != 0 {
		t.Fatalf("syntax error: %+v %v", res, err)
	}
	// So do a misspelled setting and two monitors with one name.
	for _, bad := range []string{
		"healthchecks:\n  - {name: API, type: tcp, target: x:1, intervall: 30s}\n",
		"healthchecks:\n  - {name: a, type: tcp, target: x:1}\n  - {name: a, type: tcp, target: x:2}\n",
	} {
		if res, err := Apply(ctx, st, []byte(bad)); err != nil || !res.Held {
			t.Fatalf("%q: %+v %v", bad, res, err)
		}
	}
	if all, _ := st.ListMonitors(ctx); len(all) != 4 {
		t.Fatalf("a held file changed the monitors: %d", len(all))
	}

	// An invalid monitor is skipped and keeps its last good settings; the
	// valid ones are applied; a new invalid one isn't created.
	edited := `
healthchecks:
  - name: API
    type: http
    target: https://api.example.com/health
    interval: 5s
  - name: Postgres
    type: tcp
    target: postgres.railway.internal:5433
  - name: New but broken
    type: icmp
    target: x
heartbeats:
  - name: Nightly backup
    cron: "0 3 * * *"
    timezone: Europe/Berlin
    grace: 30m
`
	res, err = Apply(ctx, st, []byte(edited))
	if err != nil || res.Held || len(res.Problems) != 2 || res.Created != 0 || res.Updated != 1 || res.Deleted != 1 {
		t.Fatalf("invalid monitors: %+v %v", res, err)
	}
	if got := find(t, st, "API"); got.Check.IntervalSeconds != api.Check.IntervalSeconds || !got.UpdatedAt.Equal(api.UpdatedAt) {
		t.Fatalf("the invalid monitor changed: %+v", got.Check)
	}
	if got := find(t, st, "Postgres"); got.Check.Target != "postgres.railway.internal:5433" {
		t.Fatalf("a valid monitor wasn't applied: %+v", got.Check)
	}
	all, _ := st.ListMonitors(ctx)
	if len(all) != 3 { // API, Postgres, Nightly backup; Queue worker deleted
		t.Fatalf("expected 3 monitors, got %d", len(all))
	}
}

// A database target whose variable is set to something that isn't a URL
// doesn't reject the file: that check fails when it runs.
func TestApplyInvalidVariable(t *testing.T) {
	t.Setenv("FILESYNC_TEST_PG_URL", "not a url")
	ds, err := Parse([]byte("healthchecks:\n  - {name: DB, type: postgres, target: \"${FILESYNC_TEST_PG_URL}\"}\n"))
	if err != nil || len(ds) != 1 {
		t.Fatalf("got %v, %v", ds, err)
	}
}
