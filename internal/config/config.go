// Package config reads agent settings from the environment.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config is the runtime configuration. Everything comes from env vars so the
// same image runs unchanged on Kubernetes, Railway and plain Docker.
//
// Env vars are for how the process runs, for secrets, and for declarative
// monitors. Everything else (the status page, alert channels, Uptimy
// alerting) is set in the UI and stored in the database.
type Config struct {
	Addr          string
	DataDir       string
	AdminUsername string
	AdminPassword string

	// Declarative monitors: a YAML file path, or the YAML itself (handy on
	// platforms where mounting files is awkward).
	MonitorsFile string
	MonitorsYAML string

	RetentionDays    int
	MCPEnabled       bool
	MCPAllowedHosts  []string
	MCPMaxConcurrent int

	// KubernetesDiscovery creates monitors for resources labeled
	// upti.my/monitor=true when running in a cluster. On by default: nothing
	// happens until something is labeled.
	KubernetesDiscovery bool

	// UptimyHeartbeatURL pins the "Watch the watcher" check-in URL, for
	// agents whose database doesn't survive a restart. Usually it's set up
	// with "Connect to Uptimy" in the UI instead.
	UptimyHeartbeatURL string

	// Uptimy endpoints used by "Connect to Uptimy". Only change these for
	// development against a local Uptimy.
	UptimyAppURL        string
	UptimyAPIURL        string
	UptimyHeartbeatsURL string

	// AgentName labels this agent in Uptimy (consent screen, heartbeat name).
	// Defaults to the hostname, which in Kubernetes is a random pod name.
	AgentName string

	// UIDevServer proxies the UI to a Vite dev server (set by `make run`).
	UIDevServer string
}

// Load reads the configuration from the environment.
func Load() (Config, error) {
	c := Config{
		Addr:                ":" + env("PORT", "8080"),
		DataDir:             env("DATA_DIR", "./data"),
		AdminUsername:       env("ADMIN_USERNAME", "admin"),
		AdminPassword:       os.Getenv("ADMIN_PASSWORD"),
		MonitorsFile:        os.Getenv("MONITORS_FILE"),
		MonitorsYAML:        os.Getenv("MONITORS_YAML"),
		UptimyHeartbeatURL:  os.Getenv("UPTIMY_HEARTBEAT_URL"),
		UIDevServer:         os.Getenv("UI_DEV_SERVER"),
		UptimyAppURL:        env("UPTIMY_APP_URL", "https://app.upti.my"),
		UptimyAPIURL:        env("UPTIMY_API_URL", "https://api.upti.my"),
		UptimyHeartbeatsURL: env("UPTIMY_HEARTBEATS_URL", "https://heartbeats.upti.my"),
		AgentName:           os.Getenv("AGENT_NAME"),
	}
	if c.AgentName == "" {
		c.AgentName, _ = os.Hostname()
	}
	if c.AgentName == "" {
		c.AgentName = "uptimy-agent"
	}
	if host := os.Getenv("HOST"); host != "" {
		c.Addr = host + c.Addr
	}

	var err error
	if c.KubernetesDiscovery, err = envBool("KUBERNETES_DISCOVERY", true); err != nil {
		return c, err
	}
	if c.RetentionDays, err = envInt("RETENTION_DAYS", 30); err != nil {
		return c, err
	}
	if c.RetentionDays < 1 {
		return c, fmt.Errorf("RETENTION_DAYS must be at least 1")
	}
	if c.MCPEnabled, err = envBool("MCP_ENABLED", false); err != nil {
		return c, err
	}
	if c.MCPMaxConcurrent, err = envInt("MCP_MAX_CONCURRENT", 4); err != nil {
		return c, err
	}
	if c.MCPMaxConcurrent < 1 || c.MCPMaxConcurrent > 32 {
		return c, fmt.Errorf("MCP_MAX_CONCURRENT must be between 1 and 32")
	}
	for _, host := range strings.Split(os.Getenv("MCP_ALLOWED_HOSTS"), ",") {
		if host = strings.TrimSpace(host); host != "" {
			c.MCPAllowedHosts = append(c.MCPAllowedHosts, host)
		}
	}
	if c.MCPEnabled && len(c.MCPAllowedHosts) == 0 {
		return c, fmt.Errorf("MCP_ALLOWED_HOSTS is required when MCP_ENABLED=true")
	}
	return c, nil
}

// DatabasePath is where the SQLite database lives.
func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "uptimy-agent.db") }

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	return n, nil
}

func envBool(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return b, nil
}
