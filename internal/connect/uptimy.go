package connect

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Default Uptimy endpoints. Overridable for development and self-hosted setups.
const (
	DefaultAppURL        = "https://app.upti.my"
	DefaultAPIURL        = "https://api.upti.my"
	DefaultHeartbeatsURL = "https://heartbeats.upti.my"
)

// ErrKeyRejected means Uptimy didn't accept the agent key (revoked, expired,
// or never valid).
var ErrKeyRejected = errors.New("Uptimy rejected the agent key")

// Client talks to Uptimy with an agent-scoped API key: just enough to check
// the key, manage the agent's own heartbeat, and revoke the key again.
type Client struct {
	APIURL        string
	HeartbeatsURL string
	HTTP          *http.Client
}

// NewClient returns a client for the given base URLs.
func NewClient(apiURL, heartbeatsURL string) *Client {
	return &Client{
		APIURL:        strings.TrimRight(apiURL, "/"),
		HeartbeatsURL: strings.TrimRight(heartbeatsURL, "/"),
		HTTP:          &http.Client{Timeout: 15 * time.Second},
	}
}

// KeyInfo describes an API key and the workspace it belongs to.
type KeyInfo struct {
	UUID      string `json:"uuid"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Workspace struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"workspace"`
}

// OAuthClientID is the agent's built-in OAuth client in Uptimy
// (upti.my-api utils/oauth.ts).
const OAuthClientID = "uptimy-agent"

// NewPKCE returns an RFC 7636 verifier and its S256 challenge.
func NewPKCE() (verifier, challenge string) {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

// ExchangeCode trades the one-time code from Uptimy's consent page for the
// agent key (OAuth with PKCE). The agent's server does this itself, so the key
// never passes through the browser, and Uptimy creates it only now.
func (c *Client) ExchangeCode(ctx context.Context, code, verifier, redirectURI string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", OAuthClientID)
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.APIURL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "uptimy-agent")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // don't echo URLs into user-facing errors
		}
		return "", fmt.Errorf("couldn't reach Uptimy: %w", err)
	}
	defer resp.Body.Close()
	var out struct {
		AccessToken      string `json:"access_token"`
		ErrorDescription string `json:"error_description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out)
	if resp.StatusCode != http.StatusOK {
		if out.ErrorDescription != "" {
			return "", fmt.Errorf("Uptimy: %s", out.ErrorDescription)
		}
		return "", fmt.Errorf("Uptimy returned HTTP %d", resp.StatusCode)
	}
	if !strings.HasPrefix(out.AccessToken, "upt_") {
		return "", errors.New("Uptimy didn't return an agent key")
	}
	return out.AccessToken, nil
}

// WhoAmI verifies the key and returns its workspace.
func (c *Client) WhoAmI(ctx context.Context, key string) (KeyInfo, error) {
	var out struct {
		Data KeyInfo `json:"data"`
	}
	err := c.do(ctx, key, http.MethodGet, c.APIURL+"/v1/api/api-key", nil, &out)
	return out.Data, err
}

// Heartbeat is the agent's heartbeat monitor in Uptimy.
type Heartbeat struct {
	UUID     string
	PingURL  string
	Existing bool // true when an earlier connection's heartbeat was reclaimed
}

// CheckInInterval is how often the agent checks in with Uptimy. When Uptimy
// alerts is set separately, as the heartbeat's grace (see GraceFor).
const CheckInInterval = time.Minute

// Connection is an account connection made through "Connect to Uptimy". The
// agent key never leaves the agent's server.
type Connection struct {
	APIKey        string    `json:"api_key"`
	KeyUUID       string    `json:"key_uuid"`
	WorkspaceName string    `json:"workspace_name"`
	HeartbeatUUID string    `json:"heartbeat_uuid"`
	ConnectedAt   time.Time `json:"connected_at"`
	ConnectedBy   string    `json:"connected_by"`

	// Paused mirrors the heartbeat's paused state in Uptimy.
	Paused bool `json:"paused,omitempty"`
	// During a maintenance window: when it ends, and the grace to restore.
	MaintenanceUntil   *time.Time `json:"maintenance_until,omitempty"`
	NormalGraceSeconds int        `json:"normal_grace_seconds,omitempty"`
}

// DefaultAlertAfter is how long the agent may be silent before Uptimy alerts,
// for a new connection. Long enough to ride out a normal restart or redeploy
// (image pull, Railway rebuild, Helm upgrade) without paging anyone.
const DefaultAlertAfter = 5 * time.Minute

// GraceFor returns the heartbeat grace that makes Uptimy alert after alertAfter
// of silence (Uptimy alerts once CheckInInterval + grace pass without a
// check-in).
func GraceFor(alertAfter time.Duration) int {
	return max(0, int((alertAfter - CheckInInterval).Seconds()))
}

// EnsureHeartbeat creates the agent's heartbeat, or reclaims the one it made
// before: clientRef (a stable install ID) makes this idempotent on Uptimy's
// side, so reconnecting never leaves an orphaned heartbeat behind. A
// reclaimed heartbeat keeps its settings, including any changed in Uptimy.
func (c *Client) EnsureHeartbeat(ctx context.Context, key, name, clientRef string) (Heartbeat, error) {
	body := map[string]any{
		"name":            name,
		"description":     "Created by Uptimy Agent. Alerts when the agent stops checking in.",
		"intervalSeconds": int(CheckInInterval.Seconds()),
		"graceSeconds":    GraceFor(DefaultAlertAfter),
		"clientRef":       clientRef,
	}
	var out struct {
		Data struct {
			UUID       string `json:"uuid"`
			Credential struct {
				PublicID string `json:"publicId"`
			} `json:"credential"`
		} `json:"data"`
	}
	status, err := c.doStatus(ctx, key, http.MethodPost, c.HeartbeatsURL+"/v1/api/heartbeat-monitors/", body, &out)
	if err != nil {
		return Heartbeat{}, err
	}
	if out.Data.UUID == "" || out.Data.Credential.PublicID == "" {
		return Heartbeat{}, errors.New("Uptimy didn't return the heartbeat's check-in URL")
	}
	return Heartbeat{
		UUID:     out.Data.UUID,
		PingURL:  c.HeartbeatsURL + "/v1/monitors/" + url.PathEscape(out.Data.Credential.PublicID),
		Existing: status == http.StatusOK,
	}, nil
}

// HeartbeatSettings are the parts of the agent's heartbeat that decide when
// Uptimy alerts. Uptimy is the source of truth: people can change them there
// or with uptimyctl, so the agent reads them rather than assuming.
type HeartbeatSettings struct {
	IntervalSeconds int  `json:"interval_seconds"`
	GraceSeconds    int  `json:"grace_seconds"`
	Paused          bool `json:"paused"`
}

// AlertAfter is how long the agent may be silent before Uptimy alerts.
func (h HeartbeatSettings) AlertAfter() time.Duration {
	return time.Duration(h.IntervalSeconds+h.GraceSeconds) * time.Second
}

// ErrHeartbeatGone means the agent's heartbeat no longer exists in Uptimy.
var ErrHeartbeatGone = errors.New("the agent's heartbeat no longer exists in Uptimy; connect again")

func (c *Client) getHeartbeat(ctx context.Context, key, uuid string) (map[string]any, error) {
	var out struct {
		Data map[string]any `json:"data"`
	}
	err := c.do(ctx, key, http.MethodGet, c.HeartbeatsURL+"/v1/api/heartbeat-monitors/"+url.PathEscape(uuid), nil, &out)
	if errors.Is(err, errNotFound) {
		return nil, ErrHeartbeatGone
	}
	return out.Data, err
}

func settingsOf(hb map[string]any) HeartbeatSettings {
	num := func(k string) int {
		f, _ := hb[k].(float64)
		return int(f)
	}
	return HeartbeatSettings{
		IntervalSeconds: num("intervalSeconds"),
		GraceSeconds:    num("graceSeconds"),
		Paused:          hb["pausedAt"] != nil,
	}
}

// GetHeartbeatSettings reads the heartbeat's current settings from Uptimy.
func (c *Client) GetHeartbeatSettings(ctx context.Context, key, uuid string) (HeartbeatSettings, error) {
	hb, err := c.getHeartbeat(ctx, key, uuid)
	if err != nil {
		return HeartbeatSettings{}, err
	}
	return settingsOf(hb), nil
}

// UpdateHeartbeat changes the heartbeat's grace and/or paused state. Uptimy's
// update replaces the whole heartbeat, so it reads the current one first and
// sends everything else back unchanged (the same merge uptimyctl does).
func (c *Client) UpdateHeartbeat(ctx context.Context, key, uuid string, graceSeconds *int, paused *bool) (HeartbeatSettings, error) {
	hb, err := c.getHeartbeat(ctx, key, uuid)
	if err != nil {
		return HeartbeatSettings{}, err
	}
	body := map[string]any{"paused": hb["pausedAt"] != nil}
	for _, k := range []string{"name", "description", "intervalSeconds", "graceSeconds", "failureThreshold", "recoveryThreshold", "enforceOrder", "steps"} {
		if v, ok := hb[k]; ok {
			body[k] = v
		}
	}
	if graceSeconds != nil {
		body["graceSeconds"] = *graceSeconds
	}
	if paused != nil {
		body["paused"] = *paused
	}
	var out struct {
		Data map[string]any `json:"data"`
	}
	err = c.do(ctx, key, http.MethodPut, c.HeartbeatsURL+"/v1/api/heartbeat-monitors/"+url.PathEscape(uuid), body, &out)
	if errors.Is(err, errNotFound) {
		return HeartbeatSettings{}, ErrHeartbeatGone
	}
	if err != nil {
		return HeartbeatSettings{}, err
	}
	return settingsOf(out.Data), nil
}

// DeleteHeartbeat removes the agent's heartbeat. A heartbeat that's already
// gone counts as success.
func (c *Client) DeleteHeartbeat(ctx context.Context, key, uuid string) error {
	err := c.do(ctx, key, http.MethodDelete, c.HeartbeatsURL+"/v1/api/heartbeat-monitors/"+url.PathEscape(uuid), nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// RevokeKey deletes the agent's own key. A key that's already revoked counts
// as success.
func (c *Client) RevokeKey(ctx context.Context, key string) error {
	err := c.do(ctx, key, http.MethodDelete, c.APIURL+"/v1/api/api-key", nil, nil)
	if errors.Is(err, ErrKeyRejected) || errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

var errNotFound = errors.New("not found")

func (c *Client) do(ctx context.Context, key, method, endpoint string, body, out any) error {
	_, err := c.doStatus(ctx, key, method, endpoint, body, out)
	return err
}

func (c *Client) doStatus(ctx context.Context, key, method, endpoint string, body, out any) (int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "uptimy-agent")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // don't echo URLs into user-facing errors
		}
		return 0, fmt.Errorf("couldn't reach Uptimy: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return resp.StatusCode, ErrKeyRejected
	case resp.StatusCode == http.StatusNotFound:
		return resp.StatusCode, errNotFound
	case resp.StatusCode >= 300:
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(raw, &e) == nil && e.Error != "" {
			return resp.StatusCode, fmt.Errorf("Uptimy: %s", e.Error)
		}
		return resp.StatusCode, fmt.Errorf("Uptimy returned HTTP %d", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("unexpected response from Uptimy: %w", err)
		}
	}
	return resp.StatusCode, nil
}
