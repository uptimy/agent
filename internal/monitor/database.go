package monitor

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// IsDatabase reports whether t is a database check, whose target is a
// connection URL that may carry a password.
func (t Type) IsDatabase() bool {
	return t == TypePostgres || t == TypeMySQL || t == TypeRedis
}

// databaseSchemes lists the URL schemes each database type accepts.
var databaseSchemes = map[Type][]string{
	TypePostgres: {"postgres", "postgresql"},
	TypeMySQL:    {"mysql", "mariadb"},
	TypeRedis:    {"redis", "rediss"},
}

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// agentEnv are variables the agent reads for its own configuration. A
// database target can't reference them, so an admin can't point a monitor at
// a server they control and read, say, ADMIN_PASSWORD out of the connection.
var agentEnv = []string{
	"PORT", "HOST", "DATA_DIR", "ADMIN_USERNAME", "ADMIN_PASSWORD",
	"MONITORS_FILE", "MONITORS_YAML", "UI_DEV_SERVER", "AGENT_NAME",
}

func isAgentEnv(name string) bool {
	return slices.Contains(agentEnv, name) ||
		strings.HasPrefix(name, "UPTIMY_") ||
		strings.HasPrefix(name, "RETENTION_")
}

// EnvRefs returns the environment variables a database target references,
// in order.
func (c Check) EnvRefs() []string {
	if !c.Type.IsDatabase() {
		return nil
	}
	var names []string
	for _, ref := range envRef.FindAllStringSubmatch(c.Target, -1) {
		if !slices.Contains(names, ref[1]) {
			names = append(names, ref[1])
		}
	}
	return names
}

// ResolvedTarget returns the target with ${VAR} references replaced by
// environment variables, so connection strings (or just their passwords) can
// come from a Kubernetes Secret or a Railway reference variable instead of
// being stored in the agent. Only database targets are expanded; the stored
// target keeps the reference.
func (c Check) ResolvedTarget() (string, error) {
	if !c.Type.IsDatabase() {
		return c.Target, nil
	}
	var err error
	out := envRef.ReplaceAllStringFunc(c.Target, func(ref string) string {
		name := envRef.FindStringSubmatch(ref)[1]
		if isAgentEnv(name) {
			err = errors.Join(err, fmt.Errorf("environment variable %s is reserved for the agent's own settings", name))
			return ""
		}
		v, ok := os.LookupEnv(name)
		if !ok {
			err = errors.Join(err, fmt.Errorf("environment variable %s is not set", name))
		}
		return v
	})
	return out, err
}

// DisplayTarget is the target with any password masked, for the UI list,
// viewers and alert messages. ${VAR} references are shown as written.
func (c Check) DisplayTarget() string {
	if !c.Type.IsDatabase() {
		return c.Target
	}
	return MaskPassword(c.Target)
}

// MaskPassword hides the password in scheme://user:password@host/... . It
// works on the raw string so it also masks targets that don't parse as URLs.
func MaskPassword(target string) string {
	_, rest, ok := strings.Cut(target, "://")
	if !ok {
		return target
	}
	start := len(target) - len(rest)
	authority := rest
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority = rest[:i]
	}
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return target
	}
	colon := strings.Index(authority[:at], ":")
	if colon < 0 || colon == at-1 {
		return target
	}
	return target[:start+colon+1] + "••••••" + target[start+at:]
}

// normalizeDatabase validates a database monitor's connection URL.
func (c *Check) normalizeDatabase() error {
	if c.Target == "" {
		return errors.New("connection URL is required")
	}
	unset := false
	for _, ref := range envRef.FindAllStringSubmatch(c.Target, -1) {
		if isAgentEnv(ref[1]) {
			return fmt.Errorf("environment variable %s is reserved for the agent's own settings", ref[1])
		}
		if _, ok := os.LookupEnv(ref[1]); !ok {
			unset = true
		}
	}
	// A variable that isn't set yet fails the check with a clear message
	// rather than rejecting the monitor (or a whole monitors file); the URL is
	// validated once it can be resolved.
	if !unset {
		if err := c.validateURL(); err != nil {
			return err
		}
	}
	if c.Type == TypeRedis {
		c.Config.Query, c.Config.Expected = "", ""
		return nil
	}
	c.Config.Query = strings.TrimSpace(c.Config.Query)
	c.Config.Expected = strings.TrimSpace(c.Config.Expected)
	if c.Config.Expected != "" && c.Config.Query == "" {
		return errors.New("an expected value needs a query")
	}
	return nil
}

func (c *Check) validateURL() error {
	target, err := c.ResolvedTarget()
	if err != nil {
		return err
	}
	schemes := databaseSchemes[c.Type]
	example := map[Type]string{ //nolint:gosec // G101: example URLs for the error message, not credentials
		TypePostgres: "postgres://user:password@host:5432/db",
		TypeMySQL:    "mysql://user:password@host:3306/db",
		TypeRedis:    "redis://:password@host:6379/0",
	}[c.Type]
	u, err := url.Parse(target)
	if err != nil || !slices.Contains(schemes, u.Scheme) || u.Hostname() == "" {
		return fmt.Errorf("target must be a %s URL, e.g. %s", schemes[0], example)
	}
	if db := strings.Trim(u.Path, "/"); c.Type == TypeRedis && db != "" {
		if n, err := strconv.Atoi(db); err != nil || n < 0 {
			return fmt.Errorf("redis database %q must be a number", db)
		}
	}
	return nil
}
