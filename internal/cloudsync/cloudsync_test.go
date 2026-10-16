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
	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// fakeCloud serves the agent-key routes of upti.my-api, upti.my-heartbeat
// and upti.my-ingest from one server.
type fakeCloud struct {
	*httptest.Server
	mu           sync.Mutex
	interval     int
	statusChange int
	sample       int
	locationPuts int
	hcPuts       int
	locationName string
	// refuse: healthchecks with this name are refused (400).
	refuse string
	// reject: ingest rejects every item for this reason.
	reject string
	hbPuts int
	// failNext answers the next request whose "METHOD path prefix" matches
	// with this status (and an error), once.
	failNext     map[string]int
	healthchecks map[string]connect.MirroredHealthcheck // clientRef -> last PUT
	heartbeats   map[string]map[string]any              // uuid -> body
	deleted      []string
	uploads      []connect.Upload
	uploadStatus int // 0 = 202
	retryAfter   string
}

func newFakeCloud(t *testing.T) *fakeCloud {
	f := &fakeCloud{interval: 60, statusChange: 10, sample: 300, healthchecks: map[string]connect.MirroredHealthcheck{}, heartbeats: map[string]map[string]any{}, failNext: map[string]int{}}
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
	for prefix, status := range f.failNext {
		if strings.HasPrefix(r.Method+" "+path, prefix) {
			delete(f.failNext, prefix)
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":"failed on purpose"}`))
			return
		}
	}
	switch {
	case r.Method == http.MethodPut && path == "/v1/api/agent/location":
		f.locationPuts++
		f.locationName, _ = body["name"].(string)
		reply(http.StatusOK, map[string]any{"locationUuid": "loc-1", "uploadIntervalSeconds": f.interval,
			"statusChangeIntervalSeconds": f.statusChange, "sampleIntervalSeconds": f.sample, "withinPlan": true})
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
		if h.Name == f.refuse {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"VALIDATION.400","error":"type isn't supported"}`))
			return
		}
		f.healthchecks[ref] = h
		f.hcPuts++
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
			f.hbPuts++
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
		if f.uploadStatus == http.StatusConflict {
			f.uploadStatus = 0 // set up again, it takes uploads
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"code":"AGENT_LOCATION.409","error":"no location"}`))
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
		rejected := []map[string]any{}
		if f.reject != "" {
			for _, h := range u.Healthchecks {
				rejected = append(rejected, map[string]any{"uuid": h.HealthcheckUUID, "reason": f.reject})
			}
		}
		reply(http.StatusAccepted, map[string]any{"accepted": len(u.Healthchecks) + len(u.Heartbeats) - len(rejected), "rejected": rejected, "uploadIntervalSeconds": f.interval,
			"statusChangeIntervalSeconds": f.statusChange, "sampleIntervalSeconds": f.sample})
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

	// History from before connecting stays here: it would open incidents in
	// Cloud that are long over.
	old := e.now.Add(-time.Hour)
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: db.ID, Time: old, OK: false})
	if _, err := e.st.InsertRun(ctx, monitor.Run{MonitorID: hb.ID, FinishedAt: &old, Outcome: monitor.OutcomeFailure}); err != nil {
		t.Fatal(err)
	}
	if wait := e.s.cycle(ctx); wait != time.Minute {
		t.Fatalf("waits %s, want the upload interval", wait)
	}
	if len(e.cloud.uploads) != 1 || len(e.cloud.uploads[0].Healthchecks)+len(e.cloud.uploads[0].Heartbeats) != 0 {
		t.Fatalf("first upload %+v, want only the check-in", e.cloud.uploads)
	}

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

	e.s.cycle(ctx)

	conn, _ := e.st.UptimyConnection(ctx)
	if conn.LocationUUID != "loc-1" || conn.UploadIntervalSeconds != 60 || conn.StatusChangeIntervalSeconds != 10 || conn.SampleIntervalSeconds != 300 {
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

	if len(e.cloud.uploads) != 2 {
		t.Fatalf("%d uploads", len(e.cloud.uploads))
	}
	u := e.cloud.uploads[1]
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
	if len(e.cloud.uploads) != 3 || len(e.cloud.uploads[2].Healthchecks)+len(e.cloud.uploads[2].Heartbeats) != 0 {
		t.Fatalf("third upload %+v", e.cloud.uploads[2:])
	}
}

// Ingest drops a batch id it has seen as a duplicate: idle uploads, which
// only check the agent in, must not share one.
func TestIdleUploadsHaveTheirOwnBatchID(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.cycle(ctx)
	e.s.cycle(ctx)
	e.s.cycle(ctx)
	ids := map[string]bool{}
	for _, u := range e.cloud.uploads {
		if len(u.Healthchecks)+len(u.Heartbeats) != 0 || u.BatchID == "" {
			t.Fatalf("upload %+v, want an empty batch with an id", u)
		}
		ids[u.BatchID] = true
	}
	if len(e.cloud.uploads) != 3 || len(ids) != 3 {
		t.Fatalf("%d idle uploads with %d batch ids, want one id each", len(e.cloud.uploads), len(ids))
	}
}

// A run still running holds up no other heartbeat's runs, and is uploaded
// once, when it finishes (the same row, updated).
func TestRunningRunDoesNotHoldUpOthers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	backup := e.monitor(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "backup", Heartbeat: &monitor.Heartbeat{Token: "tok-backup-0123456789", EverySeconds: 3600}})
	sync := e.monitor(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "sync", Heartbeat: &monitor.Heartbeat{Token: "tok-sync-01234567890", EverySeconds: 60}})
	e.s.cycle(ctx)

	started := e.now.Add(-time.Minute)
	long, err := e.st.InsertRun(ctx, monitor.Run{MonitorID: backup.ID, StartedAt: &started, Outcome: monitor.OutcomeRunning})
	if err != nil {
		t.Fatal(err)
	}
	done := e.now
	if _, err := e.st.InsertRun(ctx, monitor.Run{MonitorID: sync.ID, FinishedAt: &done, Outcome: monitor.OutcomeSuccess}); err != nil {
		t.Fatal(err)
	}
	e.s.cycle(ctx)
	got := e.cloud.uploads[len(e.cloud.uploads)-1].Heartbeats
	if len(got) != 1 || got[0].HeartbeatUUID != e.mirror(t, sync.ID) {
		t.Fatalf("uploaded %+v, want sync's run while backup's still runs", got)
	}

	finished, dur := e.now.Add(time.Second), int64(61000)
	long.FinishedAt, long.Outcome, long.DurationMS = &finished, monitor.OutcomeSuccess, &dur
	if err := e.st.UpdateRun(ctx, long); err != nil {
		t.Fatal(err)
	}
	e.s.cycle(ctx)
	e.s.cycle(ctx)
	n := len(e.cloud.uploads)
	if got := e.cloud.uploads[n-2].Heartbeats; len(got) != 1 || got[0].HeartbeatUUID != e.mirror(t, backup.ID) || got[0].DurationMs != 61000 {
		t.Fatalf("uploaded %+v, want backup's run once it finished", got)
	}
	if got := e.cloud.uploads[n-1].Heartbeats; len(got) != 0 {
		t.Fatalf("uploaded %+v again", got)
	}
}

func (e *env) lastUpload() connect.Upload {
	e.cloud.mu.Lock()
	defer e.cloud.mu.Unlock()
	return e.cloud.uploads[len(e.cloud.uploads)-1]
}

func (e *env) mirror(t *testing.T, monitorID int64) string {
	t.Helper()
	cloud, err := e.st.CloudMonitors(context.Background())
	if err != nil || cloud[monitorID].CloudUUID == "" {
		t.Fatalf("monitor %d isn't mirrored (%v)", monitorID, err)
	}
	return cloud[monitorID].CloudUUID
}

// The scheduler records a status change after the check that caused it has
// finished: that result must already carry the new status.
func TestResultCarriesTheStatusItCaused(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx)
	started := e.now.Add(-time.Minute)
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: started, OK: false, LatencyMS: 3000})
	if _, err := e.st.InsertEvent(ctx, monitor.Event{MonitorID: api.ID, Time: started.Add(3050 * time.Millisecond), Status: monitor.StatusDown}); err != nil {
		t.Fatal(err)
	}
	e.s.cycle(ctx)
	if got := e.lastUpload().Healthchecks; len(got) != 1 || got[0].Status != "down" {
		t.Fatalf("uploaded %+v, want the result that took it down marked down", got)
	}
}

// Cloud shows the HTTP status of every result and why failures failed; a
// result that's up has no error class to send.
func TestUploadsCarryStatusCodeAndErrorClass(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx)
	at := e.now.Add(-2 * time.Minute)
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: at, OK: true, LatencyMS: 12, StatusCode: 200, ErrorClass: "stale"})
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: at.Add(time.Minute), LatencyMS: 40, Message: "unexpected status 503", StatusCode: 503, ErrorClass: "http_5xx"})
	if _, err := e.st.InsertEvent(ctx, monitor.Event{MonitorID: api.ID, Time: at.Add(time.Minute + time.Second), Status: monitor.StatusDown}); err != nil {
		t.Fatal(err)
	}
	e.s.cycle(ctx)
	got := e.lastUpload().Healthchecks
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
	e.s.cycle(ctx)
	start := e.now.Add(-13 * time.Minute)
	// Every 10s for 12 minutes, up but for one failure at 200s: one unchanged
	// result per 5-minute sample interval, and every status change.
	var want []string
	for i := 0; i < 72; i++ {
		at := start.Add(time.Duration(i) * 10 * time.Second)
		_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: at, OK: i != 20})
		if i == 0 || i == 20 || i == 21 || i == 51 {
			want = append(want, at.Format(time.TimeOnly))
		}
	}
	e.s.cycle(ctx)
	var got []string
	for _, h := range e.lastUpload().Healthchecks {
		got = append(got, h.CheckedAt.Format(time.TimeOnly))
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("uploaded results at %v, want %v (first, degraded, up again, 5 minutes later)", got, want)
	}
}

// A status change uploads no sooner than the status change interval after
// the last upload; an edit to monitors still uploads after the debounce.
func TestWatchPacesStatusChanges(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.cloud.statusChange = 120
	e.s.cycle(ctx) // uploads at e.now
	e.now = e.now.Add(30 * time.Second)

	waits := make(chan time.Duration, 10)
	fire := make(chan time.Time)
	e.s.after = func(d time.Duration) <-chan time.Time {
		waits <- d
		return fire
	}
	msgs := make(chan events.Message)
	go e.s.Watch(ctx, func() (<-chan events.Message, func()) { return msgs, func() {} })
	next := func() time.Duration {
		t.Helper()
		select {
		case d := <-waits:
			return d
		case <-time.After(5 * time.Second):
			t.Fatal("no timer started")
			return 0
		}
	}

	msgs <- events.Message{Type: "status"}
	if d := next(); d != 90*time.Second {
		t.Fatalf("status change waits %s, want 90s (120s after the last upload)", d)
	}
	msgs <- events.Message{Type: "status"} // already pending: no second timer
	msgs <- events.Message{Type: "monitors"}
	if d := next(); d != debounce {
		t.Fatalf("monitors edit waits %s, want the debounce", d)
	}
	msgs <- events.Message{Type: "status"} // later than the pending trigger
	fire <- time.Time{}
	select {
	case <-e.s.wake:
	case <-time.After(5 * time.Second):
		t.Fatal("no cycle triggered")
	}
	if len(waits) != 0 {
		t.Fatalf("%d extra timers started", len(waits))
	}

	// Long after the last upload, a status change only waits the debounce.
	e.now = e.now.Add(10 * time.Minute)
	msgs <- events.Message{Type: "status"}
	if d := next(); d != debounce {
		t.Fatalf("status change waits %s, want the debounce", d)
	}
}

// New status change and sample intervals from an upload are stored without
// setting the location up again; a new upload interval sets it up again (its
// check-in heartbeat checks in at that interval).
func TestUploadResponseUpdatesIntervals(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.cycle(ctx)

	e.cloud.statusChange, e.cloud.sample = 30, 600
	e.s.cycle(ctx)
	conn, _ := e.st.UptimyConnection(ctx)
	if conn.LocationUUID != "loc-1" || conn.StatusChangeIntervalSeconds != 30 || conn.SampleIntervalSeconds != 600 || e.cloud.locationPuts != 1 {
		t.Fatalf("connection %+v after %d set-ups, want the new intervals and one set-up", conn, e.cloud.locationPuts)
	}
	if e.s.statusEvery != 30*time.Second {
		t.Fatalf("status changes every %s, want 30s", e.s.statusEvery)
	}

	e.cloud.interval = 300
	e.s.cycle(ctx)
	if conn, _ := e.st.UptimyConnection(ctx); conn.LocationUUID != "" {
		t.Fatalf("connection %+v, want the location forgotten", conn)
	}
	e.s.cycle(ctx)
	conn, _ = e.st.UptimyConnection(ctx)
	if conn.UploadIntervalSeconds != 300 || e.cloud.locationPuts != 2 {
		t.Fatalf("connection %+v after %d set-ups, want 300s and a second set-up", conn, e.cloud.locationPuts)
	}
}

// A cycle works on the connection it loaded; saving what it changed must
// keep a setting changed in the meantime, such as pausing alerts.
func TestUpdateConnectionKeepsNewerSettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.cycle(ctx)
	stale, _ := e.st.UptimyConnection(ctx)

	paused, _ := e.st.UptimyConnection(ctx)
	paused.Paused = true
	if err := e.st.UpdateUptimyConnection(ctx, *paused); err != nil {
		t.Fatal(err)
	}
	if err := e.s.updateConnection(ctx, stale, func(c *connect.Connection) { c.SampleIntervalSeconds = 900 }); err != nil {
		t.Fatal(err)
	}
	conn, _ := e.st.UptimyConnection(ctx)
	if !conn.Paused || conn.SampleIntervalSeconds != 900 || stale.SampleIntervalSeconds != 900 {
		t.Fatalf("stored %+v, cycle's copy %+v", conn, stale)
	}
}

// An older Cloud sends only the upload interval.
func TestIntervalDefaults(t *testing.T) {
	upload, statusChange, sample := intervals(&connect.Connection{UploadIntervalSeconds: 60})
	if upload != time.Minute || statusChange != time.Minute || sample != defaultSampleInterval {
		t.Fatalf("intervals %s %s %s", upload, statusChange, sample)
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

// Results have no AUTOINCREMENT: once the newest are deleted, SQLite hands
// their rowids out again, and the cursor must not skip them.
func TestResultsCursorFollowsReusedRowids(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.monitor(t, healthcheck("old", "http", "http://old.local/health"))
	e.s.cycle(ctx)
	for i := 0; i < 3; i++ {
		_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: old.ID, Time: e.now.Add(time.Duration(i) * time.Second), OK: true})
	}
	e.s.cycle(ctx)
	if r, _, _, _ := e.st.SyncCursors(ctx); r != 3 {
		t.Fatalf("results cursor %d, want 3", r)
	}
	if err := e.st.DeleteMonitor(ctx, old.ID); err != nil { // its results go with it
		t.Fatal(err)
	}
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx) // mirrors api; nothing to upload
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: e.now.Add(time.Minute), OK: false})
	e.s.cycle(ctx)
	if last := e.cloud.uploads[len(e.cloud.uploads)-1]; len(last.Healthchecks) != 1 || last.Healthchecks[0].HealthcheckUUID != "uuid-hc-"+itoa(api.ID) {
		t.Fatalf("last upload %+v, want api's result (rowid 1 again)", last)
	}
}

// Cloud losing the agent's location (disconnected there) sets it up again
// and sends every mirror again, but no history Cloud already had: replaying
// it would open and resolve old incidents once more.
func TestLostLocationKeepsTheCursors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx)
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: e.now, OK: false})
	e.s.cycle(ctx)
	r0, n0, _, _ := e.st.SyncCursors(ctx)
	puts := e.cloud.hcPuts

	e.cloud.uploadStatus = http.StatusConflict
	e.s.cycle(ctx) // 409: the location is forgotten
	if conn, _ := e.st.UptimyConnection(ctx); conn.LocationUUID != "" {
		t.Fatalf("connection %+v, want the location forgotten", conn)
	}
	e.s.cycle(ctx)
	if e.cloud.locationPuts != 2 || e.cloud.hcPuts != puts+1 {
		t.Fatalf("%d set-ups and %d healthcheck PUTs, want the location and the mirror sent again", e.cloud.locationPuts, e.cloud.hcPuts-puts)
	}
	if r, n, ok, _ := e.st.SyncCursors(ctx); !ok || r != r0 || n != n0 {
		t.Fatalf("cursors %d/%d, want %d/%d kept", r, n, r0, n0)
	}
	if last := e.lastUpload(); len(last.Healthchecks) != 0 {
		t.Fatalf("uploaded %+v again", last.Healthchecks)
	}
}

// A failed update keeps the mirror as Cloud has it: its results still
// upload, and the next cycle sends the update again.
func TestFailedMirrorUpdateKeepsTheMirror(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx)
	before, _ := e.st.CloudMonitors(ctx)

	api.Check.Target = "http://api.local/healthz"
	if _, err := e.st.UpdateMonitor(ctx, api); err != nil {
		t.Fatal(err)
	}
	_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: e.now, OK: true})
	e.cloud.failNext["PUT /v1/api/agent/healthchecks/"] = http.StatusInternalServerError
	e.s.cycle(ctx)
	if after, _ := e.st.CloudMonitors(ctx); after[api.ID] != before[api.ID] {
		t.Fatalf("mirror %+v after a failed update, want %+v kept", after[api.ID], before[api.ID])
	}
	if got := e.lastUpload().Healthchecks; len(got) != 1 || got[0].HealthcheckUUID != before[api.ID].CloudUUID {
		t.Fatalf("uploaded %+v, want the result under the kept mirror", got)
	}
	e.s.cycle(ctx)
	if h := e.cloud.healthchecks["hc-"+itoa(api.ID)]; h.Target != "http://api.local/healthz" {
		t.Fatalf("mirror %+v, want the update sent again", h)
	}
}

// A heartbeat whose mirror couldn't be created yet keeps its runs: they
// upload once it is, so Cloud learns its status.
func TestRunsWaitForTheirMirror(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.cycle(ctx)
	backup := e.monitor(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "backup", Heartbeat: &monitor.Heartbeat{Token: "tok-backup-0123456789", EverySeconds: 3600}})
	if _, err := e.st.InsertRun(ctx, monitor.Run{MonitorID: backup.ID, DueAt: &e.now, Outcome: monitor.OutcomeMissed}); err != nil {
		t.Fatal(err)
	}
	e.cloud.failNext["POST /v1/api/heartbeat-monitors/"] = http.StatusInternalServerError
	e.s.cycle(ctx)
	if got := e.lastUpload().Heartbeats; len(got) != 0 {
		t.Fatalf("uploaded %+v without a mirror", got)
	}
	e.s.cycle(ctx)
	if got := e.lastUpload().Heartbeats; len(got) != 1 || got[0].Outcome != "missed" || got[0].HeartbeatUUID != e.mirror(t, backup.ID) {
		t.Fatalf("uploaded %+v, want the missed run once mirrored", got)
	}
}

// The full reconcile sends nothing that's unchanged and still in Cloud (an
// update resets a heartbeat's state there), re-creates what Cloud lost, and
// deletes what this agent doesn't know.
func TestFullReconcileSendsOnlyWhatsMissing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	backup := e.monitor(t, monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "backup", Heartbeat: &monitor.Heartbeat{Token: "tok-backup-0123456789", EverySeconds: 3600}})
	e.s.cycle(ctx)
	hcPuts, hbPuts := e.cloud.hcPuts, e.cloud.hbPuts

	e.now = e.now.Add(fullReconcileEvery)
	e.s.cycle(ctx)
	if e.cloud.hcPuts != hcPuts || e.cloud.hbPuts != hbPuts {
		t.Fatalf("full reconcile sent %d healthchecks and %d heartbeats again, want none", e.cloud.hcPuts-hcPuts, e.cloud.hbPuts-hbPuts)
	}

	e.cloud.mu.Lock()
	delete(e.cloud.healthchecks, "hc-"+itoa(api.ID))
	delete(e.cloud.heartbeats, e.mirror(t, backup.ID))
	e.cloud.heartbeats["hb-stray"] = map[string]any{"clientRef": "stray", "source": "agent"}
	e.cloud.mu.Unlock()
	e.now = e.now.Add(fullReconcileEvery)
	e.s.cycle(ctx)
	if _, ok := e.cloud.healthchecks["hc-"+itoa(api.ID)]; !ok {
		t.Fatal("the healthcheck Cloud lost wasn't sent again")
	}
	if _, ok := e.cloud.heartbeats[e.mirror(t, backup.ID)]; !ok {
		t.Fatal("the heartbeat Cloud lost wasn't created again")
	}
	if _, ok := e.cloud.heartbeats["hb-stray"]; ok {
		t.Fatal("a stray mirror wasn't deleted")
	}
}

// The location gets the agent's name and version when the agent starts and
// whenever they change, not only when it's set up.
func TestLocationGetsNameAndVersionOnStart(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.cycle(ctx)
	e.s.cycle(ctx)
	if e.cloud.locationPuts != 1 {
		t.Fatalf("%d location PUTs, want one", e.cloud.locationPuts)
	}

	// A restart: a new syncer on the same database.
	restarted := New(e.st, e.s.Client, "homelab-2", "0.3.1", e.s.Log)
	restarted.now = e.s.now
	restarted.cycle(ctx)
	restarted.cycle(ctx)
	if e.cloud.locationPuts != 2 || e.cloud.locationName != "homelab-2" {
		t.Fatalf("%d location PUTs, name %q, want one more with the new name", e.cloud.locationPuts, e.cloud.locationName)
	}
	if conn, _ := e.st.UptimyConnection(ctx); conn.LocationUUID != "loc-1" {
		t.Fatalf("connection %+v, want the location kept", conn)
	}
	// The status shows the stored intervals without setting up again.
	if st := restarted.Status(); st.UploadIntervalSeconds != 60 || st.LocationUUID != "loc-1" {
		t.Fatalf("status after a restart %+v, want the stored location and interval", st)
	}
	restarted.Version = "0.3.2"
	restarted.cycle(ctx)
	if e.cloud.locationPuts != 3 {
		t.Fatalf("%d location PUTs, want one more for the new version", e.cloud.locationPuts)
	}
}

// Cloud only shows a mirror's interval and timeout: they're sent within its
// limits rather than refused. So is the agent's name.
func TestMirrorsFitCloudsLimits(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.s.AgentName = strings.Repeat("n", 150)
	slow := healthcheck("slow", "http", "http://slow.local")
	slow.Check.IntervalSeconds, slow.Check.TimeoutSeconds = 172800, 7200
	slow = e.monitor(t, slow)
	e.s.cycle(ctx)
	if h := e.cloud.healthchecks["hc-"+itoa(slow.ID)]; h.IntervalSeconds != 86400 || h.TimeoutSeconds != 3600 {
		t.Fatalf("mirror %+v, want interval 86400 and timeout 3600", h)
	}
	if len(e.cloud.locationName) != 100 {
		t.Fatalf("location name of %d characters, want 100", len(e.cloud.locationName))
	}
}

// A monitor Cloud refuses shows in the status with why, and isn't sent
// again unchanged until the full reconcile.
func TestRefusedMonitorsShowInStatus(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.cloud.refuse = "odd"
	e.monitor(t, healthcheck("odd", "http", "http://odd.local"))
	e.monitor(t, healthcheck("long", "http", "http://long.local/"+strings.Repeat("a", 2100)))
	e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx)
	got := e.s.Status().NotSynced
	if len(got) != 2 || got[0].Name != "long" || !strings.Contains(got[0].Reason, "2048") || got[1].Name != "odd" || got[1].Reason != "type isn't supported" {
		t.Fatalf("not synced %+v", got)
	}
	if st := e.s.Status(); st.Healthchecks != 1 {
		t.Fatalf("status %+v, want api mirrored", st)
	}
	puts := e.cloud.hcPuts
	e.s.cycle(ctx)
	if e.cloud.hcPuts != puts || len(e.s.Status().NotSynced) != 2 {
		t.Fatalf("%d PUTs of refused monitors, status %+v", e.cloud.hcPuts-puts, e.s.Status())
	}
	e.cloud.refuse = ""
	e.now = e.now.Add(fullReconcileEvery)
	e.s.cycle(ctx)
	if got := e.s.Status().NotSynced; len(got) != 1 || got[0].Name != "long" {
		t.Fatalf("not synced %+v after the full reconcile, want only long", got)
	}
}

// Results Cloud rejects for their time are counted for the UI (the clock is
// off); ones for a mirror Cloud doesn't know make it send that mirror again.
func TestRejectedResults(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	api := e.monitor(t, healthcheck("api", "http", "http://api.local/health"))
	e.s.cycle(ctx)
	result := func(ok bool) {
		e.now = e.now.Add(10 * time.Minute) // past the sample interval
		_ = e.st.InsertResult(ctx, monitor.Result{MonitorID: api.ID, Time: e.now, OK: ok})
	}

	e.cloud.reject = connect.RejectedTooOld
	result(true)
	e.s.cycle(ctx)
	result(true)
	e.s.cycle(ctx)
	if st := e.s.Status(); st.RejectedTooOld != 2 || st.RejectedClockAhead != 0 {
		t.Fatalf("status %+v, want 2 rejected as too old", st)
	}
	e.cloud.reject = connect.RejectedClockAhead
	result(true)
	e.s.cycle(ctx)
	if st := e.s.Status(); st.RejectedTooOld != 0 || st.RejectedClockAhead != 1 {
		t.Fatalf("status %+v, want 1 rejected as ahead and the too-old count cleared", st)
	}

	e.cloud.reject = connect.RejectedUnknown
	result(false)
	puts := e.cloud.hcPuts
	e.s.cycle(ctx)
	e.cloud.reject = ""
	e.s.cycle(ctx)
	if e.cloud.hcPuts != puts+1 {
		t.Fatalf("%d PUTs after an unknown mirror, want it sent again once", e.cloud.hcPuts-puts)
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
