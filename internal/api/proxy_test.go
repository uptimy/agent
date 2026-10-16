package api

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uptimy/agent/internal/config"
)

func TestClientIPAndHTTPS(t *testing.T) {
	trust := func(v string) *Server {
		p, err := config.ParseTrustedProxies(v)
		if err != nil {
			t.Fatal(err)
		}
		return &Server{Config: config.Config{TrustedProxies: p}}
	}
	for _, c := range []struct {
		name, trusted, remote, xff, proto string
		wantIP                            string
		wantHTTPS                         bool
	}{
		{"direct", "private", "203.0.113.9:5000", "", "", "203.0.113.9", false},
		{"spoofed from outside", "private", "203.0.113.9:5000", "1.2.3.4", "https", "203.0.113.9", false},
		{"behind an ingress", "private", "10.42.0.7:5000", "198.51.100.20", "https", "198.51.100.20", true},
		{"spoofed through an ingress", "private", "10.42.0.7:5000", "1.2.3.4, 198.51.100.20", "https", "198.51.100.20", true},
		{"two proxies", "private", "127.0.0.1:5000", "198.51.100.20, 10.0.0.5", "https,http", "198.51.100.20", true},
		{"only proxies", "private", "10.42.0.7:5000", "10.0.0.9", "http", "10.0.0.9", false},
		{"garbage", "private", "10.42.0.7:5000", "nonsense", "", "10.42.0.7", false},
		{"none trusted", "none", "10.42.0.7:5000", "198.51.100.20", "https", "10.42.0.7", false},
		{"one proxy", "192.0.2.1", "192.0.2.1:443", "198.51.100.20", "HTTPS", "198.51.100.20", true},
		{"IPv6 proxy", "fd00::/8", "[fd00::1]:5000", "2001:db8::7", "https", "2001:db8::7", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := trust(c.trusted)
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remote
			if c.xff != "" {
				r.Header.Set("X-Forwarded-For", c.xff)
			}
			if c.proto != "" {
				r.Header.Set("X-Forwarded-Proto", c.proto)
			}
			if got := s.clientIP(r); got != c.wantIP {
				t.Errorf("client IP %s, want %s", got, c.wantIP)
			}
			if got := s.isHTTPS(r); got != c.wantHTTPS {
				t.Errorf("HTTPS %v, want %v", got, c.wantHTTPS)
			}
		})
	}

	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{}
	if !trust("none").isHTTPS(r) {
		t.Error("a TLS connection is HTTPS")
	}
	for _, bad := range []string{"10.0.0.0/33", "proxy.local", "none,10.0.0.1"} {
		if _, err := config.ParseTrustedProxies(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
