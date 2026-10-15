package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAlertDeliveries(t *testing.T) {
	c := newTestServer(t)
	c.login("admin", adminPassword)

	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
	defer down.Close()
	var hookStatus atomic.Int32
	hookStatus.Store(http.StatusNotFound)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(int(hookStatus.Load())) }))
	defer hook.Close()

	if code, body := c.do("POST", "/api/notifiers", map[string]any{"name": "Hook", "type": "webhook", "enabled": true, "config": map[string]any{"url": hook.URL}}); code != 201 {
		t.Fatalf("create channel: %d %v", code, body)
	}
	code, m := c.do("POST", "/api/healthchecks", map[string]any{"name": "Shop", "check": map[string]any{"type": "http", "target": down.URL, "interval_seconds": 3600, "failure_threshold": 1}})
	if code != 201 {
		t.Fatalf("create healthcheck: %d %v", code, m)
	}
	id := int64(m["id"].(float64))

	// The down event shows the delivery failing: the webhook is gone (404).
	var delivery map[string]any
	waitFor(t, "a failed delivery", func() bool {
		_, events := c.doList("GET", fmt.Sprintf("/api/monitors/%d/events", id))
		for _, e := range events {
			if ds, ok := e["deliveries"].([]any); ok && e["status"] == "down" && len(ds) == 1 {
				delivery = ds[0].(map[string]any)
				return delivery["state"] == "failed"
			}
		}
		return false
	})
	if delivery["notifier_name"] != "Hook" || !strings.Contains(delivery["detail"].(string), "404") {
		t.Fatalf("delivery: %v", delivery)
	}

	// The channel's health says so too.
	_, channels := c.doList("GET", "/api/notifiers")
	health := channels[0]["health"].(map[string]any)
	if health["failed"].(float64) != 1 || health["last"].(map[string]any)["state"] != "failed" {
		t.Fatalf("health: %v", health)
	}

	// Fixed: retry it.
	hookStatus.Store(http.StatusOK)
	retry := fmt.Sprintf("/api/alerts/%d/deliveries/%d/retry", int64(delivery["alert_id"].(float64)), int64(delivery["notifier_id"].(float64)))
	if code, body := c.do("POST", retry, nil); code != 204 {
		t.Fatalf("retry: %d %v", code, body)
	}
	waitFor(t, "the retry", func() bool {
		_, channels := c.doList("GET", "/api/notifiers")
		last, _ := channels[0]["health"].(map[string]any)["last"].(map[string]any)
		return last != nil && last["state"] == "sent"
	})
	if code, _ := c.do("POST", retry, nil); code != 409 {
		t.Fatalf("retrying a sent alert: %d, want 409", code)
	}

	body := getMetrics(t, c)
	for _, want := range []string{
		`uptimy_agent_alert_deliveries_total{channel_type="webhook",result="failed"} 1`,
		`uptimy_agent_alert_deliveries_total{channel_type="webhook",result="sent"} 1`,
		"uptimy_agent_alert_deliveries_pending 0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics: missing %s", want)
		}
	}
}
