package mcp

import (
	"log/slog"
	"net/http"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/uptimy/agent/internal/identity"
	"github.com/uptimy/agent/internal/investigation"
)

type Options struct {
	AllowedHosts  []string
	MaxConcurrent int
	Log           *slog.Logger
}

func New(service *investigation.Service, tokens identity.TokenStore, opts Options) http.Handler {
	if service == nil || tokens == nil {
		return unavailable(opts.Log)
	}
	hosts, err := allowedHosts(opts.AllowedHosts)
	if err != nil {
		return unavailable(opts.Log)
	}
	if opts.MaxConcurrent == 0 {
		opts.MaxConcurrent = 8
	}
	if opts.MaxConcurrent < 1 || opts.MaxConcurrent > 128 {
		return unavailable(opts.Log)
	}
	server := sdk.NewServer(&sdk.Implementation{Name: "uptimy", Version: "1.0.0"}, nil)
	registerTools(server, service, opts.Log)
	transport := sdk.NewStreamableHTTPHandler(func(*http.Request) *sdk.Server { return server }, &sdk.StreamableHTTPOptions{
		Stateless: true, JSONResponse: true,
	})
	return protect(transport, tokens, hosts, opts.MaxConcurrent)
}

func unavailable(log *slog.Logger) http.Handler {
	if log != nil {
		log.Error("MCP unavailable: invalid configuration")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "MCP unavailable", http.StatusServiceUnavailable)
	})
}
