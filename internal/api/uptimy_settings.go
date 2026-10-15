package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/uptimy/agent/internal/connect"
)

// When Uptimy alerts about a silent agent, and how to quiet it for planned
// work. Only for "Connect to Uptimy" connections: a pasted heartbeat URL
// gives the agent no access to the heartbeat's settings.
//
// Uptimy holds the settings (people can also change them there or with
// uptimyctl), so every read goes to Uptimy. The agent only remembers what it
// needs to undo: the normal grace during a maintenance window.

// alertAfterChoices are the delays offered in the UI, in minutes.
var alertAfterChoices = []int{2, 5, 10, 15, 30, 60}

// maintenanceChoices are the maintenance windows offered in the UI, in minutes.
var maintenanceChoices = []int{30, 60, 240}

type heartbeatSettingsResponse struct {
	// Available is false when the agent can't manage the heartbeat (no account
	// connection); the UI then points to Uptimy instead.
	Available         bool       `json:"available"`
	AlertAfterSeconds int        `json:"alert_after_seconds,omitempty"`
	Paused            bool       `json:"paused"`
	MaintenanceUntil  *time.Time `json:"maintenance_until,omitempty"`
	// NormalAlertAfterSeconds is what alerting goes back to after maintenance.
	NormalAlertAfterSeconds int   `json:"normal_alert_after_seconds,omitempty"`
	AlertAfterChoices       []int `json:"alert_after_choices"`
	MaintenanceChoices      []int `json:"maintenance_choices"`
}

// checkInInterval is how often the agent checks in: every upload once the
// Cloud sync is set up, otherwise the old one-minute ping.
func checkInInterval(conn *connect.Connection) time.Duration {
	if conn.LocationUUID != "" && conn.UploadIntervalSeconds > 0 {
		return time.Duration(conn.UploadIntervalSeconds) * time.Second
	}
	return connect.CheckInInterval
}

// alertAfterChoicesFor offers only delays of at least two check-ins, so one
// late upload never alerts.
func alertAfterChoicesFor(conn *connect.Connection) []int {
	minimum := int((2 * checkInInterval(conn)).Minutes())
	var out []int
	for _, m := range alertAfterChoices {
		if m >= minimum {
			out = append(out, m)
		}
	}
	if len(out) == 0 {
		out = []int{minimum}
	}
	return out
}

func (s *Server) uptimyClient() *connect.Client {
	return connect.NewClient(s.Config.UptimyAPIURL, s.Config.UptimyHeartbeatsURL)
}

// settingsResponse describes hs, keeping the watchdog in step with a pause
// made in Uptimy itself.
func (s *Server) settingsResponse(ctx context.Context, conn *connect.Connection, hs connect.HeartbeatSettings) heartbeatSettingsResponse {
	s.Watchdog.SetPaused(ctx, hs.Paused)
	if conn.Paused && !hs.Paused && conn.LocationUUID != "" {
		s.Sync.Trigger() // resumed: check in right away
	}
	if conn.Paused != hs.Paused {
		conn.Paused = hs.Paused
		if err := s.Store.UpdateUptimyConnection(ctx, *conn); err != nil {
			s.Log.Warn("saving the Uptimy connection", "err", err)
		}
	}
	resp := heartbeatSettingsResponse{
		Available:          true,
		AlertAfterSeconds:  int(hs.AlertAfter().Seconds()),
		Paused:             hs.Paused,
		AlertAfterChoices:  alertAfterChoicesFor(conn),
		MaintenanceChoices: maintenanceChoices,
	}
	if conn.MaintenanceUntil != nil {
		resp.MaintenanceUntil = conn.MaintenanceUntil
		resp.NormalAlertAfterSeconds = hs.IntervalSeconds + conn.NormalGraceSeconds
	}
	return resp
}

// connectionForSettings loads the account connection, or answers "not
// available" and returns nil.
func (s *Server) connectionForSettings(w http.ResponseWriter, r *http.Request) *connect.Connection {
	conn, err := s.Store.UptimyConnection(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return nil
	}
	if conn == nil || s.Watchdog.ManagedByEnv() {
		writeJSON(w, http.StatusOK, heartbeatSettingsResponse{AlertAfterChoices: alertAfterChoices, MaintenanceChoices: maintenanceChoices})
		return nil
	}
	return conn
}

func (s *Server) uptimyError(w http.ResponseWriter, err error) {
	if errors.Is(err, connect.ErrKeyRejected) {
		writeError(w, http.StatusBadGateway, "Uptimy no longer accepts this agent's key; connect again")
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
}

func (s *Server) getHeartbeatSettings(w http.ResponseWriter, r *http.Request) {
	conn := s.connectionForSettings(w, r)
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	hs, err := s.uptimyClient().GetHeartbeatSettings(ctx, conn.APIKey, conn.HeartbeatUUID)
	if err != nil {
		s.uptimyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(r.Context(), conn, hs))
}

// updateHeartbeatSettings changes how long the agent may be silent before
// Uptimy alerts, and/or pauses alerting.
func (s *Server) updateHeartbeatSettings(w http.ResponseWriter, r *http.Request) {
	conn := s.connectionForSettings(w, r)
	if conn == nil {
		return
	}
	var in struct {
		AlertAfterSeconds *int  `json:"alert_after_seconds"`
		Paused            *bool `json:"paused"`
	}
	if !decode(w, r, &in) {
		return
	}
	var grace *int
	if in.AlertAfterSeconds != nil {
		if conn.MaintenanceUntil != nil {
			writeError(w, http.StatusConflict, "end the maintenance window first")
			return
		}
		if !slices.Contains(alertAfterChoicesFor(conn), *in.AlertAfterSeconds/60) || *in.AlertAfterSeconds%60 != 0 {
			writeError(w, http.StatusBadRequest, "unsupported delay")
			return
		}
		g := connect.GraceFor(time.Duration(*in.AlertAfterSeconds)*time.Second, checkInInterval(conn))
		grace = &g
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	hs, err := s.uptimyClient().UpdateHeartbeat(ctx, conn.APIKey, conn.HeartbeatUUID, grace, in.Paused)
	if err != nil {
		s.uptimyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(r.Context(), conn, hs))
}

// startUptimyMaintenance lets the agent be silent for a while without an alert, for
// planned work on the agent or its server. Rather than pausing, it stretches
// the heartbeat's grace in Uptimy: if the agent doesn't come back, Uptimy
// still alerts once the window has passed, which a pause would never do.
func (s *Server) startUptimyMaintenance(w http.ResponseWriter, r *http.Request) {
	conn := s.connectionForSettings(w, r)
	if conn == nil {
		return
	}
	var in struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !slices.Contains(maintenanceChoices, in.Minutes) {
		writeError(w, http.StatusBadRequest, "unsupported maintenance window")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	client := s.uptimyClient()
	current, err := client.GetHeartbeatSettings(ctx, conn.APIKey, conn.HeartbeatUUID)
	if err != nil {
		s.uptimyError(w, err)
		return
	}
	// Extending a running window keeps the grace from before it started.
	if conn.MaintenanceUntil == nil {
		conn.NormalGraceSeconds = current.GraceSeconds
	}
	window := time.Duration(in.Minutes) * time.Minute
	grace := int(window.Seconds())
	hs, err := client.UpdateHeartbeat(ctx, conn.APIKey, conn.HeartbeatUUID, &grace, nil)
	if err != nil {
		s.uptimyError(w, err)
		return
	}
	until := time.Now().UTC().Add(window)
	conn.MaintenanceUntil = &until
	if err := s.Store.UpdateUptimyConnection(r.Context(), *conn); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Log.Info("maintenance window started", "until", until)
	writeJSON(w, http.StatusOK, s.settingsResponse(r.Context(), conn, hs))
}

func (s *Server) endUptimyMaintenance(w http.ResponseWriter, r *http.Request) {
	conn := s.connectionForSettings(w, r)
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	hs, err := s.restoreAfterMaintenance(ctx, conn)
	if err != nil {
		s.uptimyError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.settingsResponse(r.Context(), conn, hs))
}

// restoreAfterMaintenance puts the normal grace back and forgets the window.
func (s *Server) restoreAfterMaintenance(ctx context.Context, conn *connect.Connection) (connect.HeartbeatSettings, error) {
	client := s.uptimyClient()
	if conn.MaintenanceUntil == nil {
		return client.GetHeartbeatSettings(ctx, conn.APIKey, conn.HeartbeatUUID)
	}
	grace := conn.NormalGraceSeconds
	hs, err := client.UpdateHeartbeat(ctx, conn.APIKey, conn.HeartbeatUUID, &grace, nil)
	if err != nil {
		return hs, err
	}
	conn.MaintenanceUntil, conn.NormalGraceSeconds = nil, 0
	if err := s.Store.UpdateUptimyConnection(ctx, *conn); err != nil {
		return hs, err
	}
	s.Log.Info("maintenance window ended; normal alerting restored")
	return hs, nil
}

// RestorePaused carries a heartbeat paused from the agent over a restart, so
// the agent doesn't report check-ins that Uptimy rejects on purpose. Call it
// before the watchdog starts.
func (s *Server) RestorePaused(ctx context.Context) {
	if conn, err := s.Store.UptimyConnection(ctx); err == nil && conn != nil && conn.Paused {
		s.Watchdog.SetPaused(ctx, true)
	}
}

// RunUptimyMaintenance restores normal alerting when a maintenance window
// runs out, retrying on the next tick if Uptimy can't be reached.
func (s *Server) RunUptimyMaintenance(ctx context.Context) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.expireMaintenance(ctx)
	}
}

func (s *Server) expireMaintenance(ctx context.Context) {
	conn, err := s.Store.UptimyConnection(ctx)
	if err != nil || conn == nil || conn.MaintenanceUntil == nil || time.Now().Before(*conn.MaintenanceUntil) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if _, err := s.restoreAfterMaintenance(ctx, conn); err != nil {
		s.Log.Warn("couldn't restore normal alerting after maintenance; will retry", "err", err)
	}
}
