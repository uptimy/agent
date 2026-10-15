package cloudsync

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/connect"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// fakeCloud serves the agent-key routes of upti.my-api, upti.my-heartbeat
// and upti.my-ingest from one server.
type fakeCloud struct {
	*httptest.Server
	mu           sync.Mutex
	interval     int
	healthchecks map[string]connect.MirroredHealthcheck // clientRef -> last PUT
	heartbeats   map[string]map[string]any              // uuid -> body
	deleted      []string
	uploads      []connect.Upload
	uploadStatus int // 0 = 202
	retryAfter   string
}

func newFakeCloud(t *testing.T) *fakeCloud {
	f := &fakeCloud{interval: 60, healthchecks: map[string]connect.MirroredHealthcheck{}, heartbeats: map[string]map[string]any{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeCloud) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer upt_agent" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	reply := func(status int, data any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}
	var body map[string]any
	raw, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(raw, &body)
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPut && path == "/v1/api/agent/location":
		reply(http.StatusOK, map[string]any{"locationUuid": "loc-1", "uploadIntervalSeconds": f.interval, "withinPlan": true})
	case r.Method == http.MethodDelete && path == "/v1/api/agent/location":
		w.WriteHeader(http.StatusNoContent)
	case strings.HasPrefix(path, "/v1/api/agent/healthchecks/"):
		ref := strings.TrimPrefix(path, "/v1/api/agent/healthchecks/")
		if r.Method == http.MethodDelete {
			delete(f.healthchecks, ref)
			f.deleted = append(f.deleted, ref)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var h connect.MirroredHealthcheck
		_ = json.Unmarshal(raw, &h)
		f.healthchecks[ref] = h
		reply(http.StatusOK, map[string]any{"clientRef": ref, "healthcheckUuid": "uuid-" + ref, "applicationUuid": "app-" + ref})
	case r.Method == http.MethodGet && path == "/v1/api/agent/healthchecks":
		var out []map[string]any
		for ref := range f.healthchecks {
			out = append(out, map[string]any{"clientRef": ref, "healthcheckUuid": "uuid-" + ref})
		}
		reply(http.StatusOK, map[string]any{"results": out})
	case r.Method == http.MethodPost && path == "/v1/api/heartbeat-monitors/":
		uuid := "hb-" + body["clientRef"].(string)
		if _, ok := f.heartbeats[uuid]; !ok {
			f.heartbeats[uuid] = body
		}
		reply(http.StatusCreated, map[string]any{"uuid": uuid, "clientRef": body["clientRef"], "source": body["source"]})
	case r.Method == http.MethodGet && path == "/v1/api/heartbeat-monitors/":
		var out []map[string]any
		for uuid, b := range f.heartbeats {
			out = append(out, map[string]any{"uuid": uuid, "clientRef": b["clientRef"], "source": b["source"]})
		}
		reply(http.StatusOK, map[string]any{"results": out})
	case strings.HasPrefix(path, "/v1/api/heartbeat-monitors/"):
		uuid := strings.TrimPrefix(path, "/v1/api/heartbeat-monitors/")
		b, ok := f.heartbeats[uuid]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case http.MethodGet:
			reply(http.StatusOK, b)
		case http.MethodPut:
			for k, v := range body {
				b[k] = v
			}
			reply(http.StatusOK, b)
		case http.MethodDelete:
			delete(f.heartbeats, uuid)
			w.WriteHeader(http.StatusNoContent)
		}
	case r.Method == http.MethodPost && path == "/v1/agent/results":
		if f.uploadStatus == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", f.retryAfter)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"BACKPRESSURE.429","error":"slow down"}`))
			return
		}
		if f.uploadStatus == http.StatusForbidden {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"code":"PLAN_LIMIT.403","error":"over plan"}`))
			return
		}
		var u connect.Upload
		_ = json.Unmarshal(raw, &u)
		f.uploads = append(f.uploads, u)
		reply(http.StatusAccepted, map[string]any{"accepted": len(u.Healthchecks) + len(u.Heartbeats), "uploadIntervalSeconds": f.interval})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

type env struct {
	st    *store.Store
	cloud *fakeCloud
	s     *Syncer
	now   time.Time
}

func newEnv(t *testing.T) *env {
	st, err := store.Open(filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e := &env{st: st, cloud: newFakeCloud(t), now: time.Now().UTC()}
	e.s = New(st, func() *connect.Client {
		c := connect.NewClient(e.cloud.URL, e.cloud.URL)
		c.IngestURL = e.cloud.URL
		return c
	}, "homelab", "0.3.0", slog.New(slog.NewTextHandler(io.Discard, nil)))
	e.s.now = func() time.Time { return e.now }
	if err := st.SaveUptimyConnection(context.Background(), connect.Connection{APIKey: "upt_agent", ConnectedAt: e.now}); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e *env) monitor(t *testing.T, m monitor.Monitor) monitor.Monitor {
	t.Helper()
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	m, err := e.st.CreateMonitor(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func healthcheck(name, typ, target string) monitor.Monitor {
	return monitor.Monitor{Kind: monitor.KindHealthcheck, Name: name, Check: &monitor.Check{Type: monitor.Type(typ), Target: target, IntervalSeconds: 30, TimeoutSeconds: 5}}
}

func TestFirstCycleSetsUpMirrorsAndUploads(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	db := e.monitor(t, healthcheck("db", "postgres", "postgres://app:s3cret@db:5432/app"))
	hb := e.monitor(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "backup", Heartbeat: &monitor.Heartbeat{Token: "tok-backup-0123456789", EverySeconds: 3600, GraceSeconds: 300}})

	at := e.now.Add(-10 * time.Minute)
	for i, ok := range []bool{true, false, false} {
		if err := e.st.InsertResult(ctx, monitor.Result{MonitorID: db.ID, Time: at.Add(time.Duration(i) * time.Minute), OK: ok, LatencyMS: 4, Message: "refused"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.st.InsertEvent(ctx, monitor.Event{MonitorID: db.ID, Time: at.Add(2 * time.Minute), Status: monitor.StatusDown}); err != nil {
		t.Fatal(err)
	}
	finished, dur := at.Add(3*time.Minute), int64(4200)
	if _, err := e.st.InsertRun(ctx, monitor.Run{MonitorID: hb.ID, FinishedAt: &finished, Outcome: monitor.OutcomeFailure, DurationMS: &dur, Message: "exit 1"}); err != nil {
		t.Fatal(err)
	}

	if wait := e.s.cycle(ctx); wait != time.Minute {
		t.Fatalf("waits %s, want the upload interval", wait)
	}

	conn, _ := e.st.UptimyConnection(ctx)
	if conn.LocationUUID != "loc-1" || conn.UploadIntervalSeconds != 60 {
		t.Fatalf("connection %+v", conn)
	}
	installID, _ := e.st.InstallID(ctx)
	liveness := e.cloud.heartbeats["hb-"+installID]
	if liveness == nil || liveness["source"] != "agent_liveness" {
		t.Fatalf("liveness heartbeat %v", liveness)
	}
	mirror := e.cloud.heartbeats["hb-"+installID+":hb-"+itoa(hb.ID)]
	if mirror == nil || mirror["source"] != "agent" || mirror["intervalSeconds"] != float64(3600) {
		t.Fatalf("heartbeat mirror %v", mirror)
	}
	h := e.cloud.healthchecks["hc-"+itoa(db.ID)]
	if h.Type != "postgres" || strings.Contains(h.Target, "s3cret") {
		t.Fatalf("healthcheck mirror %+v (the password must not leave the agent)", h)
	}

	if len(e.cloud.uploads) != 1 {
		t.Fatalf("%d uploads", len(e.cloud.uploads))
	}
	u := e.cloud.uploads[0]
	var statuses []string
	for _, i := range u.Healthchecks {
		statuses = append(statuses, i.Status)
	}
	if strings.Join(statuses, ",") != "up,degraded,down" {
		t.Fatalf("statuses %v, want up, degraded (failing, under the threshold), down", statuses)
	}
	if len(u.Heartbeats) != 1 || u.Heartbeats[0].Outcome != "failed" || u.Heartbeats[0].DurationMs != 4200 {
		t.Fatalf("heartbeat outcomes %+v", u.Heartbeats)
	}

	// Nothing new: the next cycle still uploads (the agent's check-in), empty.
	e.s.cycle(ctx)
	if len(e.cloud.uploads) != 2 || len(e.cloud.uploads[1].Healthchecks)+len(e.cloud.uploads[1].Heartbeats) != 0 {
		t.Fatalf("second upload %+v", e.cloud.uploads[1:])
	}
}

// The scheduler records a status change after the check that caused it has
// finished: that result must already carry the new status.
func TestResultCarriesTheStatusItCaused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	started := e.now.Add(-time.Minute)
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: started, OK: false, LatencyMS: 3000})
	if _, err := e.st.InsertEvent(ctx, monitor.Event{MonitorID: api.ID, Time: started.Add(3050 * time.Millisecond), Status: monitor.StatusDown}); err != nil {
		t.Fatal(err)
	}
	e.s.cycle(ctx)
	if got := e.cloud.uploads[0].Healthchecks; len(got) != 1 || got[0].Status != "down" {
		t.Fatalf("uploaded %+v, want the result that took it down marked down", got)
	}
}

// Cloud shows the HTTP status of every result and why failures failed; a
// result that's up has no error class to send.
func TestUploadsCarryStatusCodeAndErrorClass(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	at := e.now.Add(-2 * time.Minute)
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: at, OK: true, LatencyMS: 12, StatusCode: 200, ErrorClass: "stale"})
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: at.Add(time.Minute), LatencyMS: 40, Message: "unexpected status 503", StatusCode: 503, ErrorClass: "http_5xx"})
	if _, err := e.st.InsertEvent(ctx, monitor.Event{MonitorID: api.ID, Time: at.Add(time.Minute + time.Second), Status: monitor.StatusDown}); err != nil {
		t.Fatal(err)
	}
	e.s.cycle(ctx)
	got := e.cloud.uploads[0].Healthchecks
	if len(got) != 2 {
		t.Fatalf("uploaded %+v", got)
	}
	if got[0].StatusCode != 200 || got[0].ErrorClass != "" {
		t.Fatalf("up result %+v, want status 200 and no error class", got[0])
	}
	if got[1].StatusCode != 503 || got[1].ErrorClass != "http_5xx" {
		t.Fatalf("failed result %+v, want status 503 and http_5xx", got[1])
	}
	raw, _ := json.Marshal(got[1])
	if !strings.Contains(string(raw), `"statusCode":503`) || !strings.Contains(string(raw), `"errorClass":"http_5xx"`) {
		t.Fatalf("wire format %s", raw)
	}
}

func TestUnchangedResultsAreThinned(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	start := e.now.Add(-5 * time.Minute)
	// Every 10s for 3 minutes, all up: one per 60s interval is enough.
	for i := 0; i < 18; i++ {
		_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: start.Add(time.Duration(i) * 10 * time.Second), OK: true})
	}
	e.s.cycle(ctx)
	if n := len(e.cloud.uploads[0].Healthchecks); n != 3 {
		t.Fatalf("%d results uploaded, want 3 (one per minute)", n)
	}
}

func TestDeletedMonitorsMirrorIsDeleted(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx)
	if err := e.st.DeleteMonitor(ctx, api.ID); err != nil {
		t.Fatal(err)
	}
	e.s.cycle(ctx)
	if len(e.cloud.deleted) != 1 || e.cloud.deleted[0] != "hc-"+itoa(api.ID) {
		t.Fatalf("deleted %v", e.cloud.deleted)
	}
}

func TestBackpressureAndPlanLimitKeepTheCursor(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx) // set up
	r0, n0, _, _ := e.st.SyncCursors(ctx)
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: e.now, OK: false})

	e.cloud.uploadStatus, e.cloud.retryAfter = http.StatusTooManyRequests, "30"
	if wait := e.s.cycle(ctx); wait != 30*time.Second {
		t.Fatalf("waits %s after a 429, want Retry-After", wait)
	}
	if r, n, _, _ := e.st.SyncCursors(ctx); r != r0 || n != n0 {
		t.Fatal("the cursor moved although nothing was stored")
	}

	e.cloud.uploadStatus = http.StatusForbidden
	e.s.cycle(ctx)
	if !e.s.Status().OverPlan {
		t.Fatalf("status %+v, want over plan", e.s.Status())
	}

	e.cloud.uploadStatus = 0
	e.s.cycle(ctx)
	last := e.cloud.uploads[len(e.cloud.uploads)-1]
	if len(last.Healthchecks) != 1 || e.s.Status().OverPlan {
		t.Fatalf("after recovering: %+v, status %+v", last, e.s.Status())
	}
}

func TestDisconnectRemovesEverythingInCloud(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.monitor(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "backup", Heartbeat: &monitor.Heartbeat{Token: "tok-backup-0123456789", EverySeconds: 3600}})
	e.s.cycle(ctx)
	conn, _ := e.st.UptimyConnection(ctx)
	if err := e.s.Disconnect(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if len(e.cloud.heartbeats) != 0 {
		t.Fatalf("heartbeats left in Cloud: %v", e.cloud.heartbeats)
	}
	if mirrors, _ := e.st.CloudMonitors(ctx); len(mirrors) != 0 {
		t.Fatalf("mirrors left locally: %v", mirrors)
	}
	if _, _, ok, _ := e.st.SyncCursors(ctx); ok {
		t.Fatal("cursors kept")
	}
}

func TestAgentLimitStopsSetUp(t *testing.T) {
	e := newEnv(t)
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"AGENT_LIMIT.403","error":"Your plan allows 1 connected agent."}`))
	}))
	defer limited.Close()
	e.s.Client = func() *connect.Client { return connect.NewClient(limited.URL, limited.URL) }
	conn, _ := e.st.UptimyConnection(context.Background())
	if err := e.s.SetUp(context.Background(), conn); !errors.Is(err, connect.ErrAgentLimit) {
		t.Fatalf("err %v, want ErrAgentLimit", err)
	}
	if !e.s.Status().OverPlan {
		t.Fatalf("status %+v, want over plan", e.s.Status())
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
