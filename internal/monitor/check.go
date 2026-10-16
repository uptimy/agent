package monitor

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Type is a healthcheck's check type ("http", "dns", ...). Each one is
// registered in a check_<type>.go file; see checktypes.go.
type Type string

// Built-in check types.
const (
	TypeHTTP       Type = "http"
	TypeTCP        Type = "tcp"
	TypePing       Type = "ping"
	TypeDNS        Type = "dns"
	TypeTLS        Type = "tls"
	TypeKubernetes Type = "kubernetes"
	TypePostgres   Type = "postgres"
	TypeMySQL      Type = "mysql"
	TypeRedis      Type = "redis"
)

// Check is a healthcheck's settings: what to probe and how often.
type Check struct {
	Type             Type   `json:"type"`
	Target           string `json:"target"`
	IntervalSeconds  int    `json:"interval_seconds"`
	TimeoutSeconds   int    `json:"timeout_seconds"`
	FailureThreshold int    `json:"failure_threshold"` // consecutive failures before it's down
	// RecoveryThreshold is how many consecutive successes bring it back up
	// once down, so a flapping target doesn't send down/up pairs.
	RecoveryThreshold int    `json:"recovery_threshold"`
	Config            Config `json:"config"`
}

// Config holds check-type settings: one flat set of keys, so YAML stays
// simple (`config: {method: POST}`). Each CheckType declares the keys it
// uses, and normalizing drops the rest.
type Config struct {
	// HTTP
	Method         string            `json:"method,omitempty" yaml:"method,omitempty"`
	ExpectedStatus string            `json:"expected_status,omitempty" yaml:"expected_status,omitempty"`
	Keyword        string            `json:"keyword,omitempty" yaml:"keyword,omitempty"`
	Headers        map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	IgnoreTLS      bool              `json:"ignore_tls,omitempty" yaml:"ignore_tls,omitempty"` // also Redis (rediss://)

	// DNS
	RecordType string `json:"record_type,omitempty" yaml:"record_type,omitempty"`
	Resolver   string `json:"resolver,omitempty" yaml:"resolver,omitempty"`
	// Expected is a substring of the DNS answers, or the exact first column
	// of the first row a database query returns.
	Expected string `json:"expected,omitempty" yaml:"expected,omitempty"`

	// Postgres, MySQL: optional query, run read-only; default SELECT 1
	Query string `json:"query,omitempty" yaml:"query,omitempty"`

	// TLS
	MinDaysRemaining int `json:"min_days_remaining,omitempty" yaml:"min_days_remaining,omitempty"`
}

// Interval returns the check interval as a duration.
func (c Check) Interval() time.Duration { return time.Duration(c.IntervalSeconds) * time.Second }

// Timeout returns the check timeout as a duration.
func (c Check) Timeout() time.Duration { return time.Duration(c.TimeoutSeconds) * time.Second }

func (c *Check) normalize() error {
	c.Target = strings.TrimSpace(c.Target)
	if c.IntervalSeconds == 0 {
		c.IntervalSeconds = 60
	}
	if c.IntervalSeconds < 10 {
		return errors.New("interval must be at least 10 seconds")
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 10
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > c.IntervalSeconds {
		return errors.New("timeout must be between 1 second and the interval")
	}
	if c.FailureThreshold == 0 {
		c.FailureThreshold = 2
	}
	if c.FailureThreshold < 1 || c.FailureThreshold > 10 {
		return errors.New("failure threshold must be between 1 and 10")
	}
	if c.RecoveryThreshold == 0 {
		c.RecoveryThreshold = 1
	}
	if c.RecoveryThreshold < 1 || c.RecoveryThreshold > 10 {
		return errors.New("recovery threshold must be between 1 and 10")
	}
	ct, ok := LookupCheckType(c.Type)
	if !ok {
		return fmt.Errorf("unsupported check type %q", c.Type)
	}
	if err := ct.Normalize(c); err != nil {
		return err
	}
	var err error
	c.Config, err = c.Config.onlyKeys(ct.keys())
	return err
}

// Result is the outcome of one healthcheck probe.
type Result struct {
	MonitorID int64     `json:"monitor_id"`
	Time      time.Time `json:"time"`
	OK        bool      `json:"ok"`
	LatencyMS int64     `json:"latency_ms"`
	Message   string    `json:"message"`
	// StatusCode is the HTTP status the check got, whatever it was; 0 when
	// there was no response or it isn't an HTTP check.
	StatusCode int `json:"status_code,omitempty"`
	// ErrorClass is why a failed check failed (timeout, dns, tls, connect,
	// http_5xx, http_4xx, assertion, other); "" when it was up.
	ErrorClass string `json:"error_class,omitempty"`
}

// StatusRange is an inclusive HTTP status code range.
type StatusRange struct{ Min, Max int }

// ParseStatusRanges parses "200-299,301,404".
func ParseStatusRanges(s string) ([]StatusRange, error) {
	var out []StatusRange
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		var r StatusRange
		if lo, hi, ok := strings.Cut(part, "-"); ok {
			if _, err := fmt.Sscanf(lo+" "+hi, "%d %d", &r.Min, &r.Max); err != nil {
				return nil, fmt.Errorf("invalid status range %q", part)
			}
		} else {
			if _, err := fmt.Sscanf(part, "%d", &r.Min); err != nil {
				return nil, fmt.Errorf("invalid status code %q", part)
			}
			r.Max = r.Min
		}
		if r.Min < 100 || r.Max > 599 || r.Min > r.Max {
			return nil, fmt.Errorf("invalid status range %q", part)
		}
		out = append(out, r)
	}
	if len(out) == 0 {
		return nil, errors.New("expected status is empty")
	}
	return out, nil
}
