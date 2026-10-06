package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/uptimy/agent/internal/connect"
)

const (
	// connectCallbackPath must match AGENT_CALLBACK_PATH in upti.my-app.
	connectCallbackPath = "/uptimy/connected"
	connectStateTTL     = 10 * time.Minute
)

func randomToken(n int) string {
	buf := make([]byte, n)
	rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

type heartbeatStatusResponse struct {
	connect.Status
	// Account is set when connected through "Connect to Uptimy".
	Account *struct {
		WorkspaceName string    `json:"workspace_name"`
		ConnectedAt   time.Time `json:"connected_at"`
		ConnectedBy   string    `json:"connected_by"`
	} `json:"account"`
}

func (s *Server) heartbeatStatusFor(r *http.Request) (heartbeatStatusResponse, error) {
	resp := heartbeatStatusResponse{Status: s.Watchdog.Status()}
	if !currentUser(r).IsAdmin() {
		resp.URL = "" // the URL embeds the heartbeat's secret token
	}
	if resp.ManagedByEnv {
		return resp, nil
	}
	conn, err := s.Store.UptimyConnection(r.Context())
	if err != nil || conn == nil {
		return resp, err
	}
	resp.Account = &struct {
		WorkspaceName string    `json:"workspace_name"`
		ConnectedAt   time.Time `json:"connected_at"`
		ConnectedBy   string    `json:"connected_by"`
	}{conn.WorkspaceName, conn.ConnectedAt, conn.ConnectedBy}
	return resp, nil
}

func (s *Server) writeHeartbeatStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := s.heartbeatStatusFor(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) heartbeatStatus(w http.ResponseWriter, r *http.Request) {
	s.writeHeartbeatStatus(w, r)
}

func (s *Server) rejectIfEnvManaged(w http.ResponseWriter) bool {
	if s.Watchdog.ManagedByEnv() {
		writeError(w, http.StatusConflict, "the heartbeat is set by the UPTIMY_HEARTBEAT_URL environment variable")
		return true
	}
	return false
}

// connectHeartbeat verifies a pasted heartbeat URL (or bare token) by sending
// a check-in, and saves it only once Uptimy accepts it. This is the manual
// alternative to "Connect to Uptimy".
func (s *Server) connectHeartbeat(w http.ResponseWriter, r *http.Request) {
	if s.rejectIfEnvManaged(w) {
		return
	}
	var in struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &in) {
		return
	}
	target, err := connect.NormalizeHeartbeatURL(in.URL)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	if err := s.Watchdog.Connect(ctx, target); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.Store.SetHeartbeatURL(r.Context(), target); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.writeHeartbeatStatus(w, r)
}

// pendingConnect is a "Connect to Uptimy" handoff in progress. The state is
// only valid for the admin who started it, once, for a few minutes. The PKCE
// verifier never leaves the agent's server.
type pendingConnect struct {
	userID      int64
	expires     time.Time
	verifier    string
	redirectURI string
}

type connectFlows struct {
	mu      sync.Mutex
	pending map[string]pendingConnect // by state
}

func (f *connectFlows) add(state string, p pendingConnect) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pending == nil {
		f.pending = map[string]pendingConnect{}
	}
	now := time.Now()
	for k, p := range f.pending {
		if now.After(p.expires) {
			delete(f.pending, k)
		}
	}
	p.expires = now.Add(connectStateTTL)
	f.pending[state] = p
}

// take consumes a state if it exists, belongs to userID and hasn't expired.
func (f *connectFlows) take(state string, userID int64) (pendingConnect, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, p := range f.pending {
		if subtle.ConstantTimeCompare([]byte(k), []byte(state)) == 1 {
			delete(f.pending, k)
			return p, p.userID == userID && time.Now().Before(p.expires)
		}
	}
	return pendingConnect{}, false
}

// startConnect begins "Connect to Uptimy" (OAuth with PKCE): it returns the
// Uptimy consent page to open. The browser comes back to
// <origin>/uptimy/connected with a one-time code and the state, which the page
// hands to finishConnect.
func (s *Server) startConnect(w http.ResponseWriter, r *http.Request) {
	if s.rejectIfEnvManaged(w) {
		return
	}
	var in struct {
		Origin string `json:"origin"`
	}
	if !decode(w, r, &in) {
		return
	}
	// The origin comes from the admin's own browser (window.location.origin):
	// the address they reach the agent at, which the server can't know.
	origin, err := url.Parse(in.Origin)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Host == "" ||
		(origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.User != nil {
		writeError(w, http.StatusBadRequest, "invalid origin")
		return
	}

	state := randomToken(32)
	verifier, challenge := connect.NewPKCE()
	redirectURI := origin.Scheme + "://" + origin.Host + connectCallbackPath
	s.connect.add(state, pendingConnect{userID: currentUser(r).ID, verifier: verifier, redirectURI: redirectURI})

	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", connect.OAuthClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	q.Set("host", s.Config.AgentName)
	writeJSON(w, http.StatusOK, map[string]string{
		"authorize_url": strings.TrimRight(s.Config.UptimyAppURL, "/") + "/oauth/authorize?" + q.Encode(),
	})
}

// finishConnect completes "Connect to Uptimy" with the code the consent page
// sent back: it exchanges the code for the agent key, verifies the key,
// creates (or reclaims) the agent's heartbeat, checks in once, and saves the
// connection.
func (s *Server) finishConnect(w http.ResponseWriter, r *http.Request) {
	if s.rejectIfEnvManaged(w) {
		return
	}
	var in struct {
		State string `json:"state"`
		Code  string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	user := currentUser(r)
	pending, ok := s.connect.take(in.State, user.ID)
	if !ok {
		writeError(w, http.StatusBadRequest, "this connection attempt expired or wasn't started here; start again from Settings")
		return
	}
	if in.Code == "" {
		writeError(w, http.StatusBadRequest, "Uptimy didn't return a code")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	client := connect.NewClient(s.Config.UptimyAPIURL, s.Config.UptimyHeartbeatsURL)

	key, err := client.ExchangeCode(ctx, in.Code, pending.verifier, pending.redirectURI)
	if err != nil {
		writeError(w, http.StatusBadGateway, "couldn't get the agent key from Uptimy: "+err.Error())
		return
	}
	who, err := client.WhoAmI(ctx, key)
	if err != nil {
		writeError(w, http.StatusBadGateway, "couldn't verify the key with Uptimy: "+err.Error())
		return
	}
	if who.Scope != "agent" {
		// Never keep a full workspace key on the agent, even if one is handed over.
		writeError(w, http.StatusBadRequest, "Uptimy returned a full-access key instead of an agent key; start again from Settings")
		return
	}
	installID, err := s.Store.InstallID(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	hb, err := client.EnsureHeartbeat(ctx, key, "Uptimy Agent · "+s.Config.AgentName, installID)
	if err != nil {
		writeError(w, http.StatusBadGateway, "couldn't create the heartbeat in Uptimy: "+err.Error())
		return
	}
	if err := s.Watchdog.Connect(ctx, hb.PingURL); err != nil {
		writeError(w, http.StatusBadGateway, "the heartbeat was created but the first check-in failed: "+err.Error())
		return
	}

	conn := connect.Connection{
		APIKey:        key,
		KeyUUID:       who.UUID,
		WorkspaceName: who.Workspace.Name,
		HeartbeatUUID: hb.UUID,
		ConnectedAt:   time.Now().UTC(),
		ConnectedBy:   user.Username,
	}
	// Replacing an earlier connection: revoke its key (the heartbeat was
	// reclaimed above, so it must not be deleted), and keep a maintenance
	// window running on that same heartbeat so it still ends on time.
	if old, err := s.Store.UptimyConnection(r.Context()); err == nil && old != nil {
		if old.KeyUUID != who.UUID {
			if err := client.RevokeKey(ctx, old.APIKey); err != nil {
				s.Log.Warn("couldn't revoke the previous Uptimy agent key", "err", err)
			}
		}
		if old.HeartbeatUUID == hb.UUID {
			conn.MaintenanceUntil, conn.NormalGraceSeconds = old.MaintenanceUntil, old.NormalGraceSeconds
		}
	}
	if err := s.Store.SaveUptimyConnection(r.Context(), conn, hb.PingURL); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Log.Info("connected to Uptimy", "workspace", who.Workspace.Name, "reused_heartbeat", hb.Existing)
	s.writeHeartbeatStatus(w, r)
}

// disconnectHeartbeat stops checking in. For an account connection it also
// deletes the heartbeat in Uptimy (so it doesn't alert) and revokes the agent
// key. If Uptimy can't be reached, the agent disconnects anyway and says so.
func (s *Server) disconnectHeartbeat(w http.ResponseWriter, r *http.Request) {
	if s.rejectIfEnvManaged(w) {
		return
	}
	var warning string
	conn, err := s.Store.UptimyConnection(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if conn != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		client := connect.NewClient(s.Config.UptimyAPIURL, s.Config.UptimyHeartbeatsURL)
		if err := client.DeleteHeartbeat(ctx, conn.APIKey, conn.HeartbeatUUID); err != nil {
			warning = "Disconnected, but the heartbeat couldn't be removed from Uptimy (" + err.Error() + "). Delete it there so it doesn't alert."
		}
		if err := client.RevokeKey(ctx, conn.APIKey); err != nil && warning == "" {
			warning = "Disconnected, but the agent key couldn't be revoked (" + err.Error() + "). Revoke it under Settings → API Keys in Uptimy."
		}
		cancel()
	}
	if err := s.Store.DisconnectUptimy(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	s.Watchdog.SetURL("")
	resp, err := s.heartbeatStatusFor(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		heartbeatStatusResponse
		Warning string `json:"warning,omitempty"`
	}{resp, warning})
}

func (s *Server) testHeartbeat(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	_ = s.Watchdog.PingNow(ctx) // the outcome is recorded in the status returned below
	s.writeHeartbeatStatus(w, r)
}
