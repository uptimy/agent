package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKumaImport(t *testing.T) {
	c := newTestServer(t)
	c.login("admin", adminPassword)

	path := filepath.Join(t.TempDir(), "kuma.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE monitor (id INTEGER PRIMARY KEY, name TEXT, active BOOLEAN DEFAULT 1, interval INTEGER, url TEXT,
			type TEXT, hostname TEXT, port INTEGER, maxretries INTEGER DEFAULT 0, retry_interval INTEGER DEFAULT 0,
			upside_down BOOLEAN DEFAULT 0, accepted_statuscodes_json TEXT, method TEXT);
		CREATE TABLE notification (id INTEGER PRIMARY KEY, name TEXT, active BOOLEAN DEFAULT 1, config TEXT);
		CREATE TABLE monitor_notification (id INTEGER PRIMARY KEY, monitor_id INTEGER, notification_id INTEGER);
		CREATE TABLE monitor_group (id INTEGER PRIMARY KEY, monitor_id INTEGER, group_id INTEGER, weight INTEGER);
		CREATE TABLE "group" (id INTEGER PRIMARY KEY, name TEXT, weight INTEGER, status_page_id INTEGER);
		INSERT INTO "group" (id, name, weight, status_page_id) VALUES (1, 'Public site', 1, 1);
		INSERT INTO monitor_group (monitor_id, group_id, weight) VALUES (1, 1, 1);
		INSERT INTO monitor (id, name, type, url, interval) VALUES (1, 'Website', 'http', 'https://example.com', 60);
		INSERT INTO monitor (id, name, type, interval) VALUES (2, 'Cron', 'push', 300);
		INSERT INTO monitor (id, name, type, hostname, port) VALUES (3, 'Redis port', 'port', 'redis', 6379);
		INSERT INTO notification (id, name, config) VALUES (1, 'Team', '{"type":"discord","discordWebhookUrl":"https://discord.com/api/webhooks/1/x"}');
		INSERT INTO monitor_notification (monitor_id, notification_id) VALUES (1, 1);`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	file, _ := os.ReadFile(path)

	upload := func(contentType string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, c.base+"/api/import/kuma", bytes.NewReader(file))
		req.Header.Set("Content-Type", contentType)
		resp, err := c.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	// A cross-site form could send text/plain: refused.
	if resp := upload("text/plain"); resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("text/plain upload: %d", resp.StatusCode)
	}
	resp := upload("application/octet-stream")
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var plan map[string]any
	json.Unmarshal(raw, &plan)
	if resp.StatusCode != http.StatusOK || len(plan["monitors"].([]any)) != 3 || len(plan["notifiers"].([]any)) != 1 {
		t.Fatalf("plan: %d %s", resp.StatusCode, raw)
	}

	// Import everything but the Redis port.
	monitors := plan["monitors"].([]any)[:2]
	code, result := c.do("POST", "/api/import/kuma/apply", map[string]any{"sections": plan["sections"], "monitors": monitors, "notifiers": plan["notifiers"]})
	if code != 200 || len(result["monitors"].([]any)) != 2 || len(result["notifiers"].([]any)) != 1 {
		t.Fatalf("apply: %d %v", code, result)
	}
	_, notifiers := c.doList("GET", "/api/notifiers")
	if ids := notifiers[0]["monitor_ids"].([]any); notifiers[0]["all_monitors"] != false || len(ids) != 1 {
		t.Fatalf("routing: %v", notifiers[0])
	}
	_, heartbeats := c.doList("GET", "/api/heartbeats")
	if len(heartbeats) != 1 || heartbeats[0]["heartbeat"].(map[string]any)["every_seconds"] != float64(300) {
		t.Fatalf("heartbeat: %v", heartbeats)
	}

	// Kuma's status page group became a section, after the page's own, with
	// the website in it.
	_, page := c.do("GET", "/api/status-page", nil)
	sections := page["sections"].([]any)
	if len(sections) != 2 || sections[1].(map[string]any)["name"] != "Public site" {
		t.Fatalf("sections: %v", sections)
	}
	for _, m := range page["monitors"].([]any) {
		m := m.(map[string]any)
		if m["name"] == "Website" && (m["public"] != true || m["section"] != sections[1].(map[string]any)["id"]) {
			t.Fatalf("website on the page: %v", m)
		}
	}

	// Importing again skips what exists.
	if _, again := c.do("POST", "/api/import/kuma/apply", map[string]any{"monitors": monitors}); len(again["skipped"].([]any)) != 2 {
		t.Fatalf("second import: %v", again)
	}
}

func TestRailwayImport(t *testing.T) {
	const token = "railway-project-token-123"
	t.Setenv("RAILWAY_PROJECT_ID", "proj")
	t.Setenv("RAILWAY_ENVIRONMENT_ID", "prod")
	t.Setenv("RAILWAY_SERVICE_ID", "agent")
	railway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		json.NewDecoder(r.Body).Decode(&req)
		switch {
		case r.Header.Get("Project-Access-Token") != token:
			w.Write([]byte(`{"data":null,"errors":[{"message":"Project Token not found"}]}`))
		case strings.Contains(req.Query, "projectToken"):
			w.Write([]byte(`{"data":{"projectToken":{"projectId":"proj","environmentId":"prod"}}}`))
		case strings.Contains(req.Query, "serviceInstances"):
			w.Write([]byte(`{"data":{"project":{"name":"Shop"},"environment":{"name":"production","serviceInstances":{"edges":[
				{"node":{"serviceId":"api","serviceName":"API","environmentId":"prod","healthcheckPath":"/healthz","domains":{"serviceDomains":[{"domain":"api.up.railway.app","targetPort":8080}]}}},
				{"node":{"serviceId":"cron","serviceName":"Backup","environmentId":"prod","cronSchedule":"0 3 * * *","domains":{}}},
				{"node":{"serviceId":"agent","serviceName":"Agent","environmentId":"prod","domains":{}}}]}}}}`))
		case strings.Contains(req.Query, "privateNetworks"):
			w.Write([]byte(`{"data":{"privateNetworks":[{"publicId":"net","dnsName":"railway.internal"}]}}`))
		default:
			w.Write([]byte(`{"data":{"privateNetworkEndpoint":{"dnsName":"api"}}}`))
		}
	}))
	defer railway.Close()

	c := newTestServer(t)
	var logs bytes.Buffer
	c.srv.Log = slog.New(slog.NewTextHandler(&logs, nil))
	c.srv.RailwayURL = railway.URL
	c.login("admin", adminPassword)

	if code, body := c.do("POST", "/api/import/railway", map[string]string{"token": "wrong"}); code != 400 || body["error"] != "Railway didn't accept this project token" {
		t.Fatalf("wrong token: %d %v", code, body)
	}
	planWith := func() map[string]any {
		t.Helper()
		code, plan := c.do("POST", "/api/import/railway", map[string]string{"token": token})
		if raw, _ := json.Marshal(plan); code != 200 || strings.Contains(string(raw), token) {
			t.Fatalf("plan: %d %s", code, raw)
		}
		return plan
	}
	plan := planWith()
	services := plan["services"].([]any)
	if plan["same_project"] != true || len(services) != 2 || len(plan["skipped"].([]any)) != 1 {
		t.Fatalf("plan: %v", plan)
	}
	if target := services[0].(map[string]any)["monitor"].(map[string]any)["check"].(map[string]any)["target"]; target != "http://api.railway.internal:8080/healthz" {
		t.Fatalf("api target: %v", target)
	}

	code, result := c.do("POST", "/api/import/railway/apply", map[string]any{"services": services})
	if code != 200 || len(result["monitors"].([]any)) != 2 || len(result["skipped"].([]any)) != 0 {
		t.Fatalf("apply: %d %v", code, result)
	}
	_, heartbeats := c.doList("GET", "/api/heartbeats")
	if len(heartbeats) != 1 || heartbeats[0]["source_ref"] != "railway:prod/cron" || heartbeats[0]["source"] != "ui" {
		t.Fatalf("heartbeats: %v", heartbeats)
	}

	// An edit keeps the ref, so a renamed monitor is still known as imported.
	id := result["monitors"].([]any)[0].(map[string]any)["id"].(float64)
	if code, m := c.do("PUT", fmt.Sprintf("/api/healthchecks/%d", int64(id)), map[string]any{
		"name": "Shop API", "check": map[string]any{"type": "http", "target": "http://api.railway.internal:8080/"},
	}); code != 200 || m["source_ref"] != "railway:prod/api" {
		t.Fatalf("edit: %d %v", code, m)
	}
	if again := planWith(); len(again["services"].([]any)) != 0 || len(again["skipped"].([]any)) != 3 {
		t.Fatalf("second plan: %v", again)
	}
	if _, again := c.do("POST", "/api/import/railway/apply", map[string]any{"services": services}); len(again["skipped"].([]any)) != 2 {
		t.Fatalf("second import: %v", again)
	}

	c.do("POST", "/api/users", map[string]string{"username": "vic", "password": "temp-pass-1", "role": "viewer"})
	v := c.another()
	v.login("vic", "temp-pass-1")
	v.do("POST", "/api/auth/password", map[string]string{"current": "temp-pass-1", "new": "vics-password"})
	for _, path := range []string{"/api/import/railway", "/api/import/railway/apply"} {
		if code, _ := v.do("POST", path, map[string]string{"token": token}); code != 403 {
			t.Fatalf("viewer %s: %d", path, code)
		}
	}
	if strings.Contains(logs.String(), token) {
		t.Fatalf("the token was logged: %s", logs.String())
	}
}
