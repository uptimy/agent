package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/config"
)

func connectedAgent(t *testing.T) (*client, *fakeUptimy, *fakeHeartbeat) {
	t.Helper()
	uptimy := newFakeUptimy(t)
	uptimy.keys["upt_agent1"] = "agent"
	c, _ := newTestServerWithConfig(t, config.Config{
		UptimyAppURL: "https://app.example", UptimyAPIURL: uptimy.URL, UptimyHeartbeatsURL: uptimy.URL,
		AgentName: "k8s-prod",
	})
	c.login("admin", adminPassword)
	state := start(t, c, uptimy)
	if code, body := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": state, "code": uptimy.approve("upt_agent1")}); code != 200 {
		t.Fatalf("connect: %d %v", code, body)
	}
	for _, hb := range uptimy.monitors {
		return c, uptimy, hb
	}
	t.Fatal("no heartbeat created")
	return nil, nil, nil
}

func TestHeartbeatAlertAfter(t *testing.T) {
	c, uptimy, hb := connectedAgent(t)

	// New connections alert after 5 minutes: 1m interval + 4m grace, so a
	// normal restart doesn't page anyone.
	if hb.interval != 60 || hb.grace != 240 {
		t.Fatalf("created with interval %d grace %d, want 60/240", hb.interval, hb.grace)
	}
	code, body := c.do("GET", "/api/uptimy/heartbeat/settings", nil)
	if code != 200 || body["available"] != true || body["alert_after_seconds"] != float64(300) || body["paused"] != false {
		t.Fatalf("settings: %d %v", code, body)
	}

	// Changing it writes the grace back to Uptimy, keeping everything else.
	code, body = c.do("PUT", "/api/uptimy/heartbeat/settings", map[string]any{"alert_after_seconds": 900})
	if code != 200 || body["alert_after_seconds"] != float64(900) || hb.grace != 840 {
		t.Fatalf("update: %d %v (grace %d)", code, body, hb.grace)
	}
	if hb.lastPut["name"] != "Uptimy Agent" || hb.lastPut["failureThreshold"] != float64(1) {
		t.Fatalf("update dropped other fields: %v", hb.lastPut)
	}
	if code, _ := c.do("PUT", "/api/uptimy/heartbeat/settings", map[string]any{"alert_after_seconds": 420}); code != 400 {
		t.Fatalf("unsupported delay accepted: %d", code)
	}

	// Uptimy is the source of truth: a change made there shows up here.
	uptimy.mu.Lock()
	hb.grace = 1740
	uptimy.mu.Unlock()
	if _, body := c.do("GET", "/api/uptimy/heartbeat/settings", nil); body["alert_after_seconds"] != float64(1800) {
		t.Fatalf("didn't read the grace from Uptimy: %v", body)
	}

	// Viewers can see the setting but not change it.
	c.do("POST", "/api/users", map[string]string{"username": "vic", "password": "temp-pass-1", "role": "viewer"})
	v := c.another()
	v.login("vic", "temp-pass-1")
	v.do("POST", "/api/auth/password", map[string]string{"current": "temp-pass-1", "new": "vics-password"})
	if code, _ := v.do("GET", "/api/uptimy/heartbeat/settings", nil); code != 200 {
		t.Fatalf("viewer read: %d", code)
	}
	if code, _ := v.do("PUT", "/api/uptimy/heartbeat/settings", map[string]any{"alert_after_seconds": 120}); code != 403 {
		t.Fatalf("viewer changed the setting: %d", code)
	}
}

func TestMaintenanceWindow(t *testing.T) {
	c, uptimy, hb := connectedAgent(t)
	c.do("PUT", "/api/uptimy/heartbeat/settings", map[string]any{"alert_after_seconds": 300})

	// A window stretches the grace in Uptimy rather than pausing, so Uptimy
	// still alerts if the agent never comes back.
	code, body := c.do("POST", "/api/uptimy/heartbeat/maintenance", map[string]any{"minutes": 60})
	if code != 200 || hb.grace != 3600 || hb.paused || body["maintenance_until"] == nil || body["normal_alert_after_seconds"] != float64(300) {
		t.Fatalf("start: %d %v (grace %d, paused %v)", code, body, hb.grace, hb.paused)
	}
	if code, _ := c.do("PUT", "/api/uptimy/heartbeat/settings", map[string]any{"alert_after_seconds": 900}); code != 409 {
		t.Fatalf("changed the delay during maintenance: %d", code)
	}
	if code, _ := c.do("POST", "/api/uptimy/heartbeat/maintenance", map[string]any{"minutes": 7}); code != 400 {
		t.Fatalf("unsupported window accepted: %d", code)
	}

	// Extending a running window keeps the original grace to restore.
	c.do("POST", "/api/uptimy/heartbeat/maintenance", map[string]any{"minutes": 240})
	if hb.grace != 14400 {
		t.Fatalf("extend: grace %d", hb.grace)
	}

	// Ending it early restores normal alerting.
	code, body = c.do("DELETE", "/api/uptimy/heartbeat/maintenance", nil)
	if code != 200 || hb.grace != 240 || body["maintenance_until"] != nil || body["alert_after_seconds"] != float64(300) {
		t.Fatalf("end: %d %v (grace %d)", code, body, hb.grace)
	}

	// A window that runs out is ended by the agent, retrying while Uptimy is down.
	c.do("POST", "/api/uptimy/heartbeat/maintenance", map[string]any{"minutes": 30})
	conn, _ := c.srv.Store.UptimyConnection(context.Background())
	past := time.Now().Add(-time.Minute)
	conn.MaintenanceUntil = &past
	c.srv.Store.UpdateUptimyConnection(context.Background(), *conn)

	uptimy.mu.Lock()
	uptimy.down = true
	uptimy.mu.Unlock()
	c.srv.expireMaintenance(context.Background())
	if hb.grace != 1800 {
		t.Fatalf("grace changed while Uptimy was down: %d", hb.grace)
	}
	uptimy.mu.Lock()
	uptimy.down = false
	uptimy.mu.Unlock()
	c.srv.expireMaintenance(context.Background())
	if hb.grace != 240 {
		t.Fatalf("expired window not restored: grace %d", hb.grace)
	}
	if conn, _ := c.srv.Store.UptimyConnection(context.Background()); conn.MaintenanceUntil != nil {
		t.Fatal("window still recorded after it ended")
	}
}

func TestPauseAlerts(t *testing.T) {
	c, uptimy, hb := connectedAgent(t)

	code, body := c.do("PUT", "/api/uptimy/heartbeat/settings", map[string]any{"paused": true})
	if code != 200 || body["paused"] != true || !hb.paused {
		t.Fatalf("pause: %d %v", code, body)
	}
	// Paused: the agent stops checking in instead of reporting Uptimy's 404s.
	before := uptimy.checkIns
	_, status := c.do("POST", "/api/uptimy/heartbeat/test", nil)
	if status["paused"] != true || status["last_error"] != nil || uptimy.checkIns != before {
		t.Fatalf("paused agent checked in or reported an error: %v (check-ins %d -> %d)", status, before, uptimy.checkIns)
	}
	// The pause survives a restart.
	if conn, _ := c.srv.Store.UptimyConnection(context.Background()); !conn.Paused {
		t.Fatal("pause not remembered")
	}

	// Resuming checks in right away.
	code, body = c.do("PUT", "/api/uptimy/heartbeat/settings", map[string]any{"paused": false})
	if code != 200 || body["paused"] != false || hb.paused || uptimy.checkIns != before+1 {
		t.Fatalf("resume: %d %v (check-ins %d)", code, body, uptimy.checkIns)
	}

	// A pause made in Uptimy itself is picked up too.
	uptimy.mu.Lock()
	hb.paused = true
	uptimy.mu.Unlock()
	c.do("GET", "/api/uptimy/heartbeat/settings", nil)
	if _, status := c.do("GET", "/api/uptimy/heartbeat", nil); status["paused"] != true {
		t.Fatalf("pause from Uptimy not picked up: %v", status)
	}
}

func TestHeartbeatSettingsWithoutAccount(t *testing.T) {
	uptimy := newFakeUptimy(t)
	c, _ := newTestServerWithConfig(t, config.Config{UptimyHeartbeatsURL: uptimy.URL})
	c.login("admin", adminPassword)
	// A pasted heartbeat URL gives the agent no access to the settings.
	c.do("PUT", "/api/uptimy/heartbeat", map[string]string{"url": uptimy.URL + "/v1/monitors/pub-manual"})
	code, body := c.do("GET", "/api/uptimy/heartbeat/settings", nil)
	if code != 200 || body["available"] != false {
		t.Fatalf("settings without an account: %d %v", code, body)
	}
	raw, _ := json.Marshal(body["alert_after_choices"])
	if string(raw) != "[2,5,15,30]" {
		t.Fatalf("choices: %s", raw)
	}
}
