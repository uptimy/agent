package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Behind a proxy (an Ingress, Railway's edge, nginx), every request comes
// from the proxy's address, and the proxy says who the client was in
// X-Forwarded-For and whether it came over HTTPS in X-Forwarded-Proto.
// Anyone can send those headers, so they're only believed from addresses in
// TRUSTED_PROXIES (config.TrustedProxies).

// trusted reports whether addr is a proxy whose forwarding headers count.
func (s *Server) trusted(addr netip.Addr) bool {
	for _, p := range s.Config.TrustedProxies {
		if p.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

func remoteAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	return a.Unmap(), err == nil
}

// clientIP is the address the request came from: the connection's, or,
// through trusted proxies, the nearest address in X-Forwarded-For that
// isn't one of them (anything further left could be made up by the client).
func (s *Server) clientIP(r *http.Request) string {
	remote, ok := remoteAddr(r)
	if !ok {
		return r.RemoteAddr
	}
	if !s.trusted(remote) {
		return remote.String()
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	client := remote
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break // garbage: stop at the last address we could trust
		}
		client = a.Unmap()
		if !s.trusted(client) {
			break
		}
	}
	return client.String()
}

// isHTTPS reports whether the client reached the agent over HTTPS: directly,
// or through a trusted proxy that says so.
func (s *Server) isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	remote, ok := remoteAddr(r)
	if !ok || !s.trusted(remote) {
		return false
	}
	// With several proxies, the first value is what the client used.
	proto, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}
