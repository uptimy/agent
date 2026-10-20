package connect

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Uptimy Cloud sync (AGENT_CLOUD_SYNC_PLAN.md in the uptimy repo): with the
// same agent key, the agent registers itself as a private location, mirrors
// its monitors read-only, and uploads their results to ingest.upti.my.

// DefaultIngestURL receives uploads.
const DefaultIngestURL = "https://ingest.upti.my"

// Errors Cloud answers with that the sync handles.
var (
	// ErrAgentLimit: the workspace has as many agents as its plan allows.
	ErrAgentLimit = errors.New("your Uptimy plan's connected agents are all in use")
	// ErrNoLocation: Cloud has no location for this key (disconnected there,
	// or not set up yet); the agent registers it again.
	ErrNoLocation = errors.New("Uptimy has no location for this agent")
	// ErrPlanLimit: this agent is over the plan's connected agents after a
	// downgrade; Cloud refuses its results until it's within the plan again.
	ErrPlanLimit = errors.New("this agent is over your plan's connected agents, so its results aren't synced")
)

// RefusedError: Cloud refused the request as invalid (400 or 422), e.g. a
// monitor it can't mirror. Sending it again unchanged won't help.
type RefusedError struct{ Reason string }

func (e *RefusedError) Error() string { return "Uptimy: " + e.Reason }

// RetryAfterError asks the caller to wait before uploading again (429).
type RetryAfterError struct{ After time.Duration }

func (e *RetryAfterError) Error() string { return "Uptimy asked to retry in " + e.After.String() }

// Location is the agent's private location in Cloud.
type Location struct {
	UUID                        string `json:"locationUuid"`
	UploadIntervalSeconds       int    `json:"uploadIntervalSeconds"`
	StatusChangeIntervalSeconds int    `json:"statusChangeIntervalSeconds"`
	SampleIntervalSeconds       int    `json:"sampleIntervalSeconds"`
	WithinPlan                  bool   `json:"withinPlan"`
}

// PutLocation creates or renames the agent's location (keyed by install id).
func (c *Client) PutLocation(ctx context.Context, key, installID, name, version string) (Location, error) {
	var out struct {
		Data Location `json:"data"`
	}
	err := c.cloudDo(ctx, key, http.MethodPut, c.APIURL+"/v1/api/agent/location",
		map[string]any{"installId": installID, "name": name, "version": version}, &out)
	return out.Data, err
}

// DeleteLocation removes the agent's location and its mirrored healthchecks
// (the agent disconnecting). One that's already gone counts as success.
func (c *Client) DeleteLocation(ctx context.Context, key string) error {
	err := c.cloudDo(ctx, key, http.MethodDelete, c.APIURL+"/v1/api/agent/location", nil, nil)
	if errors.Is(err, errNotFound) || errors.Is(err, ErrNoLocation) {
		return nil
	}
	return err
}

// MirroredHealthcheck is what Cloud shows for one of the agent's healthchecks.
type MirroredHealthcheck struct {
	Name            string         `json:"name"`
	Type            string         `json:"type"`
	Target          string         `json:"target"`
	IntervalSeconds int            `json:"intervalSeconds"`
	TimeoutSeconds  int            `json:"timeoutSeconds"`
	Config          map[string]any `json:"config,omitempty"`
	Paused          bool           `json:"paused"`
}

// HealthcheckRef identifies a mirrored healthcheck in Cloud.
type HealthcheckRef struct {
	ClientRef       string `json:"clientRef"`
	HealthcheckUUID string `json:"healthcheckUuid"`
}

// PutHealthcheck creates or updates the mirror of one healthcheck.
func (c *Client) PutHealthcheck(ctx context.Context, key, clientRef string, h MirroredHealthcheck) (HealthcheckRef, error) {
	var out struct {
		Data HealthcheckRef `json:"data"`
	}
	err := c.cloudDo(ctx, key, http.MethodPut, c.APIURL+"/v1/api/agent/healthchecks/"+url.PathEscape(clientRef), h, &out)
	return out.Data, err
}

// DeleteHealthcheck removes a mirror. One that's already gone counts as success.
func (c *Client) DeleteHealthcheck(ctx context.Context, key, clientRef string) error {
	err := c.cloudDo(ctx, key, http.MethodDelete, c.APIURL+"/v1/api/agent/healthchecks/"+url.PathEscape(clientRef), nil, nil)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// ListHealthchecks returns every mirrored healthcheck, for a full reconcile.
func (c *Client) ListHealthchecks(ctx context.Context, key string) ([]HealthcheckRef, error) {
	var out struct {
		Data struct {
			Results []HealthcheckRef `json:"results"`
		} `json:"data"`
	}
	err := c.cloudDo(ctx, key, http.MethodGet, c.APIURL+"/v1/api/agent/healthchecks", nil, &out)
	return out.Data.Results, err
}

// Heartbeat sources in Cloud.
const (
	SourceAgent         = "agent"
	SourceAgentLiveness = "agent_liveness"
)

// MirroredHeartbeat is what Cloud shows for one of the agent's heartbeats.
// Its schedule is IntervalSeconds, or Cron in Timezone (IntervalSeconds 0).
type MirroredHeartbeat struct {
	Name            string `json:"name"`
	Description     string `json:"description,omitempty"`
	IntervalSeconds int    `json:"intervalSeconds"`
	Cron            string `json:"cron,omitempty"`
	Timezone        string `json:"timezone,omitempty"`
	GraceSeconds    int    `json:"graceSeconds"`
	Paused          bool   `json:"paused"`
}

// scheduleFields is the heartbeat's schedule as Cloud takes it. cron is always
// sent: "" tells Cloud the heartbeat is on an interval (again).
func (h MirroredHeartbeat) scheduleFields(body map[string]any) map[string]any {
	body["intervalSeconds"], body["cron"] = h.IntervalSeconds, h.Cron
	if h.Cron != "" {
		body["timezone"] = h.Timezone
	}
	return body
}

// HeartbeatRef identifies an agent-managed heartbeat in Cloud.
type HeartbeatRef struct {
	UUID      string `json:"uuid"`
	ClientRef string `json:"clientRef"`
	Source    string `json:"source"`
}

// EnsureAgentHeartbeat creates an agent-managed heartbeat, or claims and
// returns the existing one with this clientRef (Cloud applies the source to
// it: the agent's old check-in heartbeat becomes its liveness heartbeat).
func (c *Client) EnsureAgentHeartbeat(ctx context.Context, key, clientRef, source string, h MirroredHeartbeat) (HeartbeatRef, error) {
	body := h.scheduleFields(map[string]any{
		"name": h.Name, "description": h.Description, "graceSeconds": h.GraceSeconds,
		"paused": h.Paused, "clientRef": clientRef, "source": source,
	})
	var out struct {
		Data HeartbeatRef `json:"data"`
	}
	err := c.cloudDo(ctx, key, http.MethodPost, c.HeartbeatsURL+"/v1/api/heartbeat-monitors/", body, &out)
	return out.Data, err
}

// PutAgentHeartbeat replaces an agent heartbeat's settings.
func (c *Client) PutAgentHeartbeat(ctx context.Context, key, uuid string, h MirroredHeartbeat) error {
	body := h.scheduleFields(map[string]any{
		"name": h.Name, "description": h.Description, "graceSeconds": h.GraceSeconds,
		"paused": h.Paused, "failureThreshold": 1, "recoveryThreshold": 1,
	})
	return c.cloudDo(ctx, key, http.MethodPut, c.HeartbeatsURL+"/v1/api/heartbeat-monitors/"+url.PathEscape(uuid), body, nil)
}

// ListAgentHeartbeats returns the heartbeats the key manages.
func (c *Client) ListAgentHeartbeats(ctx context.Context, key string) ([]HeartbeatRef, error) {
	var out struct {
		Data struct {
			Results []HeartbeatRef `json:"results"`
		} `json:"data"`
	}
	err := c.cloudDo(ctx, key, http.MethodGet, c.HeartbeatsURL+"/v1/api/heartbeat-monitors/", nil, &out)
	return out.Data.Results, err
}

// Upload is one POST /v1/agent/results.
type Upload struct {
	BatchID      string              `json:"batchId"`
	LocationUUID string              `json:"locationUuid"`
	Healthchecks []UploadHealthcheck `json:"healthchecks"`
	Heartbeats   []UploadHeartbeat   `json:"heartbeats"`
}

// UploadHealthcheck is one check result with the status the agent decided.
type UploadHealthcheck struct {
	HealthcheckUUID string     `json:"healthcheckUuid"`
	CheckedAt       time.Time  `json:"checkedAt"`
	OK              bool       `json:"ok"`
	LatencyMs       *int       `json:"latencyMs,omitempty"`
	Message         string     `json:"message,omitempty"`
	Status          string     `json:"status"` // up, degraded, down
	StatusSince     *time.Time `json:"statusSince,omitempty"`
	StatusCode      int        `json:"statusCode,omitempty"` // HTTP checks that got a response
	ErrorClass      string     `json:"errorClass,omitempty"` // failures only
}

// UploadHeartbeat is one heartbeat run as the agent judged it.
type UploadHeartbeat struct {
	HeartbeatUUID string    `json:"heartbeatUuid"`
	At            time.Time `json:"at"`
	Outcome       string    `json:"outcome"` // success, failed, missed
	DurationMs    int64     `json:"durationMs,omitempty"`
	Message       string    `json:"message,omitempty"`
	Status        string    `json:"status"` // up, down
}

// Why ingest rejected an item (UploadResult.Rejected).
const (
	RejectedUnknown    = "unknown"     // no such mirror (any more)
	RejectedTooOld     = "too_old"     // more than a day old
	RejectedClockAhead = "clock_ahead" // timestamped in the future
)

// UploadResult is what ingest accepted.
type UploadResult struct {
	Accepted int `json:"accepted"`
	Rejected []struct {
		UUID   string `json:"uuid"`
		Reason string `json:"reason"`
	} `json:"rejected"`
	UploadIntervalSeconds       int `json:"uploadIntervalSeconds"`
	StatusChangeIntervalSeconds int `json:"statusChangeIntervalSeconds"`
	SampleIntervalSeconds       int `json:"sampleIntervalSeconds"`
}

// UploadResults sends one batch to ingest. A repeat of a stored batch is
// answered 200 and counts as success.
func (c *Client) UploadResults(ctx context.Context, key string, u Upload) (UploadResult, error) {
	var out struct {
		Data UploadResult `json:"data"`
	}
	err := c.cloudDo(ctx, key, http.MethodPost, c.IngestURL+"/v1/agent/results", u, &out)
	return out.Data, err
}

// cloudDo is doStatus with the sync's own answers: 409 (no location), 403
// PLAN_LIMIT / AGENT_LIMIT, and 429 with Retry-After.
func (c *Client) cloudDo(ctx context.Context, key, method, endpoint string, body, out any) error {
	r, err := c.doFull(ctx, key, method, endpoint, body, out)
	switch {
	case r.status == http.StatusConflict:
		return ErrNoLocation
	case r.status == http.StatusTooManyRequests:
		after := r.retryAfter
		if after <= 0 {
			after = time.Minute
		}
		return &RetryAfterError{After: after}
	case r.code == "PLAN_LIMIT.403":
		return ErrPlanLimit
	case r.code == "AGENT_LIMIT.403":
		return ErrAgentLimit
	case r.status == http.StatusBadRequest && strings.Contains(r.message, "isn't this agent's location"):
		// Ingest: the location was replaced (set up anew, e.g. elsewhere).
		return ErrNoLocation
	case r.status == http.StatusBadRequest || r.status == http.StatusUnprocessableEntity:
		reason := r.message
		if reason == "" {
			reason = "HTTP " + strconv.Itoa(r.status)
		}
		return &RefusedError{Reason: reason}
	}
	return err
}

// parseRetryAfter reads a Retry-After header in seconds.
func parseRetryAfter(v string) time.Duration {
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return 0
}
