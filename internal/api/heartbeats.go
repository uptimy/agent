package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/uptimy/agent/internal/investigation"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/scheduler"
	"github.com/uptimy/agent/internal/store"
)

// recentRuns is how many runs a summary includes, for the run bars.
const recentRuns = 30

// heartbeatSummary is a heartbeat with where it stands against its schedule
// and its recent runs, as lists and detail pages show it.
type heartbeatSummary struct {
	monitor.Monitor
	Status   monitor.Status            `json:"status"`
	Schedule string                    `json:"schedule"` // in words
	Tracking scheduler.HeartbeatStatus `json:"tracking"`
	LastRun  *monitor.Run              `json:"last_run"`
	Recent   []monitor.Run             `json:"recent"`
	Stats30d store.RunStats            `json:"stats_30d"`

	InMaintenance bool `json:"in_maintenance"`
}

func (s *Server) summarizeHeartbeat(r *http.Request, m monitor.Monitor) (heartbeatSummary, error) {
	status, inMaintenance := investigation.CurrentView(s.Scheduler, m)
	sum := heartbeatSummary{
		Monitor: redactForViewer(r, m), Status: status,
		Schedule: m.Heartbeat.Describe(), Tracking: s.Scheduler.Heartbeat(m),
		InMaintenance: inMaintenance,
	}
	recent, err := s.Store.RecentRuns(r.Context(), m.ID, recentRuns)
	if err != nil {
		return sum, err
	}
	sum.Recent = recent
	if last, err := s.Store.LastRun(r.Context(), m.ID); err == nil {
		sum.LastRun = &last
	} else if !errors.Is(err, store.ErrNotFound) {
		return sum, err
	}
	sum.Stats30d, err = s.Store.RunStatsSince(r.Context(), m.ID, time.Now().AddDate(0, 0, -30))
	return sum, err
}

func (s *Server) listHeartbeats(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := []heartbeatSummary{}
	for _, m := range monitors {
		if m.Kind != monitor.KindHeartbeat {
			continue
		}
		sum, err := s.summarizeHeartbeat(r, m)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getHeartbeat(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHeartbeat)
	if !ok {
		return
	}
	sum, err := s.summarizeHeartbeat(r, m)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	stats := map[string]store.RunStats{}
	for label, d := range map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour} {
		if stats[label], err = s.Store.RunStatsSince(r.Context(), m.ID, time.Now().Add(-d)); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	daily, err := s.Store.DailyOnTime(r.Context(), m.ID, 30)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// The next three runs, starting with the one being waited for.
	upcoming := []time.Time{}
	if due := sum.Tracking.DueAt; !due.IsZero() {
		upcoming = append(upcoming, due)
		upcoming = append(upcoming, m.Heartbeat.Upcoming(due, 2)...)
	}
	alerts, err := s.Store.MonitorNotifierIDs(r.Context(), m.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"heartbeat": sum, "stats": stats, "daily": daily, "upcoming": upcoming, "notifier_ids": alerts,
	})
}

// heartbeatInput is the editable part of a heartbeat. The ping token isn't:
// it's generated, kept across edits and changed with rotateToken.
type heartbeatInput struct {
	Name      string            `json:"name"`
	Paused    bool              `json:"paused"`
	Heartbeat monitor.Heartbeat `json:"heartbeat"`
	// NotifierIDs: as for healthchecks.
	NotifierIDs *[]int64 `json:"notifier_ids"`
}

func (in heartbeatInput) apply(m *monitor.Monitor) {
	h := in.Heartbeat
	if m.Heartbeat != nil {
		h.Token = m.Heartbeat.Token
	} else {
		h.Token = monitor.NewToken()
	}
	m.Kind, m.Name, m.Paused, m.Heartbeat = monitor.KindHeartbeat, in.Name, in.Paused, &h
}

func (s *Server) createHeartbeat(w http.ResponseWriter, r *http.Request) {
	var in heartbeatInput
	if !decode(w, r, &in) {
		return
	}
	var m monitor.Monitor
	in.apply(&m)
	m.Source = monitor.SourceUI
	s.saveMonitor(w, r, m, http.StatusCreated, in.NotifierIDs)
}

func (s *Server) updateHeartbeat(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHeartbeat)
	if !ok || rejectManaged(w, m, "edit") {
		return
	}
	var in heartbeatInput
	if !decode(w, r, &in) {
		return
	}
	in.apply(&m)
	s.saveMonitor(w, r, m, http.StatusOK, in.NotifierIDs)
}

// rotateToken gives a heartbeat a new ping URL, e.g. after the old one
// leaked. The old URL stops working right away.
func (s *Server) rotateToken(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHeartbeat)
	if !ok || rejectManaged(w, m, "edit") {
		return
	}
	m.Heartbeat.Token = monitor.NewToken()
	s.saveMonitor(w, r, m, http.StatusOK, nil)
}

func (s *Server) heartbeatRuns(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHeartbeat)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit < 1 || limit > 500 {
		limit = 100
	}
	runs, err := s.Store.RecentRuns(r.Context(), m.ID, limit)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

// previewSchedule checks a schedule as it's typed and says when it would
// run, so the form can show "next runs: …" before saving.
func (s *Server) previewSchedule(w http.ResponseWriter, r *http.Request) {
	var h monitor.Heartbeat
	if !decode(w, r, &h) {
		return
	}
	h.Token = monitor.NewToken() // not part of the preview
	m := monitor.Monitor{Kind: monitor.KindHeartbeat, Name: "preview", Heartbeat: &h}
	if err := m.Normalize(); err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"schedule": h.Describe(),
		"upcoming": h.Upcoming(time.Now(), 3),
	})
}
