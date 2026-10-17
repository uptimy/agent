package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/uptimy/agent/internal/incident"
	"github.com/uptimy/agent/internal/maintenance"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/statuspage"
	"github.com/uptimy/agent/internal/store"
)

const statusPageDays = 30

// publicMonitor deliberately omits the target: in-cluster targets are
// internal hostnames that shouldn't leak onto a public page.
type publicMonitor struct {
	Name       string         `json:"name"`
	Kind       monitor.Kind   `json:"kind"`
	TypeLabel  string         `json:"type_label"`
	Status     monitor.Status `json:"status"`
	LastChange *time.Time     `json:"last_change"`
	// Ratio is how well it did over the period: a healthcheck's uptime, or
	// the share of a heartbeat's runs that succeeded on time. Days is the
	// same per day.
	Ratio *float64      `json:"ratio"`
	Days  []store.Daily `json:"days"`
	// LastRun is when a heartbeat's job last finished.
	LastRun *time.Time `json:"last_run,omitempty"`
	// InMaintenance is set during a public maintenance window.
	InMaintenance bool `json:"in_maintenance,omitempty"`
}

// publicMaintenance announces planned work. Monitors are the public names of
// those it covers; empty means all of them.
type publicMaintenance struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	StartsAt    time.Time         `json:"starts_at"`
	EndsAt      time.Time         `json:"ends_at"`
	CreatedAt   time.Time         `json:"created_at"`
	State       maintenance.State `json:"state"`
	Active      bool              `json:"active"`
	Monitors    []string          `json:"monitors"`
}

// maintenanceNotice is how far ahead the status page announces maintenance,
// and how long it keeps showing it once completed.
const maintenanceNotice = 7 * 24 * time.Hour

type publicSection struct {
	Name     string          `json:"name"`
	Monitors []publicMonitor `json:"monitors"`
}

// publicEvent is a monitor going down or recovering.
type publicEvent struct {
	Monitor string         `json:"monitor"`
	Status  monitor.Status `json:"status"`
	Time    time.Time      `json:"time"`
}

// publicIncident is a posted incident. Monitors are the public names of
// the affected monitors on the page.
type publicIncident struct {
	Title      string            `json:"title"`
	Severity   incident.Severity `json:"severity,omitempty"`
	Status     incident.Status   `json:"status"`
	Monitors   []string          `json:"monitors"`
	CreatedAt  time.Time         `json:"created_at"`
	ResolvedAt *time.Time        `json:"resolved_at,omitempty"`
	Updates    []publicUpdate    `json:"updates"`
}

type publicUpdate struct {
	Status  incident.Status `json:"status,omitempty"`
	Message string          `json:"message"`
	Time    time.Time       `json:"time"`
}

func (s *Server) publicStatus(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Store.StatusPage(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !settings.Enabled {
		writeError(w, http.StatusNotFound, "status page is disabled")
		return
	}
	monitors, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	since := time.Now().Add(-statusPageDays * 24 * time.Hour)
	names := map[int64]string{}
	bySection := map[string][]publicMonitor{}
	anyDown := false
	for _, m := range statuspage.Ordered(monitors) {
		if !m.Public {
			continue
		}
		names[m.ID] = m.PublicName()
		pm := publicMonitor{Name: m.PublicName(), Kind: m.Kind, TypeLabel: typeLabel(m), Status: s.Scheduler.Status(m)}
		if e, err := s.Store.LastEvent(r.Context(), m.ID); err == nil {
			pm.LastChange = &e.Time
		} else if !store.IsNotFound(err) {
			s.internalError(w, r, err)
			return
		}
		if err := s.publicHistory(r, m, since, &pm); err != nil {
			s.internalError(w, r, err)
			return
		}
		pm.InMaintenance = s.publicMaintenanceCovers(m.ID)
		anyDown = anyDown || (pm.Status == monitor.StatusDown && !pm.InMaintenance)
		sec := settings.SectionOf(m)
		bySection[sec] = append(bySection[sec], pm)
	}
	// Sections in the editor's order; empty ones aren't shown.
	sections := []publicSection{}
	for _, sec := range settings.Sections {
		if ms := bySection[sec.ID]; len(ms) > 0 {
			sections = append(sections, publicSection{Name: sec.Name, Monitors: ms})
		}
	}
	logos, err := s.statusPageLogos(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	events, err := s.Store.ListEvents(r.Context(), 0, 200)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// Events are newest first. Show every "down", and an "up" only when it
	// ends an outage; the first successful check after startup isn't news.
	// Walking newest→oldest, an up is a recovery if the next older
	// up/down event for that monitor is a down.
	changes := []publicEvent{}
	if !settings.ShowEvents {
		events = nil
	}
	pendingUp := map[int64]int{} // monitor → index of an up awaiting its predecessor
	for _, e := range events {
		name, ok := names[e.MonitorID]
		if !ok || (e.Status != monitor.StatusDown && e.Status != monitor.StatusUp) {
			continue
		}
		if i, ok := pendingUp[e.MonitorID]; ok && e.Status == monitor.StatusUp {
			changes[i].Status = "" // previous event was up too: not a recovery
		}
		delete(pendingUp, e.MonitorID)
		changes = append(changes, publicEvent{Monitor: name, Status: e.Status, Time: e.Time})
		if e.Status == monitor.StatusUp {
			pendingUp[e.MonitorID] = len(changes) - 1
		}
	}
	for _, i := range pendingUp {
		changes[i].Status = "" // no older down: first check, not a recovery
	}
	shown := []publicEvent{}
	for _, inc := range changes {
		if inc.Status != "" {
			shown = append(shown, inc)
		}
		if len(shown) == 20 {
			break
		}
	}

	posted, err := s.Store.ListIncidents(r.Context(), time.Now().Add(-incidentHistory))
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	published := publicIncidents(posted, names)
	announcement, err := s.Store.Announcement(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if !announcement.ShownAt(time.Now()) {
		announcement = nil
	}
	notices, err := s.publicMaintenance(r.Context(), names)
	if err != nil {
		s.internalError(w, r, err)
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=30")
	writeJSON(w, http.StatusOK, map[string]any{
		"title":        settings.Title,
		"description":  settings.Description,
		"logos":        logos,
		"accent_color": settings.AccentColor,
		"website_url":  settings.WebsiteURL,
		"overall":      overallStatus(anyDown, published, notices),
		"maintenance":  notices,
		"sections":     sections,
		"announcement": announcement,
		"incidents":    published,
		"events":       shown,
		"updated":      time.Now().UTC(),
	})
}

// overallStatus is the page's headline, ordered like the hosted Uptimy status
// pages: a critical or high incident is an outage (it was reported on
// purpose, maintenance or not); so is a monitor that's down outside its
// announced maintenance; then maintenance in progress; then any other open
// incident is "degraded".
func overallStatus(anyDown bool, incidents []publicIncident, maintenance []publicMaintenance) string {
	major, open := false, false
	for _, inc := range incidents {
		if inc.ResolvedAt != nil {
			continue
		}
		open = true
		if inc.Severity == incident.Critical || inc.Severity == incident.High {
			major = true
		}
	}
	if major || anyDown {
		return "outage"
	}
	for _, n := range maintenance {
		if n.Active {
			return "maintenance"
		}
	}
	if open {
		return "degraded"
	}
	return "operational"
}

// pageOverall is overallStatus for the page as it is now, without the
// history the page itself shows.
func (s *Server) pageOverall(ctx context.Context) (string, error) {
	monitors, err := s.Store.ListMonitors(ctx)
	if err != nil {
		return "", err
	}
	names := map[int64]string{}
	anyDown := false
	for _, m := range monitors {
		if !m.Public {
			continue
		}
		names[m.ID] = m.PublicName()
		anyDown = anyDown || (s.Scheduler.Status(m) == monitor.StatusDown && !s.publicMaintenanceCovers(m.ID))
	}
	posted, err := s.Store.ListIncidents(ctx, time.Now())
	if err != nil {
		return "", err
	}
	notices, err := s.publicMaintenance(ctx, names)
	if err != nil {
		return "", err
	}
	return overallStatus(anyDown, publicIncidents(posted, names), notices), nil
}

// publicIncidents is what the page shows of posted incidents: open and
// recently resolved ones.
func publicIncidents(posted []incident.Incident, names map[int64]string) []publicIncident {
	out := []publicIncident{}
	for _, in := range posted {
		p := publicIncident{
			Title: in.Title, Severity: in.Severity, Status: in.Status,
			Monitors: []string{}, CreatedAt: in.CreatedAt, ResolvedAt: in.ResolvedAt, Updates: []publicUpdate{},
		}
		for _, id := range in.MonitorIDs {
			if name, ok := names[id]; ok {
				p.Monitors = append(p.Monitors, name)
			}
		}
		for _, u := range in.Updates {
			p.Updates = append(p.Updates, publicUpdate{Status: u.Status, Message: u.Message, Time: u.CreatedAt})
		}
		out = append(out, p)
	}
	return out
}

// stream pushes live updates to the UI over Server-Sent Events.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx, some PaaS routers)
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	msgs, unsubscribe := s.Hub.Subscribe()
	defer unsubscribe()
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
		case m, ok := <-msgs:
			if !ok {
				return
			}
			data, err := json.Marshal(m)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()
	}
}

// publicHistory fills in how a monitor did over the period, in the measure
// that fits its kind.
func (s *Server) publicHistory(r *http.Request, m monitor.Monitor, since time.Time, pm *publicMonitor) error {
	ctx := r.Context()
	var err error
	if m.Kind == monitor.KindHeartbeat {
		if pm.Days, err = s.Store.DailyOnTime(ctx, m.ID, statusPageDays); err != nil {
			return err
		}
		stats, err := s.Store.RunStatsSince(ctx, m.ID, since)
		if err != nil {
			return err
		}
		pm.Ratio = stats.Ratio
		if last, err := s.Store.LastRun(ctx, m.ID); err == nil {
			pm.LastRun = last.FinishedAt
		} else if !store.IsNotFound(err) {
			return err
		}
		return nil
	}
	if pm.Days, err = s.Store.DailyUptimes(ctx, m.ID, statusPageDays); err != nil {
		return err
	}
	u, err := s.Store.UptimeSince(ctx, m.ID, since)
	pm.Ratio = u.Ratio
	return err
}

// publicMaintenanceCovers reports whether a public window covers a monitor
// now. Private windows only silence alerts.
func (s *Server) publicMaintenanceCovers(monitorID int64) bool {
	for _, w := range s.Scheduler.Maintenance().Covering(monitorID, time.Now()) {
		if w.Public {
			return true
		}
	}
	return false
}

// publicMaintenance lists public windows that start within maintenanceNotice,
// are active, or ended within it, and cover a monitor on the page. names maps the page's
// monitors to their public names.
func (s *Server) publicMaintenance(ctx context.Context, names map[int64]string) ([]publicMaintenance, error) {
	now := time.Now()
	ws, err := s.Store.ListMaintenance(ctx, now.Add(-maintenanceNotice))
	if err != nil {
		return nil, err
	}
	out := []publicMaintenance{}
	for _, w := range ws {
		state := w.StateAt(now)
		if !w.Public || w.StartsAt.Sub(now) > maintenanceNotice || now.Sub(w.EndsAt) > maintenanceNotice {
			continue
		}
		notice := publicMaintenance{
			Title: w.Title, Description: w.Description, StartsAt: w.StartsAt, EndsAt: w.EndsAt, CreatedAt: w.CreatedAt,
			State: state, Active: state == maintenance.Active, Monitors: []string{},
		}
		if !w.AllMonitors {
			for _, id := range w.MonitorIDs {
				if name, ok := names[id]; ok {
					notice.Monitors = append(notice.Monitors, name)
				}
			}
			if len(notice.Monitors) == 0 {
				continue // covers nothing on the page
			}
		}
		out = append(out, notice)
	}
	return out, nil
}
