// Package api serves the JSON API used by the web UI and the public status page.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/uptimy/agent/internal/config"
	"github.com/uptimy/agent/internal/connect"
	"github.com/uptimy/agent/internal/discovery"
	"github.com/uptimy/agent/internal/events"
	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/notify"
	"github.com/uptimy/agent/internal/scheduler"
	"github.com/uptimy/agent/internal/store"
)

// Server holds the API's dependencies.
type Server struct {
	Config        config.Config
	Version       string
	Store         *store.Store
	Scheduler     *scheduler.Scheduler
	Sender        *notify.Sender
	Hub           *events.Hub
	Log           *slog.Logger
	Watchdog      *connect.Watchdog
	KubeAvailable bool
	// Discovery is nil unless Kubernetes discovery runs.
	Discovery *discovery.Discoverer
	// MCP is an optional independently authenticated evidence adapter.
	MCP http.Handler

	limiter *loginLimiter
	connect connectFlows
	// statusDomain is the status page's own hostname, or "".
	statusDomain atomic.Pointer[string]
}

// Handler returns the API routes plus ui for everything else.
func (s *Server) Handler(ui http.Handler) http.Handler {
	s.limiter = newLoginLimiter()
	mux := http.NewServeMux()
	if s.MCP != nil {
		mux.Handle("/mcp", s.MCP)
	}

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte("ok"))
	})

	// Public.
	mux.HandleFunc("GET /api/auth/state", s.authState)
	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	mux.HandleFunc("GET /api/status", s.publicStatus)
	mux.HandleFunc("GET /api/status/logo/{variant}", s.statusPageLogo)
	s.pingRoutes(mux)  // heartbeat pings: /ping/<token>
	s.badgeRoutes(mux) // README badges: /badge/...

	// Authenticated.
	auth := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAuth(h)) }
	auth("GET /api/info", s.info)
	auth("GET /metrics", s.metrics)
	auth("POST /api/auth/password", s.changePassword)
	auth("GET /api/auth/2fa", s.twoFactorStatus)
	auth("POST /api/auth/2fa/setup", s.setupTwoFactor)
	auth("POST /api/auth/2fa/enable", s.enableTwoFactor)
	auth("POST /api/auth/2fa/disable", s.disableTwoFactor)
	auth("POST /api/auth/2fa/recovery-codes", s.newRecoveryCodesHandler)
	auth("GET /api/auth/sessions", s.sessions)
	auth("POST /api/auth/sessions/sign-out-others", s.signOutOthers)
	auth("GET /api/auth/tokens", s.listAPITokens)
	auth("POST /api/auth/tokens", s.createAPIToken)
	auth("DELETE /api/auth/tokens/{id}", s.deleteAPIToken)
	auth("GET /api/events", s.stream)

	auth("GET /api/kubernetes/discovery", s.kubernetesStatus)
	auth("GET /api/kubernetes/resources", s.kubernetesResources)

	auth("GET /api/check-types", s.checkTypes)
	auth("GET /api/notifier-types", s.notifierTypes)

	auth("GET /api/healthchecks", s.listHealthchecks)
	auth("POST /api/healthchecks", s.createHealthcheck)
	auth("GET /api/healthchecks/{id}", s.getHealthcheck)
	auth("PUT /api/healthchecks/{id}", s.updateHealthcheck)
	auth("DELETE /api/healthchecks/{id}", s.deleteMonitor(monitor.KindHealthcheck))
	auth("POST /api/healthchecks/{id}/pause", s.pauseMonitor(monitor.KindHealthcheck))
	auth("POST /api/healthchecks/{id}/check", s.checkNow)
	auth("GET /api/healthchecks/{id}/results", s.healthcheckResults)

	auth("GET /api/heartbeats", s.listHeartbeats)
	auth("POST /api/heartbeats", s.createHeartbeat)
	auth("POST /api/heartbeats/preview", s.previewSchedule)
	auth("GET /api/heartbeats/{id}", s.getHeartbeat)
	auth("PUT /api/heartbeats/{id}", s.updateHeartbeat)
	auth("DELETE /api/heartbeats/{id}", s.deleteMonitor(monitor.KindHeartbeat))
	auth("POST /api/heartbeats/{id}/pause", s.pauseMonitor(monitor.KindHeartbeat))
	auth("POST /api/heartbeats/{id}/token", s.rotateToken)
	auth("GET /api/heartbeats/{id}/runs", s.heartbeatRuns)

	auth("GET /api/maintenance", s.listMaintenance)
	auth("POST /api/maintenance", s.createMaintenance)
	auth("PUT /api/maintenance/{id}", s.updateMaintenance)
	auth("POST /api/maintenance/{id}/end", s.endMaintenance)
	auth("DELETE /api/maintenance/{id}", s.deleteMaintenance)

	auth("GET /api/incidents", s.listIncidents)
	auth("POST /api/incidents", s.createIncident)
	auth("PUT /api/incidents/{id}", s.updateIncident)
	auth("DELETE /api/incidents/{id}", s.deleteIncident)
	auth("POST /api/incidents/{id}/updates", s.addIncidentUpdate)
	auth("PUT /api/incidents/{id}/updates/{update}", s.editIncidentUpdate)
	auth("DELETE /api/incidents/{id}/updates/{update}", s.deleteIncidentUpdate)

	auth("GET /api/monitors/{id}/events", s.monitorEvents)
	auth("GET /api/activity", s.activity)

	auth("GET /api/uptimy/heartbeat", s.heartbeatStatus)
	auth("PUT /api/uptimy/heartbeat", s.connectHeartbeat)
	auth("DELETE /api/uptimy/heartbeat", s.disconnectHeartbeat)
	auth("POST /api/uptimy/heartbeat/test", s.testHeartbeat)
	auth("GET /api/uptimy/heartbeat/settings", s.getHeartbeatSettings)
	auth("PUT /api/uptimy/heartbeat/settings", s.updateHeartbeatSettings)
	auth("POST /api/uptimy/heartbeat/maintenance", s.startUptimyMaintenance)
	auth("DELETE /api/uptimy/heartbeat/maintenance", s.endUptimyMaintenance)
	auth("POST /api/uptimy/connect/start", s.startConnect)
	auth("POST /api/uptimy/connect/finish", s.finishConnect)

	auth("GET /api/status-page", s.getStatusPageConfig)
	auth("PUT /api/status-page", s.saveStatusPageConfig)
	auth("GET /api/status-page/announcement", s.getAnnouncement)
	auth("PUT /api/status-page/announcement", s.putAnnouncement)
	auth("DELETE /api/status-page/announcement", s.deleteAnnouncement)
	auth("PUT /api/status-page/logo/{variant}", s.putStatusPageLogo)
	auth("DELETE /api/status-page/logo/{variant}", s.deleteStatusPageLogo)

	admin := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.requireAdmin(h)) }
	admin("GET /api/users", s.listUsers)
	admin("POST /api/users", s.createUser)
	admin("PUT /api/users/{id}", s.updateUser)
	admin("DELETE /api/users/{id}", s.deleteUser)
	admin("POST /api/users/{id}/2fa/reset", s.resetTwoFactor)

	// The plan includes the channels' secrets (webhook URLs, keys).
	admin("POST /api/import/kuma", s.planKumaImport)
	admin("POST /api/import/kuma/apply", s.applyKumaImport)

	auth("GET /api/notifiers", s.listNotifiers)
	auth("POST /api/notifiers", s.createNotifier)
	auth("PUT /api/notifiers/{id}", s.updateNotifier)
	auth("DELETE /api/notifiers/{id}", s.deleteNotifier)
	auth("POST /api/notifiers/{id}/test", s.testNotifier)

	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	}))
	mux.Handle("/", ui)

	if sp, err := s.Store.StatusPage(context.Background()); err != nil {
		s.Log.Error("reading the status page settings", "err", err)
	} else {
		s.setStatusDomain(sp.Domain)
	}
	return securityHeaders(s.statusDomainOnly(mux, ui))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		// SAMEORIGIN (not DENY) so the status page editor can show a live
		// preview of /status; other sites still can't frame the agent.
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.Log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func (s *Server) storeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	case errors.Is(err, store.ErrUnknownMonitor):
		writeError(w, http.StatusBadRequest, "a selected monitor no longer exists; reload and try again")
	default:
		s.internalError(w, r, err)
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

func isJSON(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Content-Type"), "application/json")
}

// preflighted reports whether a request has a body type that a cross-site
// form can't send without a CORS preflight, which the agent never grants:
// JSON, or a raw file upload. Requiring one for every change, together with
// SameSite cookies, blocks CSRF.
func preflighted(r *http.Request) bool {
	return isJSON(r) || strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream")
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	sp, err := s.Store.StatusPage(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":             s.Version,
		"kubernetes":          s.KubeAvailable,
		"discovery":           s.Discovery != nil,
		"uptimy_heartbeat":    s.Watchdog.Status().Enabled,
		"status_page_enabled": sp.Enabled,
		"status_page_title":   sp.Title,
		"monitors_file":       s.Config.MonitorsFile != "" || s.Config.MonitorsYAML != "",
		"retention_days":      s.Config.RetentionDays,
	})
}
