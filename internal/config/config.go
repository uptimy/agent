// Package config reads agent settings from the environment.
package config

import (
	"fmt"
	"net/netip"
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

	RetentionDays int

	// TrustedProxies are the addresses whose X-Forwarded-For and
	// X-Forwarded-Proto headers are believed: who the client is (for sign-in
	// rate limits) and whether it used HTTPS (for Secure cookies).
	TrustedProxies []netip.Prefix

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
	// UptimyIngestURL receives Cloud sync uploads.
	UptimyIngestURL string

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
		UptimyIngestURL:     env("UPTIMY_INGEST_URL", "https://ingest.upti.my"),
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
	if c.TrustedProxies, err = ParseTrustedProxies(env("TRUSTED_PROXIES", "private")); err != nil {
		return c, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	return c, nil
}

// PrivateNetworks are loopback and private addresses, where an Ingress
// controller, Railway's edge or a reverse proxy on the same host connects
// from. The default for TRUSTED_PROXIES.
var PrivateNetworks = []netip.Prefix{
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT, used inside some platforms
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
}

// ParseTrustedProxies reads TRUSTED_PROXIES: comma-separated addresses and
// CIDR ranges, "private" for PrivateNetworks, or "none".
func ParseTrustedProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
		case part == "none":
			if strings.TrimSpace(v) != "none" {
				return nil, fmt.Errorf("none can't be combined with addresses")
			}
			return nil, nil
		case part == "private":
			out = append(out, PrivateNetworks...)
		case strings.Contains(part, "/"):
			p, err := netip.ParsePrefix(part)
			if err != nil {
				return nil, fmt.Errorf("%q isn't an address range like 10.0.0.0/8", part)
			}
			out = append(out, p.Masked())
		default:
			a, err := netip.ParseAddr(part)
			if err != nil {
				return nil, fmt.Errorf("%q isn't an IP address", part)
			}
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		}
	}
	return out, nil
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
