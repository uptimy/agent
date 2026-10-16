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
	if err != nil {
		return out, err
	}
	// A target with variables is validated once they are filled in, so a
	// variable set to something that isn't a URL fails this check rather
	// than the monitor's save (or the agent's start). The message names the
	// variables, never their values.
	if refs := c.EnvRefs(); len(refs) > 0 {
		if err := c.validateURL(out); err != nil {
			if len(refs) == 1 && c.Target == "${"+refs[0]+"}" {
				return "", fmt.Errorf("%s isn't %w", refs[0], err)
			}
			return "", fmt.Errorf("the target with %s filled in isn't %w", strings.Join(refs, ", "), err)
		}
	}
	return out, nil
}

// DisplayTarget is the target with its secrets masked, for the UI list,
// viewers, alert messages and Uptimy Cloud: the password in a URL and the
// values of query parameters such as token or api_key (see MaskSecrets).
// ${VAR} references are shown as written.
func (c Check) DisplayTarget() string {
	return MaskSecrets(c.Target)
}

// mask replaces a secret in a displayed target.
const mask = "••••••"

// MaskSecrets masks the password in scheme://user:password@host/... and the
// values of query parameters that look secret (secretParam), in any
// URL-shaped target. Targets without a scheme are returned as they are.
func MaskSecrets(target string) string {
	return maskQuery(MaskPassword(target))
}

// MaskPassword hides the password in scheme://user:password@host/... . It
// works on the raw string so it also masks targets that don't parse as URLs.
// A password that is a ${VAR} reference isn't a secret and is kept.
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
	if colon < 0 || colon == at-1 || isEnvRef(authority[colon+1:at]) {
		return target
	}
	return target[:start+colon+1] + mask + target[start+at:]
}

// secretParams are query parameter names, lowercased without - and _, whose
// values are masked: ?password=, ?access_token=, ?api-key=, ?sig= and so on.
var secretParams = []string{
	"password", "pass", "pwd", "token", "accesstoken", "apikey", "key",
	"secret", "signature", "sig", "auth", "clientsecret",
}

func secretParam(name string) bool {
	if n, err := url.QueryUnescape(name); err == nil {
		name = n
	}
	name = strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(name))
	return slices.Contains(secretParams, name)
}

// maskQuery masks the values of secret query parameters in a URL-shaped
// target, keeping everything else (order, encoding, the fragment) as written.
func maskQuery(target string) string {
	if !strings.Contains(target, "://") {
		return target
	}
	q := strings.Index(target, "?")
	if q < 0 {
		return target
	}
	query, fragment := target[q+1:], ""
	if i := strings.Index(query, "#"); i >= 0 {
		query, fragment = query[:i], query[i:]
	}
	params := strings.Split(query, "&")
	for i, p := range params {
		name, value, ok := strings.Cut(p, "=")
		if ok && value != "" && !isEnvRef(value) && secretParam(name) {
			params[i] = name + "=" + mask
		}
	}
	return target[:q+1] + strings.Join(params, "&") + fragment
}

// isEnvRef reports whether s is exactly one ${VAR} reference.
func isEnvRef(s string) bool {
	m := envRef.FindStringIndex(s)
	return m != nil && m[0] == 0 && m[1] == len(s)
}

// normalizeDatabase validates a database monitor's connection URL.
func (c *Check) normalizeDatabase() error {
	if c.Target == "" {
		return errors.New("connection URL is required")
	}
	refs := c.EnvRefs()
	for _, name := range refs {
		if isAgentEnv(name) {
			return fmt.Errorf("environment variable %s is reserved for the agent's own settings", name)
		}
	}
	// A variable that isn't set yet, or is set to something that isn't a
	// URL, fails the check with a clear message rather than rejecting the
	// monitor (or a whole monitors file): ResolvedTarget validates the URL
	// once the variables are filled in.
	if len(refs) == 0 {
		if err := c.validateURL(c.Target); err != nil {
			return fmt.Errorf("target must be %w", err)
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

// validateURL checks a resolved target. Its error reads after "isn't" or
// "must be", and never includes the target, which may hold a password.
func (c *Check) validateURL(target string) error {
	schemes := databaseSchemes[c.Type]
	example := map[Type]string{ //nolint:gosec // G101: example URLs for the error message, not credentials
		TypePostgres: "postgres://user:password@host:5432/db",
		TypeMySQL:    "mysql://user:password@host:3306/db",
		TypeRedis:    "redis://:password@host:6379/0",
	}[c.Type]
	u, err := url.Parse(target)
	if err != nil || !slices.Contains(schemes, u.Scheme) || u.Hostname() == "" {
		return fmt.Errorf("a %s URL, e.g. %s", schemes[0], example)
	}
	if db := strings.Trim(u.Path, "/"); c.Type == TypeRedis && db != "" {
		if n, err := strconv.Atoi(db); err != nil || n < 0 {
			return errors.New("a redis URL whose database is a number, e.g. " + example)
		}
	}
	return nil
}
