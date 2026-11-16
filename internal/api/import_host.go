package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/railway"
	"github.com/uptimy/agent/internal/render"
)

// Importing from Railway or Render works like importing from Kuma: the
// project token or API key plans the monitors (planRailwayImport,
// planRenderImport), then the reviewed ones are sent back
// (applyHostImport). The token is used for the plan only, and never stored
// or logged.

func (s *Server) planRailwayImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Token) == "" {
		writeError(w, http.StatusBadRequest, "enter a project token")
		return
	}
	plan, err := railway.Read(r.Context(), railway.NewClient(s.RailwayURL, strings.TrimSpace(in.Token)), railway.SelfFromEnv())
	switch {
	case errors.Is(err, railway.ErrToken):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !s.withoutImported(w, r, &plan.Services, &plan.Skipped) {
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) planRenderImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Token) == "" {
		writeError(w, http.StatusBadRequest, "enter an API key")
		return
	}
	plan, err := render.Read(r.Context(), render.NewClient(s.RenderURL, strings.TrimSpace(in.Token)), render.SelfFromEnv())
	switch {
	case errors.Is(err, render.ErrToken):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if !s.withoutImported(w, r, &plan.Services, &plan.Skipped) {
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

// withoutImported moves services whose monitor already exists to skipped.
// False when it answered with an error.
func (s *Server) withoutImported(w http.ResponseWriter, r *http.Request, services *[]railway.Service, skipped *[]railway.Skipped) bool {
	existing, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return false
	}
	imported := map[string]bool{}
	for _, m := range existing {
		if m.SourceRef != "" {
			imported[m.SourceRef] = true
		}
	}
	kept := (*services)[:0]
	for _, svc := range *services {
		if imported[svc.Monitor.SourceRef] {
			*skipped = append(*skipped, railway.Skipped{What: "service", Name: svc.Monitor.Name, Reason: "already imported"})
			continue
		}
		kept = append(kept, svc)
	}
	*services = kept
	return true
}

// applyHostImport creates the reviewed monitors of a Railway or Render plan.
func (s *Server) applyHostImport(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Services []railway.Service `json:"services"`
	}
	if !decode(w, r, &in) {
		return
	}
	existing, err := s.Store.ListMonitors(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	names, refs := map[string]bool{}, map[string]bool{}
	for _, m := range existing {
		names[strings.ToLower(m.Name)] = true
		if m.SourceRef != "" {
			refs[m.SourceRef] = true
		}
	}

	out := struct {
		Monitors []importedItem    `json:"monitors"`
		Skipped  []railway.Skipped `json:"skipped"`
	}{[]importedItem{}, []railway.Skipped{}}
	skip := func(name, reason string) {
		out.Skipped = append(out.Skipped, railway.Skipped{What: "service", Name: name, Reason: reason})
	}
	for _, svc := range in.Services {
		m := svc.Monitor
		m.ID, m.Source = 0, monitor.SourceUI
		if m.Heartbeat != nil {
			m.Heartbeat.Token = monitor.NewToken()
		}
		switch {
		case m.SourceRef != "" && refs[m.SourceRef]:
			skip(m.Name, "already imported")
			continue
		case names[strings.ToLower(strings.TrimSpace(m.Name))]:
			skip(m.Name, "a monitor with this name already exists")
			continue
		}
		if err := m.Normalize(); err != nil {
			skip(m.Name, err.Error())
			continue
		}
		created, err := s.Store.CreateMonitor(r.Context(), m)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		names[strings.ToLower(created.Name)], refs[created.SourceRef] = true, true
		s.Scheduler.Upsert(created)
		out.Monitors = append(out.Monitors, importedItem{ID: created.ID, Name: created.Name, Kind: created.Kind})
	}
	s.monitorsChanged(0)
	writeJSON(w, http.StatusOK, out)
}
