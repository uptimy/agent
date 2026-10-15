package api

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/uptimy/agent/internal/cloudsync"
	"github.com/uptimy/agent/internal/connect"
)

const (
	// connectCallbackPath must match AGENT_CALLBACK_PATH in upti.my-api's
	// utils/oauth.ts, which only lets the agent's OAuth client return here.
	connectCallbackPath = "/uptimy/connected"
	connectStateTTL     = 10 * time.Minute
)

func randomToken(n int) string {
	buf := make([]byte, n)
	rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// uptimyStatusResponse is the agent's Uptimy Cloud connection, or nothing
// but Connected false.
type uptimyStatusResponse struct {
	Connected bool `json:"connected"`
	// Paused in Uptimy: nobody is alerted when the agent goes silent.
	Paused  bool              `json:"paused"`
	Account *uptimyAccount    `json:"account"`
	Sync    *cloudsync.Status `json:"sync,omitempty"`
}

type uptimyAccount struct {
	WorkspaceName string    `json:"workspace_name"`
	ConnectedAt   time.Time `json:"connected_at"`
	ConnectedBy   string    `json:"connected_by"`
}

func (s *Server) uptimyStatusFor(r *http.Request) (uptimyStatusResponse, error) {
	conn, err := s.Store.UptimyConnection(r.Context())
	if err != nil || conn == nil {
		return uptimyStatusResponse{}, err
	}
	st := s.Sync.Status()
	return uptimyStatusResponse{
		Connected: true,
		Paused:    conn.Paused,
		Account:   &uptimyAccount{conn.WorkspaceName, conn.ConnectedAt, conn.ConnectedBy},
		Sync:      &st,
	}, nil
}

func (s *Server) writeUptimyStatus(w http.ResponseWriter, r *http.Request) {
	resp, err := s.uptimyStatusFor(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) uptimyStatus(w http.ResponseWriter, r *http.Request) {
	s.writeUptimyStatus(w, r)
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
	conn := connect.Connection{
		APIKey:        key,
		KeyUUID:       who.UUID,
		WorkspaceName: who.Workspace.Name,
		ConnectedAt:   time.Now().UTC(),
		ConnectedBy:   user.Username,
	}
	old, err := s.Store.UptimyConnection(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	// The sync registers the agent's location in Cloud, sets up its check-in
	// heartbeat (reclaiming the one an earlier connection made) and saves the
	// connection. Its uploads are the check-in from now on.
	if err := s.Sync.SetUp(ctx, &conn); err != nil {
		// Don't leave an unused key behind in Uptimy.
		if rerr := client.RevokeKey(ctx, key); rerr != nil {
			s.Log.Warn("couldn't revoke the new Uptimy agent key", "err", rerr)
		}
		if errors.Is(err, connect.ErrAgentLimit) {
			writeError(w, http.StatusForbidden, "Your Uptimy plan's connected agents are all in use. Upgrade, or disconnect another agent under Settings → Agents in Uptimy, then connect again.")
			return
		}
		writeError(w, http.StatusBadGateway, "couldn't set the agent up in Uptimy: "+err.Error())
		return
	}
	// Replacing an earlier connection: revoke its key, and keep a maintenance
	// window running on the same heartbeat so it still ends on time.
	if old != nil {
		if old.KeyUUID != who.UUID {
			if err := client.RevokeKey(ctx, old.APIKey); err != nil {
				s.Log.Warn("couldn't revoke the previous Uptimy agent key", "err", err)
			}
		}
		if old.HeartbeatUUID == conn.HeartbeatUUID && old.MaintenanceUntil != nil {
			conn.MaintenanceUntil, conn.NormalGraceSeconds = old.MaintenanceUntil, old.NormalGraceSeconds
			if err := s.Store.UpdateUptimyConnection(r.Context(), conn); err != nil {
				s.internalError(w, r, err)
				return
			}
		}
	}
	s.Log.Info("connected to Uptimy Cloud", "workspace", who.Workspace.Name, "location", conn.LocationUUID)
	s.writeUptimyStatus(w, r)
}

// disconnectUptimy removes everything the connection made in Uptimy (the
// location, its mirrored monitors and check-in) and revokes the agent key. If
// Uptimy can't be reached, the agent disconnects anyway and says so.
func (s *Server) disconnectUptimy(w http.ResponseWriter, r *http.Request) {
	var warning string
	conn, err := s.Store.UptimyConnection(r.Context())
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	if conn != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		if err := s.Sync.Disconnect(ctx, conn); err != nil {
			warning = "Disconnected, but " + err.Error() + "."
		}
		client := connect.NewClient(s.Config.UptimyAPIURL, s.Config.UptimyHeartbeatsURL)
		if err := client.RevokeKey(ctx, conn.APIKey); err != nil && warning == "" {
			warning = "Disconnected, but the agent key couldn't be revoked (" + err.Error() + "). Revoke it under Settings → API Keys in Uptimy."
		}
		cancel()
	}
	if err := s.Store.DisconnectUptimy(r.Context()); err != nil {
		s.internalError(w, r, err)
		return
	}
	resp, err := s.uptimyStatusFor(r)
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		uptimyStatusResponse
		Warning string `json:"warning,omitempty"`
	}{resp, warning})
}

// uploadNow runs a sync cycle right away (it also checks the agent in).
func (s *Server) uploadNow(w http.ResponseWriter, r *http.Request) {
	s.Sync.Trigger()
	s.writeUptimyStatus(w, r)
}
