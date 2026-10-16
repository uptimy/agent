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

// ErrKeyRejected means Uptimy didn't accept the agent key (revoked, expired,
// or never valid).
var ErrKeyRejected = errors.New("Uptimy rejected the agent key")

// Client talks to Uptimy with an agent-scoped API key: the agent's location,
// its mirrored monitors and check-in (cloud.go), and revoking the key.
type Client struct {
	APIURL        string
	HeartbeatsURL string
	// IngestURL receives the sync's uploads (DefaultIngestURL).
	IngestURL string
	HTTP      *http.Client
}

// NewClient returns a client for the given base URLs.
func NewClient(apiURL, heartbeatsURL string) *Client {
	return &Client{
		APIURL:        strings.TrimRight(apiURL, "/"),
		HeartbeatsURL: strings.TrimRight(heartbeatsURL, "/"),
		IngestURL:     DefaultIngestURL,
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

	// Cloud sync (internal/cloudsync): the agent's private location, how
	// often it uploads, how soon a status change may follow the last upload
	// (the plan's check interval), and how often an unchanged result is sent.
	// Empty LocationUUID means the sync hasn't set the connection up yet,
	// e.g. one made before 0.2.0.
	LocationUUID                string `json:"location_uuid,omitempty"`
	UploadIntervalSeconds       int    `json:"upload_interval_seconds,omitempty"`
	StatusChangeIntervalSeconds int    `json:"status_change_interval_seconds,omitempty"`
	SampleIntervalSeconds       int    `json:"sample_interval_seconds,omitempty"`
	// During a maintenance window: when it ends, and the grace to restore.
	MaintenanceUntil   *time.Time `json:"maintenance_until,omitempty"`
	NormalGraceSeconds int        `json:"normal_grace_seconds,omitempty"`
}

// GraceFor returns the heartbeat grace that makes Uptimy alert after alertAfter
// of silence from an agent checking in every interval (Uptimy alerts once
// interval + grace pass without a check-in).
func GraceFor(alertAfter, interval time.Duration) int {
	return max(0, int((alertAfter - interval).Seconds()))
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

// ErrNotFound is a 404 from Uptimy.
var ErrNotFound = errNotFound

// ErrUnreachable wraps network errors: Uptimy couldn't be asked at all.
var ErrUnreachable = errors.New("couldn't reach Uptimy")

func (c *Client) do(ctx context.Context, key, method, endpoint string, body, out any) error {
	_, err := c.doStatus(ctx, key, method, endpoint, body, out)
	return err
}

func (c *Client) doStatus(ctx context.Context, key, method, endpoint string, body, out any) (int, error) {
	r, err := c.doFull(ctx, key, method, endpoint, body, out)
	return r.status, err
}

// response is what the sync needs from an answer besides its body.
type response struct {
	status     int
	code       string // the error envelope's code, e.g. "PLAN_LIMIT.403"
	message    string // and its error
	retryAfter time.Duration
}

func (c *Client) doFull(ctx context.Context, key, method, endpoint string, body, out any) (response, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return response{}, err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return response{}, err
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
		return response{}, fmt.Errorf("%w: %w", ErrUnreachable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	r := response{status: resp.StatusCode, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"))}

	if resp.StatusCode >= 300 {
		var e struct {
			Code  string `json:"code"`
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &e)
		r.code, r.message = e.Code, e.Error
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return r, ErrKeyRejected
		case resp.StatusCode == http.StatusForbidden && e.Code != "AGENT_LIMIT.403" && e.Code != "PLAN_LIMIT.403":
			return r, ErrKeyRejected
		case resp.StatusCode == http.StatusNotFound:
			return r, errNotFound
		case e.Error != "":
			return r, fmt.Errorf("Uptimy: %s", e.Error)
		}
		return r, fmt.Errorf("Uptimy returned HTTP %d", resp.StatusCode)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return r, fmt.Errorf("unexpected response from Uptimy: %w", err)
		}
	}
	return r, nil
}
