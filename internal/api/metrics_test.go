package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetrics(t *testing.T) {
	c := newTestServer(t)
	c.login("admin", adminPassword)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer site.Close()
	// A quote in the name checks label escaping.
	if code, _ := c.do("POST", "/api/healthchecks", map[string]any{"name": `Shop "API"`, "check": map[string]any{"type": "http", "target": site.URL, "interval_seconds": 10}}); code != 201 {
		t.Fatalf("create healthcheck: %d", code)
	}
	if code, _ := c.do("POST", "/api/heartbeats", map[string]any{"name": "Backup", "heartbeat": map[string]any{"every_seconds": 3600, "grace_seconds": 300}}); code != 201 {
		t.Fatalf("create heartbeat: %d", code)
	}

	get := func(cl *client) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, c.base+"/metrics", nil)
		cl.authorize(req)
		resp, err := cl.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Without credentials: refused.
	if code, _ := get(&client{t: t, base: c.base, http: &http.Client{}}); code != 401 {
		t.Fatalf("anonymous /metrics: %d", code)
	}

	// A read-only API token is enough, as Prometheus would use.
	_, created := c.do("POST", "/api/auth/tokens", map[string]any{"name": "Prometheus", "read_only": true})
	prom := c.withToken(created["token"].(string))
	var body string
	waitFor(t, "the first check", func() bool {
		_, body = get(prom)
		return strings.Contains(body, `uptimy_agent_checks_total{id="1",name="Shop \"API\"",kind="healthcheck",type="http",source="ui",result="success"} 1`)
	})
	for _, want := range []string{
		`uptimy_agent_info{version="test"} 1`,
		"# TYPE uptimy_agent_checks_total counter",
		`uptimy_agent_monitor_status{id="1",name="Shop \"API\"",kind="healthcheck",type="http",source="ui",status="pending"} 0`,
		`uptimy_agent_check_duration_seconds{id="1"`,
		`uptimy_agent_heartbeat_runs_total{id="2",name="Backup",kind="heartbeat",type="heartbeat",source="ui",outcome="missed"} 0`,
		`uptimy_agent_heartbeat_next_due_timestamp_seconds{id="2"`,
		`uptimy_agent_heartbeat_running{id="2",name="Backup",kind="heartbeat",type="heartbeat",source="ui"} 0`,
		"uptimy_agent_go_goroutines ",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if strings.Contains(body, site.URL) {
		t.Error("metrics must not expose targets")
	}
}

func getMetrics(t *testing.T, c *client) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, c.base+"/metrics", nil)
	c.authorize(req)
	resp, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
