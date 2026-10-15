package notify

import (
	"errors"
	"fmt"
	"net/textproto"
	"testing"
	"time"
)

func TestIsPermanent(t *testing.T) {
	for _, c := range []struct {
		err  error
		want bool
	}{
		{&HTTPError{Status: 400}, true},
		{&HTTPError{Status: 401}, true},
		{&HTTPError{Status: 404}, true},
		{&HTTPError{Status: 408}, false},
		{&HTTPError{Status: 429}, false},
		{&HTTPError{Status: 500}, false},
		{&HTTPError{Status: 503}, false},
		{fmt.Errorf("signing in: %w", &textproto.Error{Code: 535}), true},
		{&textproto.Error{Code: 421}, false},
		{Permanent(errors.New("unsupported")), true},
		{errors.New("connection refused"), false},
	} {
		if got := IsPermanent(c.err); got != c.want {
			t.Errorf("IsPermanent(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if got := parseRetryAfter("120", now); got != 2*time.Minute {
		t.Errorf("seconds: %v", got)
	}
	if got := parseRetryAfter("Fri, 09 Oct 2026 12:00:30 GMT", now); got != 30*time.Second {
		t.Errorf("date: %v", got)
	}
	for _, v := range []string{"", "soon", "-5", "Fri, 09 Oct 2026 11:00:00 GMT"} {
		if got := parseRetryAfter(v, now); got != 0 {
			t.Errorf("%q: %v", v, got)
		}
	}
}
