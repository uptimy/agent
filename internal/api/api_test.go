package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/checks"
	"github.com/uptimy/agent/internal/cloudsync"
	"github.com/uptimy/agent/internal/config"
	"github.com/uptimy/agent/internal/connect"
	"github.com/uptimy/agent/internal/delivery"
	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/filesync"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
	"github.com/uptimy/agent/internal/scheduler"
	"github.com/uptimy/agent/internal/store"
)

type client struct {
	t    *testing.T
	base string
	http *http.Client
	srv  *Server // for driving background work in tests
	// bearer, when set, authenticates with an API token instead of cookies.
	bearer string
}

// withToken is a client that uses an API token and no session.
func (c *client) withToken(token string) *client {
	return &client{t: c.t, base: c.base, http: &http.Client{}, srv: c.srv, bearer: token}
}

func (c *client) authorize(req *http.Request) {
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
}

func (c *client) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	data, _ := io.ReadAll(resp.Body)
	json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

// doList is do for endpoints that return a JSON array.
func (c *client) doList(method, path string) (int, []map[string]any) {
	c.t.Helper()
	req, _ := http.NewRequest(method, c.base+path, nil)
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out []map[string]any
	data, _ := io.ReadAll(resp.Body)
	json.Unmarshal(data, &out)
	return resp.StatusCode, out
}

func newTestServer(t *testing.T) *client {
	c, _ := newTestServerWithStore(t)
	return c
}

func newTestServerWithStore(t *testing.T) (*client, *store.Store) {
	t.Helper()
	return newTestServerWithConfig(t, config.Config{})
}

func newTestServerWithConfig(t *testing.T, cfg config.Config) (*client, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := events.NewHub()
	sender := notify.NewSender(log)
	alerts := delivery.New(st, sender, hub, log)
	sched := scheduler.New(st, checks.New(nil), alerts, hub, log)
	ctx, cancel := context.WithCancel(context.Background())
	alertsDone := make(chan struct{})
	go func() { alerts.Run(ctx, time.Second); close(alertsDone) }()
	t.Cleanup(func() { cancel(); sched.Wait(); <-alertsDone })
	if err := sched.Start(ctx); err != nil {
		t.Fatal(err)
	}
	syncer := cloudsync.New(st, func() *connect.Client {
		c := connect.NewClient(cfg.UptimyAPIURL, cfg.UptimyHeartbeatsURL)
		c.IngestURL = cfg.UptimyAPIURL // the fake Uptimy serves ingest too
		return c
	}, cfg.AgentName, "test", log)
	srv := &Server{
		Config: cfg, Version: "test",
		Store: st, Scheduler: sched, Sender: sender, Alerts: alerts, Hub: hub, Log: log,
		Sync: syncer,
	}
	ts := httptest.NewServer(srv.Handler(http.NotFoundHandler()))
	t.Cleanup(ts.Close)
	jar, _ := cookiejar.New(nil)
	if _, err := EnsureDefaultAdmin(context.Background(), st, "admin", adminPassword); err != nil {
		t.Fatal(err)
	}
	return &client{t: t, base: ts.URL, http: &http.Client{Jar: jar}, srv: srv}, st
}

const adminPassword = "correct horse"

// another returns a second browser (its own cookies) against the same server.
func (c *client) another() *client {
	jar, _ := cookiejar.New(nil)
	return &client{t: c.t, base: c.base, http: &http.Client{Jar: jar}, srv: c.srv}
}

func (c *client) login(username, password string) (int, map[string]any) {
	c.t.Helper()
	return c.do("POST", "/api/auth/login", map[string]string{"username": username, "password": password})
}

func TestAuthAndMonitorLifecycle(t *testing.T) {
	c := newTestServer(t)

	if code, body := c.do("GET", "/api/auth/state", nil); code != 200 || body["authenticated"] != false {
		t.Fatalf("state: %d %v", code, body)
	}
	if code, _ := c.do("GET", "/api/healthchecks", nil); code != 401 {
		t.Fatalf("unauthenticated list: %d", code)
	}
	if code, _ := c.login("admin", "wrong password"); code != 401 {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := c.login("nobody", adminPassword); code != 401 {
		t.Fatalf("unknown user: %d", code)
	}
	if code, body := c.login("ADMIN", adminPassword); code != 200 || body["user"].(map[string]any)["role"] != "admin" {
		t.Fatalf("login (usernames are case-insensitive): %d %v", code, body)
	}

	code, created := c.do("POST", "/api/healthchecks", map[string]any{
		"name": "Internal API", "check": map[string]any{"type": "tcp", "target": "api.default.svc:8080"},
	})
	if code != 201 || created["kind"] != "healthcheck" {
		t.Fatalf("create: %d %v", code, created)
	}
	id := strconv.Itoa(int(created["id"].(float64)))

	if code, body := c.do("POST", "/api/healthchecks", map[string]any{"name": "bad", "check": map[string]any{"type": "http", "target": "nope"}}); code != 400 {
		t.Fatalf("invalid healthcheck accepted: %d %v", code, body)
	}
	// A healthcheck isn't reachable as a heartbeat.
	if code, _ := c.do("GET", "/api/heartbeats/"+id, nil); code != 404 {
		t.Fatalf("healthcheck served as a heartbeat: %d", code)
	}

	// The public status page must never expose targets.
	req, _ := http.NewRequest(http.MethodGet, c.base+"/api/status", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || bytes.Contains(raw, []byte("api.default.svc")) {
		t.Fatalf("status page: %d %s", resp.StatusCode, raw)
	}

	if code, _ := c.do("POST", "/api/healthchecks/"+id+"/pause", map[string]bool{"paused": true}); code != 200 {
		t.Fatalf("pause: %d", code)
	}
	if code, _ := c.do("POST", "/api/healthchecks/"+id+"/check", nil); code != 409 {
		t.Fatalf("checked a paused healthcheck: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/healthchecks/"+id, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do("GET", "/api/healthchecks/"+id, nil); code != 404 {
		t.Fatalf("deleted healthcheck still found: %d", code)
	}

	c.do("POST", "/api/auth/logout", nil)
	if code, _ := c.do("GET", "/api/healthchecks", nil); code != 401 {
		t.Fatalf("list after logout: %d", code)
	}
	if code, _ := c.login("admin", adminPassword); code != 200 {
		t.Fatalf("login: %d", code)
	}
}

func TestRejectsNonJSONMutations(t *testing.T) {
	c := newTestServer(t)
	c.login("admin", adminPassword)
	req, _ := http.NewRequest(http.MethodPost, c.base+"/api/healthchecks", bytes.NewBufferString("name=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d", resp.StatusCode)
	}
}

func TestStatusPageShowsOnlyRealRecoveries(t *testing.T) {
	c, st := newTestServerWithStore(t)
	ctx := context.Background()
	m := createTCP(t, st, "API", "x:1", monitor.SourceUI)
	base := time.Now().Add(-time.Hour)
	for i, s := range []monitor.Status{monitor.StatusUp, monitor.StatusDown, monitor.StatusUp} {
		st.InsertEvent(ctx, monitor.Event{MonitorID: m.ID, Time: base.Add(time.Duration(i) * time.Minute), Status: s})
	}
	code, body := c.do("GET", "/api/status", nil)
	if code != 200 {
		t.Fatalf("status: %d", code)
	}
	events := body["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("want down + recovery only, got %v", events)
	}
	if events[0].(map[string]any)["status"] != "up" || events[1].(map[string]any)["status"] != "down" {
		t.Fatalf("unexpected order/status: %v", events)
	}
}

func TestDefaultAdmin(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	generated, err := EnsureDefaultAdmin(ctx, st, "admin", "")
	if err != nil || len(generated) < 12 {
		t.Fatalf("expected a generated password, got %q %v", generated, err)
	}
	u, _ := st.GetUserByUsername(ctx, "admin")
	if !u.IsAdmin() || !u.MustChangePassword {
		t.Fatalf("default admin should be an admin who must change the password: %+v", u)
	}
	if again, _ := EnsureDefaultAdmin(ctx, st, "admin", ""); again != "" {
		t.Fatal("second start must not create or print another password")
	}

	// Setting ADMIN_PASSWORD later resets it (the lost-password recovery path).
	if _, err := EnsureDefaultAdmin(ctx, st, "admin", "recovered-pass"); err != nil {
		t.Fatal(err)
	}
	u, _ = st.GetUserByUsername(ctx, "admin")
	if u.MustChangePassword {
		t.Fatal("env password should not require a change")
	}
	if _, err := EnsureDefaultAdmin(ctx, st, "admin", "short"); err == nil {
		t.Fatal("short ADMIN_PASSWORD accepted")
	}
}

func TestUsersAndRoles(t *testing.T) {
	admin := newTestServer(t)
	admin.login("admin", adminPassword)

	code, body := admin.do("POST", "/api/users", map[string]string{"username": "dana", "password": "temp-pass-1", "role": "viewer"})
	if code != 201 || body["must_change_password"] != true {
		t.Fatalf("create viewer: %d %v", code, body)
	}
	danaID := strconv.Itoa(int(body["id"].(float64)))
	if code, _ := admin.do("POST", "/api/users", map[string]string{"username": "Dana", "password": "temp-pass-1", "role": "viewer"}); code != 409 {
		t.Fatalf("duplicate username (case-insensitive) accepted: %d", code)
	}
	if code, _ := admin.do("POST", "/api/users", map[string]string{"username": "x y", "password": "temp-pass-1", "role": "viewer"}); code != 400 {
		t.Fatalf("invalid username accepted: %d", code)
	}
	if code, _ := admin.do("POST", "/api/users", map[string]string{"username": "eve", "password": "temp-pass-1", "role": "root"}); code != 400 {
		t.Fatalf("invalid role accepted: %d", code)
	}

	// The viewer must change the temporary password before doing anything.
	dana := admin.another()
	if code, body := dana.login("dana", "temp-pass-1"); code != 200 || body["user"].(map[string]any)["must_change_password"] != true {
		t.Fatalf("viewer login: %d %v", code, body)
	}
	if code, _ := dana.do("GET", "/api/healthchecks", nil); code != 403 {
		t.Fatalf("viewer used the app before changing the password: %d", code)
	}
	if code, _ := dana.do("POST", "/api/auth/password", map[string]string{"current": "temp-pass-1", "new": "temp-pass-1"}); code != 400 {
		t.Fatalf("reusing the temporary password accepted: %d", code)
	}
	if code, _ := dana.do("POST", "/api/auth/password", map[string]string{"current": "temp-pass-1", "new": "danas-own-pass"}); code != 200 {
		t.Fatalf("change password: %d", code)
	}

	// Anyone can sign their other devices out, viewers included.
	danaPhone := dana.another()
	danaPhone.login("dana", "danas-own-pass")
	if code, body := dana.do("GET", "/api/auth/sessions", nil); code != 200 || body["count"] != 2.0 {
		t.Fatalf("sessions: %d %v", code, body)
	}
	if code, _ := dana.do("POST", "/api/auth/sessions/sign-out-others", nil); code != 200 {
		t.Fatalf("sign out others: %d", code)
	}
	if code, _ := danaPhone.do("GET", "/api/healthchecks", nil); code != 401 {
		t.Fatalf("other device still signed in: %d", code)
	}
	if code, _ := dana.do("GET", "/api/healthchecks", nil); code != 200 {
		t.Fatalf("current device was signed out: %d", code)
	}

	// Viewers can read but not change anything, or see users.
	if code, _ := dana.do("GET", "/api/healthchecks", nil); code != 200 {
		t.Fatalf("viewer read: %d", code)
	}
	if code, _ := dana.do("POST", "/api/healthchecks", map[string]any{"name": "x", "check": map[string]any{"type": "tcp", "target": "a:1"}}); code != 403 {
		t.Fatalf("viewer created a monitor: %d", code)
	}
	if code, _ := dana.do("GET", "/api/users", nil); code != 403 {
		t.Fatalf("viewer listed users: %d", code)
	}

	// Credentials are hidden from viewers.
	admin.do("POST", "/api/notifiers", map[string]any{"name": "ops", "type": "slack", "enabled": true, "config": map[string]string{"url": "https://hooks.slack.com/services/SECRET"}})
	admin.do("POST", "/api/healthchecks", map[string]any{"name": "api", "check": map[string]any{"type": "http", "target": "https://x.test", "config": map[string]any{"headers": map[string]string{"Authorization": "Bearer SECRET"}}}})
	admin.do("POST", "/api/healthchecks", map[string]any{"name": "db", "check": map[string]any{"type": "postgres", "target": "postgres://app:SECRET@db/app"}})
	_, hb := admin.do("POST", "/api/heartbeats", map[string]any{"name": "cron", "heartbeat": map[string]any{"every_seconds": 3600}})
	token := hb["heartbeat"].(map[string]any)["token"].(string)
	for _, path := range []string{"/api/notifiers", "/api/healthchecks", "/api/heartbeats", "/api/heartbeats/" + strconv.Itoa(int(hb["id"].(float64)))} {
		req, _ := http.NewRequest(http.MethodGet, dana.base+path, nil)
		resp, err := dana.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || bytes.Contains(raw, []byte("SECRET")) || bytes.Contains(raw, []byte(token)) {
			t.Fatalf("%s leaked a credential to a viewer: %d %s", path, resp.StatusCode, raw)
		}
	}

	// Lockout guards.
	_, me := admin.do("GET", "/api/auth/state", nil)
	adminID := strconv.Itoa(int(me["user"].(map[string]any)["id"].(float64)))
	if code, _ := admin.do("DELETE", "/api/users/"+adminID, nil); code != 409 {
		t.Fatalf("admin deleted themselves: %d", code)
	}
	if code, _ := admin.do("PUT", "/api/users/"+adminID, map[string]string{"role": "viewer"}); code != 409 {
		t.Fatalf("admin demoted themselves: %d", code)
	}

	// Promote, reset password (ends their sessions, forces a change), delete.
	if code, body := admin.do("PUT", "/api/users/"+danaID, map[string]string{"role": "admin"}); code != 200 || body["role"] != "admin" {
		t.Fatalf("promote: %d %v", code, body)
	}
	if code, _ := admin.do("PUT", "/api/users/"+danaID, map[string]string{"password": "reset-pass-9"}); code != 200 {
		t.Fatalf("reset password: %d", code)
	}
	if code, _ := dana.do("GET", "/api/healthchecks", nil); code != 401 {
		t.Fatalf("session survived a password reset: %d", code)
	}
	if code, body := dana.login("dana", "reset-pass-9"); code != 200 || body["user"].(map[string]any)["must_change_password"] != true {
		t.Fatalf("login after reset: %d %v", code, body)
	}
	if code, _ := admin.do("DELETE", "/api/users/"+danaID, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := dana.do("GET", "/api/healthchecks", nil); code != 401 {
		t.Fatalf("deleted user still signed in: %d", code)
	}
	if code, body := dana.do("GET", "/api/auth/state", nil); code != 200 || body["authenticated"] != false {
		t.Fatalf("deleted user still authenticated: %v", body)
	}
}

func TestMonitorChangesArePublished(t *testing.T) {
	c, _ := newTestServerWithStore(t)
	c.login("admin", adminPassword)
	// Listen on the SSE stream the way the UI does.
	req, _ := http.NewRequest(http.MethodGet, c.base+"/api/events", nil)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	lines := make(chan string, 20)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				lines <- string(buf[:n])
			}
			if err != nil {
				close(lines)
				return
			}
		}
	}()
	<-lines // ": connected"

	c.do("POST", "/api/healthchecks", map[string]any{"name": "db", "check": map[string]any{"type": "tcp", "target": "127.0.0.1:1", "interval_seconds": 3600}})
	deadline := time.After(5 * time.Second)
	for {
		select {
		case chunk, ok := <-lines:
			if !ok {
				t.Fatal("stream closed")
			}
			if strings.Contains(chunk, `"type":"monitors"`) {
				return
			}
		case <-deadline:
			t.Fatal("no monitors event after creating a monitor")
		}
	}
}

func TestStatusPageConfig(t *testing.T) {
	c, st := newTestServerWithStore(t)
	c.login("admin", adminPassword)
	mk := func(name, source string) int64 {
		return createTCP(t, st, name, "internal-"+name+":5432", source).ID
	}
	api, db, cron := mk("api-prod-1", monitor.SourceUI), mk("prod-db-3", monitor.SourceUI), mk("backup", monitor.SourceFile)

	code, cfg := c.do("GET", "/api/status-page", nil)
	if code != 200 || cfg["title"] != "Service Status" || cfg["enabled"] != true || cfg["show_events"] != true {
		t.Fatalf("defaults come from config: %d %v", code, cfg)
	}

	save := map[string]any{
		"enabled": true, "title": "Acme status", "description": "Live status of Acme services.", "show_events": false,
		"sections": []map[string]any{{"id": "services", "name": "Services"}},
		"monitors": []map[string]any{
			{"id": db, "public": true, "label": "Database", "section": "services"},
			{"id": api, "public": true, "label": "API", "section": "services"},
			{"id": cron, "public": false, "label": ""}, // file-managed: taken off the page all the same
		},
	}
	if code, body := c.do("PUT", "/api/status-page", save); code != 200 {
		t.Fatalf("save: %d %v", code, body)
	}

	resp, err := http.Get(c.base + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var pub struct {
		Title, Description string
		Sections           []struct {
			Name     string
			Monitors []struct{ Name string }
		}
		Incidents []any
	}
	json.Unmarshal(raw, &pub)
	if pub.Title != "Acme status" || pub.Description != "Live status of Acme services." {
		t.Fatalf("title/description not applied: %s", raw)
	}
	// Without sections set up, everything is in the default one.
	if len(pub.Sections) != 1 || pub.Sections[0].Name != "Services" {
		t.Fatalf("want the default section, got %s", raw)
	}
	var names []string
	for _, m := range pub.Sections[0].Monitors {
		names = append(names, m.Name)
	}
	if strings.Join(names, ",") != "Database,API" {
		t.Fatalf("labels/order wrong, got %v", names)
	}
	if bytes.Contains(raw, []byte("internal-")) || bytes.Contains(raw, []byte("prod-db-3")) {
		t.Fatalf("status page leaks internal names or targets: %s", raw)
	}
	if len(pub.Incidents) != 0 {
		t.Fatal("events shown although show_events is off")
	}

	if code, _ := c.do("PUT", "/api/status-page", map[string]any{"enabled": true, "title": " ", "monitors": []any{}}); code != 400 {
		t.Fatalf("empty title accepted: %d", code)
	}
	c.do("PUT", "/api/status-page", map[string]any{"enabled": false, "title": "Acme status", "monitors": []any{}})
	if resp, _ := http.Get(c.base + "/api/status"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled status page still served: %d", resp.StatusCode)
	}

	c.do("POST", "/api/users", map[string]string{"username": "vic", "password": "temp-pass-1", "role": "viewer"})
	v := c.another()
	v.login("vic", "temp-pass-1")
	v.do("POST", "/api/auth/password", map[string]string{"current": "temp-pass-1", "new": "vics-password"})
	if code, _ := v.do("PUT", "/api/status-page", save); code != 403 {
		t.Fatalf("viewer changed the status page: %d", code)
	}
}

// createTCP stores a paused, public TCP healthcheck directly.
func createTCP(t *testing.T, st *store.Store, name, target, source string) monitor.Monitor {
	t.Helper()
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: name, Public: true, Paused: true, Source: source,
		Check: &monitor.Check{Type: monitor.TypeTCP, Target: target}}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := st.CreateMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestInfoMonitorsFileProblem(t *testing.T) {
	c := newTestServer(t)
	c.login("admin", adminPassword)
	if _, body := c.do("GET", "/api/info", nil); body["monitors_file_error"] != nil {
		t.Fatalf("no problem: %v", body["monitors_file_error"])
	}
	c.srv.MonitorsFileProblem = &filesync.Problem{File: "monitors.yaml", Errors: []string{"yaml: line 12: did not find expected key"}}
	_, body := c.do("GET", "/api/info", nil)
	p, _ := body["monitors_file_error"].(map[string]any)
	if p["file"] != "monitors.yaml" || p["applied"] != false || p["errors"].([]any)[0] != "yaml: line 12: did not find expected key" {
		t.Fatalf("problem: %v", body["monitors_file_error"])
	}
}
