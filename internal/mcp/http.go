package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/uptimy/agent/internal/identity"
)

const maxBody = 64 << 10

func allowedHosts(values []string) (map[string]bool, error) {
	if len(values) == 0 {
		return nil, errors.New("MCP host allowlist is required")
	}
	hosts := make(map[string]bool, len(values))
	for _, value := range values {
		host, ok := authority(value)
		if !ok {
			return nil, errors.New("invalid MCP allowed host")
		}
		hosts[host] = true
	}
	return hosts, nil
}

func authority(raw string) (string, bool) {
	if raw == "" || strings.ContainsAny(raw, " /\\?#@\t\r\n,%") {
		return "", false
	}
	u, err := url.Parse("http://" + raw)
	if err != nil || u.Host != raw || u.User != nil || u.Hostname() == "" {
		return "", false
	}
	host := strings.ToLower(u.Hostname())
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else {
		if len(host) > 253 || strings.ContainsAny(raw, "[]") {
			return "", false
		}
		for _, label := range strings.Split(host, ".") {
			if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return "", false
			}
			for _, c := range label {
				if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
					return "", false
				}
			}
		}
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
		return net.JoinHostPort(host, strconv.Itoa(n)), true
	}
	if strings.HasSuffix(raw, ":") {
		return "", false
	}
	if strings.Contains(host, ":") {
		return "[" + host + "]", true
	}
	return host, true
}

func sameOrigin(r *http.Request, host string) bool {
	values := r.Header.Values("Origin")
	if len(values) == 0 {
		return true
	}
	if len(values) != 1 {
		return false
	}
	u, err := url.Parse(values[0])
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(values[0], "#") {
		return false
	}
	originHost, ok := authority(u.Host)
	return ok && originHost == host
}

func protect(next http.Handler, tokens identity.TokenStore, hosts map[string]bool, concurrent int) http.Handler {
	slots := make(chan struct{}, concurrent)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		host, ok := authority(r.Host)
		if !ok || !hosts[host] || !sameOrigin(r, host) {
			http.Error(w, "host or origin not allowed", http.StatusForbidden)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			http.Error(w, "server busy", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		deadline, _ := ctx.Deadline()
		controller := http.NewResponseController(w)
		if controller.SetReadDeadline(deadline) == nil {
			defer func() { _ = controller.SetReadDeadline(time.Time{}) }()
		}
		if controller.SetWriteDeadline(deadline) == nil {
			defer func() { _ = controller.SetWriteDeadline(time.Time{}) }()
		}
		p, err := identity.Authenticate(ctx, r, tokens)
		if err != nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			http.Error(w, "invalid API token", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxBody)
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "invalid request body", http.StatusBadRequest)
			}
			return
		}
		body = bytes.TrimSpace(body)
		if len(body) == 0 || body[0] != '{' || !json.Valid(body) {
			http.Error(w, "expected a single JSON-RPC request", http.StatusBadRequest)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(w, r.WithContext(identity.WithPrincipal(ctx, p)))
	})
}
