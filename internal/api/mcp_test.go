package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMCPRoutingAndStatusDomainIsolation(t *testing.T) {
	c := newTestServer(t)
	c.srv.MCP = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := c.srv.Handler(http.NotFoundHandler())
	c.srv.setStatusDomain("status.example.com")
	for _, tc := range []struct {
		host string
		code int
	}{{"status.example.com", 404}, {"agent.internal", 204}} {
		r := httptest.NewRequest(http.MethodPost, "/mcp", nil)
		r.Host = tc.host
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != tc.code {
			t.Fatalf("host %s: %d", tc.host, w.Code)
		}
	}
	c.srv.MCP = nil
	h = c.srv.Handler(http.NotFoundHandler())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if w.Code != 404 {
		t.Fatalf("disabled endpoint: %d", w.Code)
	}
}
