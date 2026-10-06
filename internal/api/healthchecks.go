package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/uptimy/agent/internal/investigation"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/store"
)

// recentResults is how many results a summary includes, for the result bars.
const recentResults = 40

// healthcheckSummary is a healthcheck with its current status and recent
// history, as lists and detail pages show it.
type healthcheckSummary struct {
	monitor.Monitor
	Status        monitor.Status   `json:"status"`
	Target        string           `json:"target"` // password masked
	LastResult    *monitor.Result  `json:"last_result"`
	Recent        []monitor.Result `json:"recent"`
	Uptime24h     *float64         `json:"uptime_24h"`
	AvgLatency24h *float64         `json:"avg_latency_24h"`
	InMaintenance bool             `json:"in_maintenance"`
}

func (s *Server) summarizeHealthcheck(r *http.Request, m monitor.Monitor) (healthcheckSummary, error) {
	status, inMaintenance := investigation.CurrentView(s.Scheduler, m)
	sum := healthcheckSummary{
		Monitor: redactForViewer(r, m), Status: status, Target: m.Describe(),
		InMaintenance: inMaintenance,
	}
	recent, err := s.Store.RecentResults(r.Context(), m.ID, recentResults)
	if err != nil {
		return sum, err
	}
	sum.Recent = recent
	if len(recent) > 0 {
		sum.LastResult = &recent[len(recent)-1]
	}
	u, err := s.Store.UptimeSince(r.Context(), m.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		return sum, err
	}
	sum.Uptime24h, sum.AvgLatency24h = u.Ratio, u.AvgLatencyMS
	return sum, nil
}

func (s *Server) listHealthchecks(w http.ResponseWriter, r *http.Request) {
	monitors, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := []healthcheckSummary{}
	for _, m := range monitors {
		if m.Kind != monitor.KindHealthcheck {
			continue
		}
		sum, err := s.summarizeHealthcheck(r, m)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out = append(out, sum)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getHealthcheck(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHealthcheck)
	if !ok {
		return
	}
	sum, err := s.summarizeHealthcheck(r, m)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	uptime := map[string]store.Uptime{}
	for label, d := range map[string]time.Duration{"24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour} {
		if uptime[label], err = s.Store.UptimeSince(r.Context(), m.ID, time.Now().Add(-d)); err != nil {
			s.internalError(w, r, err)
			return
		}
	}
	alerts, err := s.Store.MonitorNotifierIDs(r.Context(), m.ID)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"healthcheck": sum, "uptime": uptime, "notifier_ids": alerts})
}

// healthcheckInput is the editable part of a healthcheck.
type healthcheckInput struct {
	Name   string        `json:"name"`
	Paused bool          `json:"paused"`
	Check  monitor.Check `json:"check"`
	// NotifierIDs are the notifiers limited to some monitors that alert for
	// this one; omitted, they're left as they are.
	NotifierIDs *[]int64 `json:"notifier_ids"`
}

func (in healthcheckInput) apply(m *monitor.Monitor) {
	c := in.Check
	m.Kind, m.Name, m.Paused, m.Check = monitor.KindHealthcheck, in.Name, in.Paused, &c
}

func (s *Server) createHealthcheck(w http.ResponseWriter, r *http.Request) {
	var in healthcheckInput
	if !decode(w, r, &in) {
		return
	}
	var m monitor.Monitor
	in.apply(&m)
	m.Source = monitor.SourceUI
	s.saveMonitor(w, r, m, http.StatusCreated, in.NotifierIDs)
}

func (s *Server) updateHealthcheck(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHealthcheck)
	if !ok || rejectManaged(w, m, "edit") {
		return
	}
	var in healthcheckInput
	if !decode(w, r, &in) {
		return
	}
	in.apply(&m)
	s.saveMonitor(w, r, m, http.StatusOK, in.NotifierIDs)
}

// checkNow runs a healthcheck right away; the result arrives over the live
// update stream like any other.
func (s *Server) checkNow(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHealthcheck)
	if !ok {
		return
	}
	if !s.Scheduler.CheckNow(m.ID) {
		writeError(w, http.StatusConflict, "this healthcheck is paused")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) healthcheckResults(w http.ResponseWriter, r *http.Request) {
	m, ok := s.loadMonitor(w, r, monitor.KindHealthcheck)
	if !ok {
		return
	}
	hours, err := strconv.Atoi(r.URL.Query().Get("hours"))
	if err != nil || hours < 1 || hours > 24*90 {
		hours = 24
	}
	results, err := s.Store.ResultsSince(r.Context(), m.ID, time.Now().Add(-time.Duration(hours)*time.Hour))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}
