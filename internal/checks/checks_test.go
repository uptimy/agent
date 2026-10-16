package checks

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/uptimy/agent/internal/monitor"
	"github.com/uptimy/agent/internal/version"
)

// normalized returns c with its defaults filled in, as a saved healthcheck has.
func normalized(t *testing.T, c monitor.Check) monitor.Check {
	t.Helper()
	m := monitor.Monitor{Kind: monitor.KindHealthcheck, Name: "t", Check: &c}
	if err := m.Normalize(); err != nil {
		t.Fatal(err)
	}
	return *m.Check
}

func httpMonitor(t *testing.T, target string, cfg monitor.Config) monitor.Check {
	return normalized(t, monitor.Check{Type: monitor.TypeHTTP, Target: target, Config: cfg})
}

func TestHTTPUserAgent(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.UserAgent() }))
	defer srv.Close()
	c := New(nil)
	ctx := context.Background()

	c.Run(ctx, httpMonitor(t, srv.URL, monitor.Config{}))
	if got != version.UserAgent() {
		t.Fatalf("user agent %q, want %q", got, version.UserAgent())
	}
	// A monitor's own header wins.
	c.Run(ctx, httpMonitor(t, srv.URL, monitor.Config{Headers: map[string]string{"user-agent": "acme-probe"}}))
	if got != "acme-probe" {
		t.Fatalf("user agent %q, want the monitor's own", got)
	}
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fail":
			w.WriteHeader(http.StatusInternalServerError)
			return
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()
	c := New(nil)
	ctx := context.Background()

	if out := c.Run(ctx, httpMonitor(t, srv.URL, monitor.Config{})); !out.OK || out.StatusCode != http.StatusOK || out.ErrorClass != "" {
		t.Fatalf("expected ok with 200: %+v", out)
	}
	if out := c.Run(ctx, httpMonitor(t, srv.URL+"/fail", monitor.Config{})); out.OK || !strings.Contains(out.Message, "500") ||
		out.StatusCode != http.StatusInternalServerError || out.ErrorClass != classHTTP5xx {
		t.Fatalf("expected 500 failure: %+v", out)
	}
	if out := c.Run(ctx, httpMonitor(t, srv.URL+"/fail", monitor.Config{ExpectedStatus: "500"})); !out.OK || out.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 to be accepted: %+v", out)
	}
	if out := c.Run(ctx, httpMonitor(t, srv.URL, monitor.Config{ExpectedStatus: "500"})); out.OK || out.StatusCode != http.StatusOK || out.ErrorClass != classAssertion {
		t.Fatalf("an unexpected 2xx is an assertion failure: %+v", out)
	}
	if out := c.Run(ctx, httpMonitor(t, srv.URL+"/missing", monitor.Config{ExpectedStatus: "200"})); out.OK || out.ErrorClass != classHTTP4xx {
		t.Fatalf("a 404 is http_4xx: %+v", out)
	}
	if out := c.Run(ctx, httpMonitor(t, srv.URL, monitor.Config{Keyword: "degraded"})); out.OK || out.StatusCode != http.StatusOK || out.ErrorClass != classAssertion {
		t.Fatalf("missing keyword should fail as an assertion: %+v", out)
	}
}

func TestHTTPTransportErrors(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()
	c := New(nil)
	if out := c.Run(context.Background(), httpMonitor(t, closed, monitor.Config{})); out.OK || out.StatusCode != 0 || out.ErrorClass != classConnect {
		t.Fatalf("refused: %+v", out)
	}

	// A certificate the agent doesn't trust.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	if out := c.Run(context.Background(), httpMonitor(t, srv.URL, monitor.Config{})); out.OK || out.ErrorClass != classTLS {
		t.Fatalf("untrusted certificate: %+v", out)
	}

	// Out of time is a timeout.
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	defer slow.Close()
	defer close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if out := c.Run(ctx, httpMonitor(t, slow.URL, monitor.Config{})); out.OK || out.ErrorClass != classTimeout {
		t.Fatalf("timeout: %+v", out)
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, ""},
		{&net.DNSError{Err: "no such host", Name: "nope.invalid", IsNotFound: true}, classDNS},
		{&net.DNSError{Err: "i/o timeout", IsTimeout: true}, classDNS}, // a lookup that timed out is still DNS
		{fmt.Errorf("get: %w", context.DeadlineExceeded), classTimeout},
		{&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, classConnect},
		{&net.OpError{Op: "read", Err: syscall.ECONNRESET}, classConnect},
		{x509.UnknownAuthorityError{}, classTLS},
		{errors.New("something else"), classOther},
	} {
		if got := classify(tc.err); got != tc.want {
			t.Errorf("classify(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
	for code, want := range map[int]string{503: classHTTP5xx, 404: classHTTP4xx, 302: classAssertion, 200: classAssertion} {
		if got := classForStatus(code); got != want {
			t.Errorf("classForStatus(%d) = %q, want %q", code, got, want)
		}
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ctx := context.Background()
	if out := tcp(ctx, addr); !out.OK {
		t.Fatalf("expected connect: %s", out.Message)
	}
	ln.Close()
	if out := tcp(ctx, addr); out.OK || out.ErrorClass != classConnect {
		t.Fatalf("expected a connect failure after close: %+v", out)
	}
}

func TestKubernetesUnavailable(t *testing.T) {
	m := monitor.Check{Type: monitor.TypeKubernetes, Target: "default/deployment/api"}
	if out := New(nil).Run(context.Background(), m); out.OK || out.ErrorClass != classOther {
		t.Fatalf("expected failure outside a cluster: %+v", out)
	}
}
