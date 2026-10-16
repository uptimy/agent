// Package cloudsync mirrors the agent's monitors to Uptimy Cloud and uploads
// their results (AGENT_CLOUD_SYNC_PLAN.md in the uptimy repo).
//
// The agent stays the source of truth: it defines, runs and judges its
// monitors and alerts on its own. Cloud gets a read-only copy of each
// monitor, every status change, and a thinned history (at most one unchanged
// result per monitor per sample interval). Uploads run every upload interval;
// a status change is sent sooner, at most once per status change interval
// (the plan's check interval). Each upload also checks the agent in: Cloud
// alerts when uploads stop.
package cloudsync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
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
	// A rejected key waits longer each time, up to this: only connecting
	// again helps.
	maxKeyRetry = time.Hour
	// Without a sample interval from Cloud: one unchanged result per monitor
	// per 5 minutes.
	defaultSampleInterval = 5 * time.Minute
	// Watch waits this long after an event, so a burst of edits or status
	// changes is sent in one upload.
	debounce = 5 * time.Second
	// Cloud's limits for a mirror (upti.my-api agent_requests.ts).
	maxTarget, maxLocationName        = 2048, 100
	maxCloudInterval, maxCloudTimeout = 86400, 3600
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
	// NotSynced are monitors that couldn't be mirrored, and why.
	NotSynced []NotSynced `json:"not_synced,omitempty"`
	// Results Cloud rejected for their time, in uploads since the last one
	// without such a rejection: the agent's clock is likely off.
	RejectedTooOld     int `json:"rejected_too_old,omitempty"`
	RejectedClockAhead int `json:"rejected_clock_ahead,omitempty"`
}

// NotSynced is a monitor that isn't in Cloud.
type NotSynced struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// failedMirror is a monitor whose mirror failed, with its definition then.
type failedMirror struct {
	hash    string
	refused bool // Cloud refused it: not sent again unchanged until a full reconcile
	NotSynced
}

// Syncer runs the sync for the agent's "Connect to Uptimy Cloud" connection.
type Syncer struct {
	Store     *store.Store
	Client    func() *connect.Client
	AgentName string
	Version   string
	Log       *slog.Logger

	wake  chan struct{}
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu       sync.Mutex
	status   Status
	lastFull time.Time
	// statusEvery is the connection's status change interval, for Watch.
	statusEvery time.Duration
	// lastSent thins uploads: per monitor, when its last result was sent and
	// with which status.
	lastSent map[int64]sent
	// mirrorErrs are the monitors whose mirror failed, by monitor id.
	mirrorErrs map[int64]failedMirror
	// keyRejections counts cycles in a row that Cloud rejected the key.
	keyRejections int
	// locationSent is the name and version the location was last given
	// in this process: Cloud gets them on start and whenever they change.
	locationSent string
}

type sent struct {
	at     time.Time
	status string
}

// New returns a syncer; Run starts it.
func New(st *store.Store, client func() *connect.Client, agentName, version string, log *slog.Logger) *Syncer {
	return &Syncer{
		Store: st, Client: client, AgentName: agentName, Version: version, Log: log,
		wake: make(chan struct{}, 1), now: time.Now, after: time.After, lastSent: map[int64]sent{}, mirrorErrs: map[int64]failedMirror{},
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
// resolves the Cloud incident without waiting for the next upload. A status
// change uploads no sooner than the status change interval after the last
// upload (the plan's check interval).
func (s *Syncer) Watch(ctx context.Context, subscribe func() (<-chan events.Message, func())) {
	msgs, unsubscribe := subscribe()
	defer unsubscribe()
	var timer <-chan time.Time
	var due time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return
			}
			var wait time.Duration
			switch m.Type {
			case "monitors":
				wait = debounce
			case "status":
				wait = s.statusWait()
			default:
				continue
			}
			// One timer: the earlier trigger wins.
			if at := s.now().Add(wait); timer == nil || at.Before(due) {
				timer, due = s.after(wait), at
			}
		case <-timer:
			timer = nil
			s.Trigger()
		}
	}
}

// statusWait is how long a status change waits before it is uploaded.
func (s *Syncer) statusWait() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.status.LastUploadAt == nil {
		return debounce
	}
	return max(debounce, s.status.LastUploadAt.Add(s.statusEvery).Sub(s.now()))
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
	} else if s.locationSent != s.locationName()+"|"+s.Version {
		if err := s.updateLocation(ctx, client, conn); errors.Is(err, connect.ErrNoLocation) {
			s.forgetLocation(ctx, conn)
			return 0
		} else if err != nil && isFatal(err) {
			return s.failed(err)
		} else if err != nil {
			s.Log.Warn("cloud sync: updating the location", "err", err) // tried again next cycle
		}
	}
	interval, statusEvery, _ := intervals(conn)
	s.mu.Lock()
	s.statusEvery = statusEvery
	s.status.LocationUUID, s.status.UploadIntervalSeconds = conn.LocationUUID, int(interval.Seconds())
	s.mu.Unlock()

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
	msg, wait := err.Error(), retryInterval
	if errors.Is(err, connect.ErrKeyRejected) {
		msg = "Uptimy no longer accepts this agent's key; connect again"
		wait = min(retryInterval<<min(s.keyRejections, 10), maxKeyRetry)
		s.keyRejections++
	}
	s.Log.Warn("cloud sync", "err", msg, "retry_in", wait)
	s.update(func(st *Status) { st.LastError = msg })
	return wait
}

// forgetLocation makes the next cycle register the location again: Cloud
// has none for this key (it was disconnected there, or set up anew). Every
// mirror is sent again, to the new location. The cursors stay: what Cloud
// already had isn't sent again, which would open and resolve its old
// incidents once more.
func (s *Syncer) forgetLocation(ctx context.Context, conn *connect.Connection) {
	s.resetUp(ctx, conn)
	if err := s.Store.MarkCloudMonitorsStale(ctx); err != nil {
		s.Log.Warn("cloud sync: marking mirrors for resending", "err", err)
	}
}

// resetUp makes the next cycle set the location and check-in up again.
func (s *Syncer) resetUp(ctx context.Context, conn *connect.Connection) {
	if err := s.updateConnection(ctx, conn, func(c *connect.Connection) { c.LocationUUID = "" }); err != nil {
		s.Log.Warn("cloud sync: saving the connection", "err", err)
	}
}

// updateConnection applies change to conn and to the stored connection,
// loaded afresh so a setting changed in the meantime (pause, maintenance)
// isn't overwritten with this cycle's copy.
func (s *Syncer) updateConnection(ctx context.Context, conn *connect.Connection, change func(*connect.Connection)) error {
	change(conn)
	stored, err := s.Store.UptimyConnection(ctx)
	if err != nil || stored == nil {
		return err // nil: disconnected meanwhile, nothing to update
	}
	change(stored)
	return s.Store.UpdateUptimyConnection(ctx, *stored)
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
		s.status, s.lastSent, s.lastFull, s.statusEvery = Status{}, map[int64]sent{}, time.Time{}, 0
		s.mirrorErrs, s.locationSent, s.keyRejections = map[int64]failedMirror{}, "", 0
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
	loc, err := client.PutLocation(ctx, conn.APIKey, installID, s.locationName(), s.Version)
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

	setUp := func(c *connect.Connection) {
		c.LocationUUID, c.UploadIntervalSeconds, c.HeartbeatUUID = loc.UUID, interval, hb.UUID
		c.StatusChangeIntervalSeconds, c.SampleIntervalSeconds = loc.StatusChangeIntervalSeconds, loc.SampleIntervalSeconds
	}
	if stored, err := s.Store.UptimyConnection(ctx); err != nil {
		return err
	} else if stored == nil || stored.APIKey != conn.APIKey {
		// Connecting (or reconnecting with a new key): conn is the new connection.
		setUp(conn)
		if err := s.Store.SaveUptimyConnection(ctx, *conn); err != nil {
			return err
		}
	} else if err := s.updateConnection(ctx, conn, setUp); err != nil {
		return err
	}
	if _, _, ok, err := s.Store.SyncCursors(ctx); err != nil {
		return err
	} else if !ok {
		// Start now: history from before connecting would open incidents in
		// Cloud that are long over.
		r, n, pending, err := s.Store.SyncStartCursors(ctx)
		if err != nil {
			return err
		}
		if err := s.Store.SetSyncCursors(ctx, r, n, pending); err != nil {
			return err
		}
	}
	s.lastFull, s.locationSent = time.Time{}, s.locationName()+"|"+s.Version
	s.update(func(st *Status) {
		st.Active, st.LocationUUID, st.UploadIntervalSeconds, st.OverPlan, st.LastError = true, loc.UUID, interval, !loc.WithinPlan, ""
	})
	s.Log.Info("cloud sync set up", "location", loc.UUID, "upload_interval_s", interval)
	return nil
}

// locationName is the agent's name as Cloud takes it.
func (s *Syncer) locationName() string { return truncateRunes(s.AgentName, maxLocationName) }

// updateLocation gives the location the agent's current name and version.
// Cloud answering with another location means it lost this one.
func (s *Syncer) updateLocation(ctx context.Context, client *connect.Client, conn *connect.Connection) error {
	installID, err := s.Store.InstallID(ctx)
	if err != nil {
		return err
	}
	loc, err := client.PutLocation(ctx, conn.APIKey, installID, s.locationName(), s.Version)
	if err != nil {
		return err
	}
	if loc.UUID != conn.LocationUUID {
		return connect.ErrNoLocation
	}
	s.locationSent = s.locationName() + "|" + s.Version
	return nil
}

// reconcile pushes changed monitors to Cloud and deletes mirrors of deleted
// ones. full also compares with Cloud's own lists: it sends a mirror Cloud
// no longer has, and deletes ones this agent doesn't know. A mirror that's
// unchanged and still in Cloud isn't sent again: a heartbeat's update
// resets its state in Cloud.
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
	var hcs []connect.HealthcheckRef
	var hbs []connect.HeartbeatRef
	inCloud := map[string]bool{} // healthchecks' clientRefs, heartbeats' uuids
	if full {
		if hcs, err = client.ListHealthchecks(ctx, conn.APIKey); err != nil {
			return err
		}
		if hbs, err = client.ListAgentHeartbeats(ctx, conn.APIKey); err != nil {
			return err
		}
		for _, h := range hcs {
			inCloud[h.ClientRef] = true
		}
		for _, h := range hbs {
			inCloud[h.UUID] = true
		}
	}

	present := map[int64]bool{}
	for _, m := range monitors {
		present[m.ID] = true
		c, mirrored := cloud[m.ID]
		hash := definitionHash(m)
		missing := full && mirrored && !inCloud[c.ClientRef] && !inCloud[c.CloudUUID]
		if mirrored && c.Hash == hash && !missing {
			delete(s.mirrorErrs, m.ID)
			continue
		}
		if f, ok := s.mirrorErrs[m.ID]; ok && f.refused && f.hash == hash && !full {
			continue // refused as it is
		}
		// On an error the mirror keeps what Cloud had (its uuid and hash):
		// the next cycle tries again, or, if Cloud refused it, the next full
		// reconcile or a change.
		failed := func(err error) {
			var refused *connect.RefusedError
			f := failedMirror{hash: hash, refused: errors.As(err, &refused), NotSynced: NotSynced{Name: m.Name, Reason: err.Error()}}
			if f.refused {
				f.Reason = refused.Reason
			}
			s.mirrorErrs[m.ID] = f
			s.Log.Warn("cloud sync: mirroring a monitor", "monitor", m.Name, "err", err)
		}
		switch m.Kind {
		case monitor.KindHealthcheck:
			if len(m.Check.DisplayTarget()) > maxTarget {
				failed(&connect.RefusedError{Reason: fmt.Sprintf("its target is longer than the %d characters Uptimy takes", maxTarget)})
				continue
			}
			ref := healthcheckRef(m.ID)
			out, err := client.PutHealthcheck(ctx, conn.APIKey, ref, mirroredHealthcheck(m))
			if err != nil {
				if isFatal(err) {
					return err
				}
				failed(err)
				continue
			}
			if err := s.Store.SaveCloudMonitor(ctx, store.CloudMonitor{MonitorID: m.ID, Kind: m.Kind, ClientRef: ref, CloudUUID: out.HealthcheckUUID, Hash: hash}); err != nil {
				return err
			}
		case monitor.KindHeartbeat:
			ref := heartbeatRef(installID, m.ID)
			uuid := c.CloudUUID
			if missing {
				uuid = "" // deleted in Cloud: create it again
			}
			mh := mirroredHeartbeat(m)
			if uuid != "" {
				err := client.PutAgentHeartbeat(ctx, conn.APIKey, uuid, mh)
				if err != nil && !errors.Is(err, connect.ErrHeartbeatGone) && !isNotFound(err) {
					if isFatal(err) {
						return err
					}
					failed(err)
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
					failed(err)
					continue
				}
				uuid = hb.UUID
			}
			if err := s.Store.SaveCloudMonitor(ctx, store.CloudMonitor{MonitorID: m.ID, Kind: m.Kind, ClientRef: ref, CloudUUID: uuid, Hash: hash}); err != nil {
				return err
			}
		}
		delete(s.mirrorErrs, m.ID)
	}
	var notSynced []NotSynced
	for id, f := range s.mirrorErrs {
		if !present[id] {
			delete(s.mirrorErrs, id)
			continue
		}
		notSynced = append(notSynced, f.NotSynced)
	}
	slices.SortFunc(notSynced, func(a, b NotSynced) int { return strings.Compare(a.Name, b.Name) })

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

	cloud, err = s.Store.CloudMonitors(ctx)
	if err != nil {
		return err
	}
	if full {
		if err := s.deleteStrays(ctx, client, conn, cloud, hcs, hbs); err != nil {
			return err
		}
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
	s.update(func(st *Status) { st.Active, st.Healthchecks, st.Heartbeats, st.NotSynced = true, hc, hb, notSynced })
	return nil
}

// deleteStrays removes mirrors in Cloud (as listed before this reconcile)
// that this agent no longer knows, e.g. after its database was reset.
func (s *Syncer) deleteStrays(ctx context.Context, client *connect.Client, conn *connect.Connection, cloud map[int64]store.CloudMonitor, hcs []connect.HealthcheckRef, hbs []connect.HeartbeatRef) error {
	known := map[string]bool{}
	for _, c := range cloud {
		known[c.ClientRef] = true
		known[c.CloudUUID] = true
	}
	for _, h := range hcs {
		if !known[h.ClientRef] {
			if err := client.DeleteHealthcheck(ctx, conn.APIKey, h.ClientRef); err != nil && isFatal(err) {
				return err
			}
		}
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
	_, _, sample := intervals(conn)
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

	// Past the newest result (they were deleted, e.g. with their monitor),
	// the cursor would skip the rowids SQLite hands out again.
	maxRow, err := s.Store.MaxResultRowID(ctx)
	if err != nil {
		return false, err
	}
	resultsCursor = min(resultsCursor, maxRow)

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
		// A healthcheck not mirrored yet (a failed create) misses these
		// results; once it is, each result it sends carries its status and
		// since when, so Cloud still learns of a status change.
		c, mirrored := cloud[r.MonitorID]
		if !mirrored || c.CloudUUID == "" || c.Kind != monitor.KindHealthcheck || now.Sub(r.Time) > maxAge {
			continue
		}
		status, since, err := s.healthcheckStatus(ctx, r.Result)
		if err != nil {
			return false, err
		}
		prev, seen := last(r.MonitorID)
		if seen && prev.status == status && r.Time.Sub(prev.at) < sample {
			continue // thinned: nothing changed and the last one is recent
		}
		latency := int(r.LatencyMS)
		item := connect.UploadHealthcheck{
			HealthcheckUUID: c.CloudUUID, CheckedAt: r.Time, OK: r.OK, LatencyMs: &latency, Status: status,
			StatusCode: r.StatusCode,
		}
		if !r.OK {
			item.Message = truncate(r.Message)
			item.ErrorClass = r.ErrorClass
		}
		if !since.IsZero() {
			item.StatusSince = &since
		}
		up.Healthchecks = append(up.Healthchecks, item)
		pending[r.MonitorID] = sent{at: r.Time, status: status}
	}

	// Runs: first those the cursor passed but couldn't send yet, then the
	// runs after the cursor. A run not sent yet is remembered (pending), not
	// waited for, so it holds up no other run: one still running (it's sent
	// once it finishes: the same row, updated), and one of a heartbeat not
	// mirrored yet (a failed create), so its status change still reaches
	// Cloud once it is. Runs older than Cloud takes are dropped.
	newRuns := runsCursor
	pendingIDs, err := s.Store.SyncPendingRuns(ctx)
	if err != nil {
		return false, err
	}
	earlier, err := s.Store.RunsByID(ctx, pendingIDs) // gone ones (deleted with their heartbeat) are dropped
	if err != nil {
		return false, err
	}
	var runs []monitor.Run
	var pendingRuns []int64
	earlierIDs := "" // the earlier runs sent, for the batch id
	room := maxItems - len(up.Healthchecks)
	// take reports whether run is sent in this batch, and keeps it pending if
	// it can't be yet.
	take := func(run monitor.Run) bool {
		c := cloud[run.MonitorID]
		switch {
		case run.Outcome == monitor.OutcomeRunning || (c.CloudUUID == "" && now.Sub(*run.FinishedAt) <= maxAge):
			pendingRuns = append(pendingRuns, run.ID)
		case c.CloudUUID == "" || c.Kind != monitor.KindHeartbeat || now.Sub(*run.FinishedAt) > maxAge:
		case len(runs) >= room:
			pendingRuns, more = append(pendingRuns, run.ID), true
		default:
			runs = append(runs, run)
			return true
		}
		return false
	}
	for _, run := range earlier {
		if take(run) {
			earlierIDs += "|" + strconv.FormatInt(run.ID, 10)
		}
	}
	if left := room - len(runs); left > 0 {
		after, err := s.Store.RunsAfter(ctx, runsCursor, left)
		if err != nil {
			return false, err
		}
		more = more || len(after) == left
		for _, run := range after {
			newRuns = run.ID
			take(run)
		}
	} else {
		more = true
	}
	for _, run := range runs {
		c := cloud[run.MonitorID]
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
	// recognized and nothing is stored twice. An empty batch only checks the
	// agent in: it gets an id of its own, or every idle upload would look
	// like a repeat of the first.
	sum := sha256.Sum256([]byte(installID + "|" + strconv.FormatInt(resultsCursor, 10) + "|" + strconv.FormatInt(runsCursor, 10) + "|" + strconv.FormatInt(newResults, 10) + "|" + strconv.FormatInt(newRuns, 10) + earlierIDs))
	if len(up.Healthchecks)+len(up.Heartbeats) == 0 {
		_, _ = rand.Read(sum[:16]) // never fails (crypto/rand panics instead)
	}
	up.BatchID = hex.EncodeToString(sum[:16])

	res, err := client.UploadResults(ctx, conn.APIKey, up)
	if err != nil {
		return false, err
	}
	if err := s.Store.SetSyncCursors(ctx, newResults, newRuns, pendingRuns); err != nil {
		return false, err
	}
	rejected := map[string]int{}
	var unknown []string
	for _, r := range res.Rejected {
		rejected[r.Reason]++
		if r.Reason == connect.RejectedUnknown {
			unknown = append(unknown, r.UUID)
		}
	}
	if len(unknown) > 0 {
		// Cloud doesn't know these mirrors (any more): send them again,
		// which returns their current uuids.
		s.Log.Warn("cloud sync: Uptimy doesn't know some mirrors; sending them again", "count", len(unknown))
		if err := s.Store.MarkCloudMonitorsStale(ctx, unknown...); err != nil {
			return false, err
		}
	}
	at := s.now().UTC()
	items := len(up.Healthchecks) + len(up.Heartbeats)
	s.keyRejections = 0
	s.mu.Lock()
	for id, p := range pending {
		s.lastSent[id] = p
	}
	s.status.LastUploadAt, s.status.LastError, s.status.OverPlan = &at, "", false
	countRejected(&s.status.RejectedTooOld, rejected[connect.RejectedTooOld], items)
	countRejected(&s.status.RejectedClockAhead, rejected[connect.RejectedClockAhead], items)
	s.mu.Unlock()

	if res.UploadIntervalSeconds > 0 && res.UploadIntervalSeconds != conn.UploadIntervalSeconds {
		// The plan changed: set the check-in heartbeat up for the new interval.
		s.Log.Info("cloud sync: upload interval changed", "from_s", conn.UploadIntervalSeconds, "to_s", res.UploadIntervalSeconds)
		s.resetUp(ctx, conn)
		return false, nil
	}
	newStatusChange, newSample := conn.StatusChangeIntervalSeconds, conn.SampleIntervalSeconds
	statusChanged := changed(&newStatusChange, res.StatusChangeIntervalSeconds)
	if sampleChanged := changed(&newSample, res.SampleIntervalSeconds); statusChanged || sampleChanged {
		s.Log.Info("cloud sync: intervals changed", "status_change_s", newStatusChange, "sample_s", newSample)
		err := s.updateConnection(ctx, conn, func(c *connect.Connection) {
			c.StatusChangeIntervalSeconds, c.SampleIntervalSeconds = newStatusChange, newSample
		})
		if err != nil {
			return false, err
		}
		_, statusEvery, _ := intervals(conn)
		s.mu.Lock()
		s.statusEvery = statusEvery
		s.mu.Unlock()
	}
	return more, nil
}

// countRejected adds an upload's rejections to *count, or clears it when an
// upload with items had none.
func countRejected(count *int, n, items int) {
	switch {
	case n > 0:
		*count += n
	case items > 0:
		*count = 0
	}
}

// changed sets *v to a value Cloud sent (0: none) and reports whether it differed.
func changed(v *int, got int) bool {
	if got <= 0 || got == *v {
		return false
	}
	*v = got
	return true
}

// intervals are the connection's upload, status change and sample
// intervals. An older Cloud sends only the upload interval: status changes
// then wait for it too.
func intervals(conn *connect.Connection) (upload, statusChange, sample time.Duration) {
	upload = time.Duration(conn.UploadIntervalSeconds) * time.Second
	if upload <= 0 {
		upload = defaultInterval
	}
	statusChange = time.Duration(conn.StatusChangeIntervalSeconds) * time.Second
	if statusChange <= 0 {
		statusChange = upload
	}
	sample = time.Duration(conn.SampleIntervalSeconds) * time.Second
	if sample <= 0 {
		sample = defaultSampleInterval
	}
	return upload, statusChange, sample
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
		Target: c.DisplayTarget(),
		// Cloud only shows these; it takes them within its limits.
		IntervalSeconds: min(max(c.IntervalSeconds, 1), maxCloudInterval),
		TimeoutSeconds:  min(max(c.TimeoutSeconds, 1), maxCloudTimeout),
		Config:          config, Paused: m.Paused,
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

// truncateRunes cuts s to at most n characters.
func truncateRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
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
