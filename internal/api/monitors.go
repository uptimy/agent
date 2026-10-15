package api

import (
	"errors"
	"net/http"

	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
	"github.com/uptimy/agent/internal/scheduler"
)

// What healthchecks and heartbeats share: loading one by kind, pausing,
// deleting, their events and the activity feed. Each kind's own endpoints
// are in healthchecks.go and heartbeats.go.

// monitorsChanged tells open UIs (other tabs, other users) to reload their
// monitor lists, since their shape changed rather than just their data.
func (s *Server) monitorsChanged(id int64) {
	s.Hub.Publish(events.Message{Type: "monitors", MonitorID: id})
}

// loadMonitor loads the monitor in the path, answering 404 if it doesn't
// exist or is of another kind (/api/heartbeats/7 when 7 is a healthcheck).
func (s *Server) loadMonitor(w http.ResponseWriter, r *http.Request, kind monitor.Kind) (monitor.Monitor, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return monitor.Monitor{}, false
	}
	m, err := s.Store.GetMonitor(r.Context(), id)
	if err != nil {
		s.storeError(w, r, err)
		return m, false
	}
	if m.Kind != kind {
		writeError(w, http.StatusNotFound, "not found")
		return m, false
	}
	return m, true
}

// redactForViewer hides the credentials a monitor may carry (auth headers,
// database passwords, a heartbeat's ping token) from read-only users.
func redactForViewer(r *http.Request, m monitor.Monitor) monitor.Monitor {
	if currentUser(r).IsAdmin() {
		return m
	}
	if m.Check != nil {
		c := *m.Check
		if len(c.Config.Headers) > 0 {
			hidden := make(map[string]string, len(c.Config.Headers))
			for k := range c.Config.Headers {
				hidden[k] = "••••••"
			}
			c.Config.Headers = hidden
		}
		c.Target = c.DisplayTarget()
		m.Check = &c
	}
	if m.Heartbeat != nil {
		h := *m.Heartbeat
		h.Token = ""
		m.Heartbeat = &h
	}
	return m
}

// saveMonitor validates and stores a created or edited monitor, and
// (re)starts watching it. It writes the response.
// saveMonitor validates and stores a monitor and (re)starts it. alerts, when
// set, are the notifiers limited to some monitors that should include it.
func (s *Server) saveMonitor(w http.ResponseWriter, r *http.Request, m monitor.Monitor, status int, alerts *[]int64) {
	if err := m.Normalize(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	var err error
	if m.ID == 0 {
		m, err = s.Store.CreateMonitor(r.Context(), m)
	} else {
		m, err = s.Store.UpdateMonitor(r.Context(), m)
	}
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	if alerts != nil {
		if err := s.Store.SetMonitorNotifiers(r.Context(), m.ID, *alerts); err != nil {
			s.storeError(w, r, err)
			return
		}
	}
	s.Scheduler.Upsert(m)
	s.monitorsChanged(m.ID)
	writeJSON(w, status, m)
}

// pauseMonitor pauses or resumes a monitor. File-managed monitors can be
// paused too (e.g. for maintenance); the next start re-applies the file.
func (s *Server) pauseMonitor(kind monitor.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, ok := s.loadMonitor(w, r, kind)
		if !ok {
			return
		}
		var in struct {
			Paused bool `json:"paused"`
		}
		if !decode(w, r, &in) {
			return
		}
		m.Paused = in.Paused
		s.saveMonitor(w, r, m, http.StatusOK, nil)
	}
}

// acknowledgeMonitor records that the signed-in user is on a monitor's
// outage: reminders stop, and chat channels hear who took it on.
func (s *Server) acknowledgeMonitor(kind monitor.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, ok := s.loadMonitor(w, r, kind)
		if !ok {
			return
		}
		err := s.Scheduler.Acknowledge(m, currentUser(r).Username)
		if errors.Is(err, scheduler.ErrNotDown) {
			writeError(w, http.StatusConflict, "only an outage can be acknowledged, and "+m.Name+" isn't down")
			return
		}
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) deleteMonitor(kind monitor.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m, ok := s.loadMonitor(w, r, kind)
		if !ok {
			return
		}
		if rejectManaged(w, m, "delete") {
			return
		}
		s.Scheduler.Remove(m.ID)
		if err := s.Store.DeleteMonitor(r.Context(), m.ID); err != nil {
			s.storeError(w, r, err)
			return
		}
		s.monitorsChanged(m.ID)
		w.WriteHeader(http.StatusNoContent)
	}
}

// rejectManaged refuses to edit or delete monitors defined outside the UI:
// their source would overwrite the change (the file on the next start,
// discovery on its next scan).
func rejectManaged(w http.ResponseWriter, m monitor.Monitor, verb string) bool {
	switch {
	case m.Source == monitor.SourceFile && verb == "delete":
		writeError(w, http.StatusConflict, "this is defined in the monitors file; remove it there")
	case m.Source == monitor.SourceFile:
		writeError(w, http.StatusConflict, "this is defined in the monitors file; edit it there")
	case m.Source == monitor.SourceKubernetes && verb == "delete":
		writeError(w, http.StatusConflict, "this was discovered in Kubernetes; remove the upti.my/monitor label from the resource to delete it")
	case m.Source == monitor.SourceKubernetes:
		writeError(w, http.StatusConflict, "this was discovered in Kubernetes; change its upti.my/ annotations to edit it")
	default:
		return false
	}
	return true
}

func (s *Server) monitorEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	events, err := s.Store.ListEvents(r.Context(), id, 50)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	ids := make([]int64, len(events))
	for i, e := range events {
		ids[i] = e.ID
	}
	deliveries, err := s.Store.EventDeliveries(r.Context(), ids)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	out := make([]eventWithAlerts, len(events))
	for i, e := range events {
		out[i] = eventWithAlerts{Event: e, Deliveries: deliveries[e.ID]}
	}
	writeJSON(w, http.StatusOK, out)
}

// eventWithAlerts is a status change and how its alert went on each
// channel; Deliveries is empty for changes that don't alert.
type eventWithAlerts struct {
	monitor.Event
	Deliveries []notify.Delivery `json:"deliveries,omitempty"`
}

// activityItem is an event with the monitor it belongs to.
type activityItem struct {
	monitor.Event
	MonitorName string       `json:"monitor_name"`
	Kind        monitor.Kind `json:"kind"`
}

// activity returns the latest status changes across all monitors.
func (s *Server) activity(w http.ResponseWriter, r *http.Request) {
	events, err := s.Store.ListEvents(r.Context(), 0, 30)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	monitors, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	byID := make(map[int64]monitor.Monitor, len(monitors))
	for _, m := range monitors {
		byID[m.ID] = m
	}
	out := make([]activityItem, 0, len(events))
	for _, e := range events {
		m := byID[e.MonitorID]
		out = append(out, activityItem{Event: e, MonitorName: m.Name, Kind: m.Kind})
	}
	writeJSON(w, http.StatusOK, out)
}
