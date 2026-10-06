package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/uptimy/agent/internal/config"
)

// fakeUptimy stands in for api.upti.my and heartbeats.upti.my, following the
// real endpoints' contracts (POST /oauth/token, GET/DELETE /v1/api/api-key,
// the API-key heartbeat routes, and check-ins on /v1/monitors/:publicId).
type fakeUptimy struct {
	*httptest.Server
	mu         sync.Mutex
	keys       map[string]string // key -> scope ("" = revoked)
	codes      map[string]fakeCode
	consent    fakeCode          // the challenge and redirect from the last consent link
	heartbeats map[string]string // clientRef -> uuid
	monitors   map[string]*fakeHeartbeat
	deleted    []string
	checkIns   int
	down       bool
}

// fakeCode is a one-time code the consent page would issue for a key.
type fakeCode struct {
	key, challenge, redirect string
}

// approve plays the user approving on the consent page: it issues a code for
// key, bound to the last consent link's PKCE challenge and redirect URI.
func (f *fakeUptimy) approve(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	code := "code-" + strconv.Itoa(len(f.codes)+1)
	c := f.consent
	c.key = key
	f.codes[code] = c
	return code
}

// fakeHeartbeat is the part of a heartbeat monitor the agent reads and writes.
type fakeHeartbeat struct {
	interval, grace int
	paused          bool
	lastPut         map[string]any
}

func (h *fakeHeartbeat) json(uuid string) map[string]any {
	m := map[string]any{
		"uuid": uuid, "name": "Uptimy Agent", "intervalSeconds": h.interval, "graceSeconds": h.grace,
		"failureThreshold": 1, "recoveryThreshold": 1, "credential": map[string]any{"publicId": "pub-" + uuid},
	}
	if h.paused {
		m["pausedAt"] = "2026-09-30T12:00:00Z"
	}
	return m
}

func newFakeUptimy(t *testing.T) *fakeUptimy {
	f := &fakeUptimy{keys: map[string]string{}, codes: map[string]fakeCode{}, heartbeats: map[string]string{}, monitors: map[string]*fakeHeartbeat{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeUptimy) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/monitors/") {
		// Like the real service, a paused heartbeat rejects check-ins.
		if hb := f.monitors[strings.TrimPrefix(r.URL.Path, "/v1/monitors/pub-")]; hb != nil && hb.paused {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.checkIns++
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if r.URL.Path == "/oauth/token" {
		// Single use, bound to the PKCE verifier and redirect URI, like the real one.
		r.ParseForm()
		code, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || r.PostForm.Get("client_id") != "uptimy-agent" || r.PostForm.Get("redirect_uri") != code.redirect ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != code.challenge {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "The authorization code is invalid or expired"})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": code.key, "token_type": "Bearer", "scope": "agent"})
		return
	}
	key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	scope, ok := f.keys[key]
	if !ok || scope == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	switch {
	case r.URL.Path == "/v1/api/api-key" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			"uuid": "uuid-" + key, "scope": scope, "workspace": map[string]any{"id": 1, "name": "Acme"},
		}})
	case r.URL.Path == "/v1/api/api-key" && r.Method == http.MethodDelete:
		f.keys[key] = ""
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/v1/api/heartbeat-monitors/" && r.Method == http.MethodPost:
		var body struct {
			ClientRef       string `json:"clientRef"`
			IntervalSeconds int    `json:"intervalSeconds"`
			GraceSeconds    int    `json:"graceSeconds"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		status := http.StatusOK
		uuid, exists := f.heartbeats[body.ClientRef]
		if !exists {
			uuid, status = "hb-"+body.ClientRef, http.StatusCreated
			f.heartbeats[body.ClientRef] = uuid
			f.monitors[uuid] = &fakeHeartbeat{interval: body.IntervalSeconds, grace: body.GraceSeconds}
		}
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"data": f.monitors[uuid].json(uuid)})
	case strings.HasPrefix(r.URL.Path, "/v1/api/heartbeat-monitors/") && (r.Method == http.MethodGet || r.Method == http.MethodPut):
		uuid := strings.TrimPrefix(r.URL.Path, "/v1/api/heartbeat-monitors/")
		hb := f.monitors[uuid]
		if hb == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodPut {
			// A full-state update, like the real one.
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			hb.lastPut = body
			hb.interval = int(body["intervalSeconds"].(float64))
			hb.grace = int(body["graceSeconds"].(float64))
			hb.paused = body["paused"] == true
		}
		json.NewEncoder(w).Encode(map[string]any{"data": hb.json(uuid)})
	case strings.HasPrefix(r.URL.Path, "/v1/api/heartbeat-monitors/") && r.Method == http.MethodDelete:
		f.deleted = append(f.deleted, strings.TrimPrefix(r.URL.Path, "/v1/api/heartbeat-monitors/"))
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

// start runs the first half of the flow and returns the state from the
// consent link, checking the link itself on the way. The link's PKCE challenge
// and redirect URI are what the fake's next approved code is bound to.
func start(t *testing.T, c *client, uptimy *fakeUptimy) string {
	t.Helper()
	code, body := c.do("POST", "/api/uptimy/connect/start", map[string]string{"origin": "https://agent.acme.test"})
	if code != 200 {
		t.Fatalf("start: %d %v", code, body)
	}
	u, err := url.Parse(body["authorize_url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Path != "/oauth/authorize" || q.Get("response_type") != "code" || q.Get("client_id") != "uptimy-agent" ||
		q.Get("host") != "k8s-prod" || q.Get("redirect_uri") != "https://agent.acme.test/uptimy/connected" ||
		q.Get("code_challenge_method") != "S256" || len(q.Get("code_challenge")) != 43 || len(q.Get("state")) < 32 {
		t.Fatalf("unexpected consent link %s", u)
	}
	uptimy.mu.Lock()
	uptimy.consent = fakeCode{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri")}
	uptimy.mu.Unlock()
	return q.Get("state")
}

func TestConnectToUptimy(t *testing.T) {
	uptimy := newFakeUptimy(t)
	uptimy.keys["upt_agent1"] = "agent"
	uptimy.keys["upt_agent2"] = "agent"
	uptimy.keys["upt_full"] = "full"
	c, _ := newTestServerWithConfig(t, config.Config{
		UptimyAppURL: "https://app.example", UptimyAPIURL: uptimy.URL, UptimyHeartbeatsURL: uptimy.URL,
		AgentName: "k8s-prod",
	})
	c.login("admin", adminPassword)

	if code, _ := c.do("POST", "/api/uptimy/connect/start", map[string]string{"origin": "javascript:alert(1)"}); code != 400 {
		t.Fatalf("bad origin accepted: %d", code)
	}

	// A state that wasn't issued here is refused.
	if code, _ := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": strings.Repeat("x", 43), "code": uptimy.approve("upt_agent1")}); code != 400 {
		t.Fatalf("forged state accepted: %d", code)
	}

	// A code issued for another consent link (another PKCE challenge) is refused.
	state := start(t, c, uptimy)
	start(t, c, uptimy)
	stolen := uptimy.approve("upt_agent1") // bound to the second link's challenge
	if code, body := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": state, "code": stolen}); code != 502 {
		t.Fatalf("code with the wrong verifier accepted: %d %v", code, body)
	}

	// A full-access key is never kept, even with a valid state.
	state = start(t, c, uptimy)
	if code, body := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": state, "code": uptimy.approve("upt_full")}); code != 400 {
		t.Fatalf("full key accepted: %d %v", code, body)
	}

	// The real flow.
	state = start(t, c, uptimy)
	code, body := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": state, "code": uptimy.approve("upt_agent1")})
	if code != 200 || body["enabled"] != true || body["last_ok"] != true {
		t.Fatalf("finish: %d %v", code, body)
	}
	if acct, _ := body["account"].(map[string]any); acct == nil || acct["workspace_name"] != "Acme" || acct["connected_by"] != "admin" {
		t.Fatalf("account not reported: %v", body["account"])
	}
	if code, _ := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": state, "code": uptimy.approve("upt_agent1")}); code != 400 {
		t.Fatalf("state reused: %d", code)
	}
	if uptimy.checkIns != 1 || len(uptimy.heartbeats) != 1 {
		t.Fatalf("want 1 check-in and 1 heartbeat, got %d and %d", uptimy.checkIns, len(uptimy.heartbeats))
	}
	var firstHeartbeat string
	for _, uuid := range uptimy.heartbeats {
		firstHeartbeat = uuid
	}

	// Viewers see the connection but can't change it, and never get the URL.
	c.do("POST", "/api/users", map[string]string{"username": "vic", "password": "temp-pass-1", "role": "viewer"})
	v := c.another()
	v.login("vic", "temp-pass-1")
	v.do("POST", "/api/auth/password", map[string]string{"current": "temp-pass-1", "new": "vics-password"})
	if code, body := v.do("GET", "/api/uptimy/heartbeat", nil); code != 200 || body["url"] != nil || body["account"] == nil {
		t.Fatalf("viewer status: %d %v", code, body)
	}
	if code, _ := v.do("POST", "/api/uptimy/connect/start", map[string]string{"origin": "https://agent.acme.test"}); code != 403 {
		t.Fatalf("viewer started a connection: %d", code)
	}

	// Reconnecting (e.g. after the key was revoked) reuses the heartbeat and
	// revokes the old key.
	state = start(t, c, uptimy)
	if code, _ := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": state, "code": uptimy.approve("upt_agent2")}); code != 200 {
		t.Fatalf("reconnect: %d", code)
	}
	if len(uptimy.heartbeats) != 1 || uptimy.keys["upt_agent1"] != "" {
		t.Fatalf("reconnect should reuse the heartbeat and revoke the old key: %v %v", uptimy.heartbeats, uptimy.keys)
	}

	// Disconnect deletes the heartbeat and revokes the key.
	code, body = c.do("DELETE", "/api/uptimy/heartbeat", nil)
	if code != 200 || body["enabled"] != false || body["account"] != nil || body["warning"] != nil {
		t.Fatalf("disconnect: %d %v", code, body)
	}
	if len(uptimy.deleted) != 1 || uptimy.deleted[0] != firstHeartbeat || uptimy.keys["upt_agent2"] != "" {
		t.Fatalf("disconnect should delete the heartbeat and revoke the key: %v %v", uptimy.deleted, uptimy.keys)
	}
}

func TestDisconnectWhenUptimyIsDown(t *testing.T) {
	uptimy := newFakeUptimy(t)
	uptimy.keys["upt_agent1"] = "agent"
	c, _ := newTestServerWithConfig(t, config.Config{
		UptimyAppURL: "https://app.example", UptimyAPIURL: uptimy.URL, UptimyHeartbeatsURL: uptimy.URL,
		AgentName: "k8s-prod",
	})
	c.login("admin", adminPassword)
	state := start(t, c, uptimy)
	if code, _ := c.do("POST", "/api/uptimy/connect/finish", map[string]string{"state": state, "code": uptimy.approve("upt_agent1")}); code != 200 {
		t.Fatalf("connect: %d", code)
	}

	uptimy.mu.Lock()
	uptimy.down = true
	uptimy.mu.Unlock()
	code, body := c.do("DELETE", "/api/uptimy/heartbeat", nil)
	if code != 200 || body["enabled"] != false {
		t.Fatalf("should still disconnect locally: %d %v", code, body)
	}
	if w, _ := body["warning"].(string); !strings.Contains(w, "Delete it there") {
		t.Fatalf("expected a warning to clean up in Uptimy, got %v", body["warning"])
	}
}
