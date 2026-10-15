package notify

import (
	"errors"
	"fmt"
	"net/http"
	"net/textproto"
	"strconv"
	"time"
)

// An alert is delivered to each channel that covers its monitor, and each
// delivery is tracked on its own: a failing channel is retried without
// sending the alert twice to the others. See internal/delivery.

// DeliveryState is where one alert stands on one channel.
type DeliveryState string

const (
	DeliveryPending    DeliveryState = "pending"    // waiting for its first or next attempt
	DeliveryHeld       DeliveryState = "held"       // held back by a maintenance window
	DeliverySent       DeliveryState = "sent"       // the channel accepted it
	DeliveryFailed     DeliveryState = "failed"     // the channel refused it; retrying can't help
	DeliveryExpired    DeliveryState = "expired"    // still failing when it got too old to matter
	DeliverySuppressed DeliveryState = "suppressed" // not sent on purpose; Detail says why
)

// Done reports whether nothing more will happen to the delivery unless
// someone retries it.
func (s DeliveryState) Done() bool {
	return s == DeliverySent || s == DeliveryFailed || s == DeliveryExpired || s == DeliverySuppressed
}

// Delivery is one alert on one channel.
type Delivery struct {
	AlertID int64 `json:"alert_id"`
	// AlertType is the alert's Type: empty for a status change.
	AlertType    string        `json:"alert_type,omitempty"`
	NotifierID   int64         `json:"notifier_id"`
	NotifierName string        `json:"notifier_name"`
	NotifierType string        `json:"notifier_type"`
	State        DeliveryState `json:"state"`
	Attempts     int           `json:"attempts"`
	// NextAttempt is when a pending delivery is tried next.
	NextAttempt time.Time `json:"next_attempt_at,omitzero"`
	// ExpiresAt is when a pending delivery stops being retried.
	ExpiresAt time.Time `json:"-"`
	// Detail is the last error, or why it was suppressed.
	Detail string     `json:"detail,omitempty"`
	DoneAt *time.Time `json:"done_at"`
}

// HTTPError is a webhook answering with a status other than 2xx.
type HTTPError struct {
	Status int
	Body   string // the start of the response, for the error message
	// RetryAfter is the server's Retry-After, if it sent one.
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body)
}

// permanentError marks an error that retrying can't fix.
type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// Permanent marks err as one that retrying can't fix.
func Permanent(err error) error {
	if err == nil {
		return nil
	}
	return permanentError{err}
}

// IsPermanent reports whether retrying err can't help: the channel refused
// the alert (a deleted webhook, a revoked token, a rejected recipient)
// rather than being unreachable or overloaded.
func IsPermanent(err error) bool {
	if errors.As(err, new(permanentError)) {
		return true
	}
	var he *HTTPError
	if errors.As(err, &he) {
		switch he.Status {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			return false
		}
		return he.Status >= 400 && he.Status < 500
	}
	var te *textproto.Error
	if errors.As(err, &te) {
		return te.Code >= 500 // SMTP: 5xx is a permanent failure, 4xx a temporary one
	}
	return false
}

// RetryAfter is how long the channel asked to wait before trying again, or
// zero.
func RetryAfter(err error) time.Duration {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.RetryAfter
	}
	return 0
}

// parseRetryAfter reads a Retry-After header: seconds, or an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}
