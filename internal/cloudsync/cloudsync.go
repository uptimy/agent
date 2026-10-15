// Package cloudsync mirrors the agent's monitors to Uptimy Cloud and uploads
// their results (AGENT_CLOUD_SYNC_PLAN.md in the uptimy repo).
//
// The agent stays the source of truth: it defines, runs and judges its
// monitors and alerts on its own. Cloud gets a read-only copy of each
// monitor, every status change, and a thinned history (at most one result per
// monitor per upload interval, the plan's check interval). Each upload also
// checks the agent in: Cloud alerts when uploads stop.
package cloudsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/uptimy/agent/internal/connect"
	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

const (
	// Upload batches: what one request may carry (ingest allows 500).
	maxItems = 400
	// Results read per batch before thinning.
	scanRows = 2000
	// Ingest drops items older than 24h; skip them here.
	maxAge = 23 * time.Hour
	// A full reconcile against Cloud's own list catches mirrors that drifted.
	fullReconcileEvery = 10 * time.Minute
	// Back-to-back batches while catching up, before waiting a cycle.
	maxBatchesPerCycle = 20
	defaultInterval    = 5 * time.Minute
	retryInterval      = time.Minute
	// statusSlack covers the moment between a result being saved and the
	// status change it causes; far shorter than any check interval.
	statusSlack = time.Second
)

// Status is what the UI shows about the sync.
type Status struct {
	Active                bool       `json:"active"`
	LocationUUID          string     `json:"location_uuid,omitempty"`
	UploadIntervalSeconds int        `json:"upload_interval_seconds,omitempty"`
	LastUploadAt          *time.Time `json:"last_upload_at,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	// OverPlan: the workspace's plan has no room for this agent, so Cloud
	// doesn't take its results.
	OverPlan     bool `json:"over_plan,omitempty"`
	Healthchecks int  `json:"healthchecks"`
	Heartbeats   int  `json:"heartbeats"`
}

// Syncer runs the sync for the agent's "Connect to Uptimy Cloud" connection.
type Syncer struct {
	Store     *store.Store
	Client    func() *connect.Client
	AgentName string
	Version   string
	Log       *slog.Logger

	wake chan struct{}
	now  func() time.Time

	mu       sync.Mutex
	status   Status
	lastFull time.Time
	// lastSent thins uploads: per monitor, when its last result was sent and
	// with which status.
	lastSent map[int64]sent
}

type sent struct {
	at     time.Time
	status string
}

// New returns a syncer; Run starts it.
func New(st *store.Store, client func() *connect.Client, agentName, version string, log *slog.Logger) *Syncer {
	return &Syncer{
		Store: st, Client: client, AgentName: agentName, Version: version, Log: log,
		wake: make(chan struct{}, 1), now: time.Now, lastSent: map[int64]sent{},
	}
}

// Trigger runs a cycle now, e.g. after a monitor changed or a connection was made.
func (s *Syncer) Trigger() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Watch runs a cycle shortly after monitors change or a monitor's status
// changes, so edits reach Cloud in seconds and a status change opens or
// resolves the Cloud incident without waiting for the next upload.
func (s *Syncer) Watch(ctx context.Context, subscribe func() (<-chan events.Message, func())) {
	msgs, unsubscribe := subscribe()
	defer unsubscribe()
	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			if (m.Type == "monitors" || m.Type == "status") && debounce == nil {
				debounce = time.After(5 * time.Second)
			}
		case <-debounce:
			debounce = nil
			s.Trigger()
		}
	}
}

// Status returns the sync's state.
func (s *Syncer) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

func (s *Syncer) update(fn func(*Status)) {
	s.mu.Lock()
	fn(&s.status)
	s.mu.Unlock()
}

// Run syncs until ctx is canceled.
func (s *Syncer) Run(ctx context.Context) {
	for {
		wait := s.cycle(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(wait):
		}
	}
}

// cycle does one round and returns how long to wait before the next.
func (s *Syncer) cycle(ctx context.Context) time.Duration {
	conn, err := s.Store.UptimyConnection(ctx)
	if err != nil {
		s.Log.Warn("cloud sync: loading the connection", "err", err)
		return retryInterval
	}
	if conn == nil {
		s.update(func(st *Status) { *st = Status{} })
		return defaultInterval
	}
	client := s.Client()

	if conn.LocationUUID == "" {
		if err := s.setup(ctx, client, conn); err != nil {
			return s.failed(err)
		}
	}
	interval := time.Duration(conn.UploadIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = defaultInterval
	}

	full := s.now().Sub(s.lastFull) >= fullReconcileEvery
	if err := s.reconcile(ctx, client, conn, full); err != nil {
		if errors.Is(err, connect.ErrNoLocation) {
			s.forgetLocation(ctx, conn)
			return 0
		}
		return s.failed(err)
	}
	if full {
		s.lastFull = s.now()
	}

	for i := 0; i < maxBatchesPerCycle; i++ {
		more, err := s.upload(ctx, client, conn)
		var retry *connect.RetryAfterError
		switch {
		case errors.As(err, &retry):
			s.update(func(st *Status) { st.LastError = "Uptimy asked the agent to slow down; retrying" })
			return retry.After
		case errors.Is(err, connect.ErrNoLocation):
			s.forgetLocation(ctx, conn)
			return 0
		case errors.Is(err, connect.ErrPlanLimit):
			s.update(func(st *Status) { st.OverPlan, st.LastError = true, err.Error() })
			return interval
		case err != nil:
			return s.failed(err)
		}
		if !more {
			break
		}
	}
	return interval
}

func (s *Syncer) failed(err error) time.Duration {
	msg := err.Error()
	if errors.Is(err, connect.ErrKeyRejected) {
		msg = "Uptimy no longer accepts this agent's key; connect again"
	}
	s.Log.Warn("cloud sync", "err", msg)
	s.update(func(st *Status) { st.LastError = msg })
	return retryInterval
}

// forgetLocation makes the next cycle register the location again: Cloud
// has none for this key (it was disconnected there, or set up anew).
func (s *Syncer) forgetLocation(ctx context.Context, conn *connect.Connection) {
	conn.LocationUUID = ""
	if err := s.Store.UpdateUptimyConnection(ctx, *conn); err != nil {
		s.Log.Warn("cloud sync: saving the connection", "err", err)
	}
	if err := s.Store.ResetCloudSync(ctx); err != nil {
		s.Log.Warn("cloud sync: resetting", "err", err)
	}
}

// SetUp sets a new connection up right away (Connect to Uptimy Cloud), so
// a problem such as the plan's agent limit shows while connecting.
func (s *Syncer) SetUp(ctx context.Context, conn *connect.Connection) error {
	if err := s.setup(ctx, s.Client(), conn); err != nil {
		return err
	}
	s.Trigger()
	return nil
}

// Disconnect removes everything the connection created in Cloud: the
// location with its mirrored healthchecks, every heartbeat the key manages
// (the check-in included), then the key. The caller forgets the connection.
// Errors are returned for the user, but the local reset always happens.
func (s *Syncer) Disconnect(ctx context.Context, conn *connect.Connection) error {
	defer func() {
		if err := s.Store.ResetCloudSync(ctx); err != nil {
			s.Log.Warn("cloud sync: resetting", "err", err)
		}
		s.mu.Lock()
		s.status, s.lastSent, s.lastFull = Status{}, map[int64]sent{}, time.Time{}
		s.mu.Unlock()
	}()
	client := s.Client()
	if err := client.DeleteLocation(ctx, conn.APIKey); err != nil {
		return fmt.Errorf("the agent couldn't be removed from Uptimy (%w); disconnect it under Settings → Agents there", err)
	}
	hbs, err := client.ListAgentHeartbeats(ctx, conn.APIKey)
	if err != nil {
		return fmt.Errorf("the agent's heartbeats couldn't be removed from Uptimy (%w); delete them there", err)
	}
	for _, h := range hbs {
		if h.Source == connect.SourceAgent || h.Source == connect.SourceAgentLiveness {
			if err := client.DeleteHeartbeat(ctx, conn.APIKey, h.UUID); err != nil {
				return fmt.Errorf("a heartbeat couldn't be removed from Uptimy (%w); delete it there", err)
			}
		}
	}
	return nil
}

// setup registers the agent's location and its check-in heartbeat
// (agent_liveness), which every upload checks in.
func (s *Syncer) setup(ctx context.Context, client *connect.Client, conn *connect.Connection) error {
	installID, err := s.Store.InstallID(ctx)
	if err != nil {
		return err
	}
	loc, err := client.PutLocation(ctx, conn.APIKey, installID, s.AgentName, s.Version)
	if errors.Is(err, connect.ErrAgentLimit) {
		s.update(func(st *Status) {
			st.OverPlan = true
			st.LastError = "Your Uptimy plan's connected agents are all in use. Upgrade, or disconnect another agent under Settings → Agents in Uptimy."
		})
		return err
	}
	if err != nil {
		return err
	}
	interval := loc.UploadIntervalSeconds
	if interval <= 0 {
		interval = int(defaultInterval.Seconds())
	}

	hb, err := client.EnsureAgentHeartbeat(ctx, conn.APIKey, installID, connect.SourceAgentLiveness, connect.MirroredHeartbeat{
		Name:            "Uptimy Agent · " + s.AgentName,
		Description:     "Created by Uptimy Agent. Alerts when the agent stops uploading.",
		IntervalSeconds: interval,
		GraceSeconds:    interval,
		Paused:          conn.Paused,
	})
	if err != nil {
		return fmt.Errorf("setting up the agent's check-in in Uptimy: %w", err)
	}
	// A reclaimed check-in heartbeat (reconnecting) keeps its settings: give
	// it the current upload interval and keep its alert delay, at least two
	// missed uploads.
	if conn.MaintenanceUntil == nil {
		if hs, err := client.GetHeartbeatSettings(ctx, conn.APIKey, hb.UUID); err == nil && hs.IntervalSeconds != interval {
			grace := max(interval, int(hs.AlertAfter().Seconds())-interval)
			if err := client.PutAgentHeartbeat(ctx, conn.APIKey, hb.UUID, connect.MirroredHeartbeat{
				Name: "Uptimy Agent · " + s.AgentName, Description: "Created by Uptimy Agent. Alerts when the agent stops uploading.",
				IntervalSeconds: interval, GraceSeconds: grace, Paused: hs.Paused,
			}); err != nil {
				return fmt.Errorf("updating the agent's check-in in Uptimy: %w", err)
			}
		}
	}

	conn.LocationUUID, conn.UploadIntervalSeconds, conn.HeartbeatUUID = loc.UUID, interval, hb.UUID
	if err := s.Store.SaveUptimyConnection(ctx, *conn); err != nil {
		return err
	}
	if _, _, ok, err := s.Store.SyncCursors(ctx); err != nil {
		return err
	} else if !ok {
		// Start with the history Cloud can still take.
		r, n, err := s.Store.SyncStartCursors(ctx, s.now().Add(-maxAge))
		if err != nil {
			return err
		}
		if err := s.Store.SetSyncCursors(ctx, r, n); err != nil {
			return err
		}
	}
	s.lastFull = time.Time{}
	s.update(func(st *Status) {
		st.Active, st.LocationUUID, st.UploadIntervalSeconds, st.OverPlan, st.LastError = true, loc.UUID, interval, !loc.WithinPlan, ""
	})
	s.Log.Info("cloud sync set up", "location", loc.UUID, "upload_interval_s", interval)
	return nil
}

// reconcile pushes changed monitors to Cloud and deletes mirrors of deleted
// ones. full also compares with Cloud's own lists and resends everything.
func (s *Syncer) reconcile(ctx context.Context, client *connect.Client, conn *connect.Connection, full bool) error {
	installID, err := s.Store.InstallID(ctx)
	if err != nil {
		return err
	}
	monitors, err := s.Store.ListMonitors(ctx)
	if err != nil {
		return err
	}
	cloud, err := s.Store.CloudMonitors(ctx)
	if err != nil {
		return err
	}

	present := map[int64]bool{}
	for _, m := range monitors {
		present[m.ID] = true
		c, mirrored := cloud[m.ID]
		hash := definitionHash(m)
		if mirrored && c.Hash == hash && !full {
			continue
		}
		switch m.Kind {
		case monitor.KindHealthcheck:
			ref := healthcheckRef(m.ID)
			out, err := client.PutHealthcheck(ctx, conn.APIKey, ref, mirroredHealthcheck(m))
			if err != nil {
				if isFatal(err) {
					return err
				}
				// e.g. a type Cloud can't mirror yet: remember it so it isn't
				// sent every cycle; it uploads nothing.
				s.Log.Warn("cloud sync: mirroring a healthcheck", "monitor", m.Name, "err", err)
				out.HealthcheckUUID = ""
			}
			if err := s.Store.SaveCloudMonitor(ctx, store.CloudMonitor{MonitorID: m.ID, Kind: m.Kind, ClientRef: ref, CloudUUID: out.HealthcheckUUID, Hash: hash}); err != nil {
				return err
			}
		case monitor.KindHeartbeat:
			ref := heartbeatRef(installID, m.ID)
			uuid := c.CloudUUID
			mh := mirroredHeartbeat(m)
			if uuid != "" {
				err := client.PutAgentHeartbeat(ctx, conn.APIKey, uuid, mh)
				if err != nil && !errors.Is(err, connect.ErrHeartbeatGone) && !isNotFound(err) {
					if isFatal(err) {
						return err
					}
					s.Log.Warn("cloud sync: updating a heartbeat", "monitor", m.Name, "err", err)
					continue
				}
				if err != nil {
					uuid = "" // deleted in Cloud: create it again
				}
			}
			if uuid == "" {
				hb, err := client.EnsureAgentHeartbeat(ctx, conn.APIKey, ref, connect.SourceAgent, mh)
				if err != nil {
					if isFatal(err) {
						return err
					}
					s.Log.Warn("cloud sync: mirroring a heartbeat", "monitor", m.Name, "err", err)
					continue
				}
				uuid = hb.UUID
			}
			if err := s.Store.SaveCloudMonitor(ctx, store.CloudMonitor{MonitorID: m.ID, Kind: m.Kind, ClientRef: ref, CloudUUID: uuid, Hash: hash}); err != nil {
				return err
			}
		}
	}

	for id, c := range cloud {
		if present[id] {
			continue
		}
		var err error
		if c.Kind == monitor.KindHealthcheck {
			err = client.DeleteHealthcheck(ctx, conn.APIKey, c.ClientRef)
		} else if c.CloudUUID != "" {
			err = client.DeleteHeartbeat(ctx, conn.APIKey, c.CloudUUID)
		}
		if err != nil {
			if isFatal(err) {
				return err
			}
			s.Log.Warn("cloud sync: deleting a mirror", "err", err)
			continue
		}
		if err := s.Store.DeleteCloudMonitor(ctx, id); err != nil {
			return err
		}
	}

	if full {
		if err := s.deleteStrays(ctx, client, conn); err != nil {
			return err
		}
	}

	cloud, err = s.Store.CloudMonitors(ctx)
	if err != nil {
		return err
	}
	hc, hb := 0, 0
	for _, c := range cloud {
		if c.CloudUUID == "" {
			continue
		}
		if c.Kind == monitor.KindHealthcheck {
			hc++
		} else {
			hb++
		}
	}
	s.update(func(st *Status) { st.Active, st.Healthchecks, st.Heartbeats = true, hc, hb })
	return nil
}

// deleteStrays removes mirrors in Cloud that this agent no longer knows,
// e.g. after its database was reset.
func (s *Syncer) deleteStrays(ctx context.Context, client *connect.Client, conn *connect.Connection) error {
	cloud, err := s.Store.CloudMonitors(ctx)
	if err != nil {
		return err
	}
	known := map[string]bool{}
	for _, c := range cloud {
		known[c.ClientRef] = true
		known[c.CloudUUID] = true
	}
	hcs, err := client.ListHealthchecks(ctx, conn.APIKey)
	if err != nil {
		return err
	}
	for _, h := range hcs {
		if !known[h.ClientRef] {
			if err := client.DeleteHealthcheck(ctx, conn.APIKey, h.ClientRef); err != nil && isFatal(err) {
				return err
			}
		}
	}
	hbs, err := client.ListAgentHeartbeats(ctx, conn.APIKey)
	if err != nil {
		return err
	}
	for _, h := range hbs {
		if h.Source == connect.SourceAgent && !known[h.UUID] {
			if err := client.DeleteHeartbeat(ctx, conn.APIKey, h.UUID); err != nil && isFatal(err) {
				return err
			}
		}
	}
	return nil
}

// upload sends one batch and reports whether more is waiting.
func (s *Syncer) upload(ctx context.Context, client *connect.Client, conn *connect.Connection) (bool, error) {
	installID, err := s.Store.InstallID(ctx)
	if err != nil {
		return false, err
	}
	resultsCursor, runsCursor, ok, err := s.Store.SyncCursors(ctx)
	if err != nil || !ok {
		return false, err
	}
	cloud, err := s.Store.CloudMonitors(ctx)
	if err != nil {
		return false, err
	}
	interval := time.Duration(conn.UploadIntervalSeconds) * time.Second
	now := s.now()

	up := connect.Upload{LocationUUID: conn.LocationUUID, Healthchecks: []connect.UploadHealthcheck{}, Heartbeats: []connect.UploadHeartbeat{}}
	pending := map[int64]sent{} // committed to lastSent only once Cloud accepts
	s.mu.Lock()
	sentBefore := make(map[int64]sent, len(s.lastSent))
	for id, v := range s.lastSent {
		sentBefore[id] = v
	}
	s.mu.Unlock()
	last := func(id int64) (sent, bool) {
		if p, ok := pending[id]; ok {
			return p, true
		}
		l, ok := sentBefore[id]
		return l, ok
	}

	rows, err := s.Store.ResultsAfter(ctx, resultsCursor, scanRows)
	if err != nil {
		return false, err
	}
	newResults, more := resultsCursor, len(rows) == scanRows
	for _, r := range rows {
		if len(up.Healthchecks) >= maxItems {
			more = true
			break
		}
		newResults = r.RowID
		c, mirrored := cloud[r.MonitorID]
		if !mirrored || c.CloudUUID == "" || c.Kind != monitor.KindHealthcheck || now.Sub(r.Time) > maxAge {
			continue
		}
		status, since, err := s.healthcheckStatus(ctx, r.Result)
		if err != nil {
			return false, err
		}
		prev, seen := last(r.MonitorID)
		if seen && prev.status == status && r.Time.Sub(prev.at) < interval {
			continue // thinned: nothing changed and the last one is recent
		}
		latency := int(r.LatencyMS)
		item := connect.UploadHealthcheck{HealthcheckUUID: c.CloudUUID, CheckedAt: r.Time, OK: r.OK, LatencyMs: &latency, Status: status}
		if !r.OK {
			item.Message = truncate(r.Message)
		}
		if !since.IsZero() {
			item.StatusSince = &since
		}
		up.Healthchecks = append(up.Healthchecks, item)
		pending[r.MonitorID] = sent{at: r.Time, status: status}
	}

	newRuns := runsCursor
	var runs []monitor.Run
	if room := maxItems - len(up.Healthchecks); room > 0 {
		if runs, err = s.Store.RunsAfter(ctx, runsCursor, room); err != nil {
			return false, err
		}
		more = more || len(runs) == room
	} else {
		more = true
	}
	for _, run := range runs {
		newRuns = run.ID
		c, mirrored := cloud[run.MonitorID]
		if !mirrored || c.CloudUUID == "" || c.Kind != monitor.KindHeartbeat || now.Sub(*run.FinishedAt) > maxAge {
			continue
		}
		status, _, _, err := s.Store.StatusAt(ctx, run.MonitorID, run.FinishedAt.Add(statusSlack))
		if err != nil {
			return false, err
		}
		item := connect.UploadHeartbeat{
			HeartbeatUUID: c.CloudUUID, At: *run.FinishedAt, Outcome: outcomeOf(run.Outcome),
			Message: truncate(run.Message), Status: "up",
		}
		if status == monitor.StatusDown {
			item.Status = "down"
		}
		if run.DurationMS != nil {
			item.DurationMs = *run.DurationMS
		}
		up.Heartbeats = append(up.Heartbeats, item)
	}

	// The same cursors give the same batch id, so a retry after a timeout is
	// recognized and nothing is stored twice.
	sum := sha256.Sum256([]byte(installID + "|" + strconv.FormatInt(resultsCursor, 10) + "|" + strconv.FormatInt(runsCursor, 10) + "|" + strconv.FormatInt(newResults, 10) + "|" + strconv.FormatInt(newRuns, 10)))
	up.BatchID = hex.EncodeToString(sum[:16])

	res, err := client.UploadResults(ctx, conn.APIKey, up)
	if err != nil {
		return false, err
	}
	if err := s.Store.SetSyncCursors(ctx, newResults, newRuns); err != nil {
		return false, err
	}
	at := s.now().UTC()
	s.mu.Lock()
	for id, p := range pending {
		s.lastSent[id] = p
	}
	s.status.LastUploadAt, s.status.LastError, s.status.OverPlan = &at, "", false
	s.mu.Unlock()

	if res.UploadIntervalSeconds > 0 && res.UploadIntervalSeconds != conn.UploadIntervalSeconds {
		// The plan changed: set the check-in heartbeat up for the new interval.
		s.Log.Info("cloud sync: upload interval changed", "from_s", conn.UploadIntervalSeconds, "to_s", res.UploadIntervalSeconds)
		s.forgetLocation(ctx, conn)
		return false, nil
	}
	return more, nil
}

// healthcheckStatus is the status the agent showed after a result, in
// Cloud's terms: down, or up, or degraded for a failing check that hasn't
// reached its failure threshold yet.
func (s *Syncer) healthcheckStatus(ctx context.Context, r monitor.Result) (string, time.Time, error) {
	// A result is stamped when its check started; the status change it causes
	// is recorded once the check is done, up to its latency later.
	decided := r.Time.Add(time.Duration(r.LatencyMS)*time.Millisecond + statusSlack)
	status, since, _, err := s.Store.StatusAt(ctx, r.MonitorID, decided)
	if err != nil {
		return "", time.Time{}, err
	}
	switch {
	case status == monitor.StatusDown:
		return "down", since, nil
	case !r.OK:
		return "degraded", since, nil
	default:
		return "up", since, nil
	}
}

func outcomeOf(o monitor.RunOutcome) string {
	switch o {
	case monitor.OutcomeSuccess:
		return "success"
	case monitor.OutcomeFailure:
		return "failed"
	default:
		return "missed"
	}
}

func mirroredHealthcheck(m monitor.Monitor) connect.MirroredHealthcheck {
	c := m.Check
	config := map[string]any{}
	if c.Config.Method != "" {
		config["method"] = c.Config.Method
	}
	if c.Config.ExpectedStatus != "" {
		config["expectedStatus"] = c.Config.ExpectedStatus
	}
	if c.Config.RecordType != "" {
		config["recordType"] = c.Config.RecordType
	}
	return connect.MirroredHealthcheck{
		Name: m.Name, Type: string(c.Type),
		// Passwords never leave the agent; ${VAR} references stay references.
		Target:          c.DisplayTarget(),
		IntervalSeconds: c.IntervalSeconds, TimeoutSeconds: c.TimeoutSeconds,
		Config: config, Paused: m.Paused,
	}
}

func mirroredHeartbeat(m monitor.Monitor) connect.MirroredHeartbeat {
	h := m.Heartbeat
	interval := h.EverySeconds
	if interval <= 0 {
		// A cron schedule: the gap between its next two runs.
		if next := h.Upcoming(time.Now(), 2); len(next) == 2 {
			interval = int(next[1].Sub(next[0]).Seconds())
		}
	}
	return connect.MirroredHeartbeat{
		Name: m.Name, Description: h.Describe(),
		IntervalSeconds: max(interval, 60), GraceSeconds: h.GraceSeconds, Paused: m.Paused,
	}
}

// definitionHash is what decides whether a mirror needs sending again.
func definitionHash(m monitor.Monitor) string {
	var v any
	if m.Kind == monitor.KindHealthcheck {
		v = mirroredHealthcheck(m)
	} else {
		mh := mirroredHeartbeat(m)
		v = mh
	}
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

func healthcheckRef(id int64) string { return "hc-" + strconv.FormatInt(id, 10) }

// heartbeatRef must be unique in the workspace, which other agents share.
func heartbeatRef(installID string, id int64) string {
	return installID + ":hb-" + strconv.FormatInt(id, 10)
}

func truncate(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

// isFatal errors stop the whole cycle: Cloud can't be used with this key, or
// can't be reached at all.
func isFatal(err error) bool {
	var retry *connect.RetryAfterError
	return errors.Is(err, connect.ErrKeyRejected) || errors.Is(err, connect.ErrNoLocation) ||
		errors.As(err, &retry) || errors.Is(err, connect.ErrUnreachable)
}

func isNotFound(err error) bool { return errors.Is(err, connect.ErrNotFound) }
