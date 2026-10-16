package monitor

import (
	"strings"
	"testing"
)

func TestNormalizeDatabase(t *testing.T) {
	ok := []Monitor{
		healthcheck("a", Check{Type: TypePostgres, Target: "postgres://app:pw@db:5432/app?sslmode=require"}),
		healthcheck("a", Check{Type: TypePostgres, Target: "postgresql://db/app"}),
		healthcheck("a", Check{Type: TypeMySQL, Target: "mysql://root:pw@db/shop?tls=true"}),
		healthcheck("a", Check{Type: TypeMySQL, Target: "mariadb://db:3307/shop"}),
		healthcheck("a", Check{Type: TypeRedis, Target: "redis://:pw@cache:6379/2"}),
		healthcheck("a", Check{Type: TypeRedis, Target: "rediss://default:pw@cache"}),
		healthcheck("a", Check{Type: TypePostgres, Target: "postgres://db/app", Config: Config{Query: "SELECT pg_is_in_recovery()", Expected: "false"}}),
		// Unset variables fail the check, not the save (or a whole monitors file).
		healthcheck("a", Check{Type: TypePostgres, Target: "${AGENT_TEST_UNSET_VAR}"}),
		healthcheck("a", Check{Type: TypeRedis, Target: "redis://:${AGENT_TEST_UNSET_VAR}@cache:6379/0"}),
		// So do variables set to something that isn't a URL (TestResolvedTargetInvalid).
		healthcheck("a", Check{Type: TypePostgres, Target: "${AGENT_TEST_NOT_A_URL}"}),
	}
	t.Setenv("AGENT_TEST_NOT_A_URL", "db.internal:5432")
	for _, m := range ok {
		if err := m.Normalize(); err != nil {
			t.Errorf("%s %s: %v", m.Check.Type, m.Check.Target, err)
		}
	}
	bad := map[string]Monitor{
		"empty":             healthcheck("a", Check{Type: TypePostgres}),
		"wrong scheme":      healthcheck("a", Check{Type: TypePostgres, Target: "mysql://db/app"}),
		"host:port only":    healthcheck("a", Check{Type: TypeMySQL, Target: "db:3306"}),
		"no host":           healthcheck("a", Check{Type: TypeRedis, Target: "redis://"}),
		"redis db not num":  healthcheck("a", Check{Type: TypeRedis, Target: "redis://cache/sessions"}),
		"expected no query": healthcheck("a", Check{Type: TypeMySQL, Target: "mysql://db/app", Config: Config{Expected: "1"}}),
		"agent env":         healthcheck("a", Check{Type: TypePostgres, Target: "postgres://admin:${ADMIN_PASSWORD}@db/app"}),
		"uptimy env":        healthcheck("a", Check{Type: TypePostgres, Target: "postgres://x:${UPTIMY_HEARTBEAT_URL}@db/app"}),
	}
	for name, m := range bad {
		if err := m.Normalize(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestResolvedTarget(t *testing.T) {
	t.Setenv("PG_URL", "postgres://app:s3cret@db:5432/app")
	t.Setenv("PG_PASSWORD", "p$ss")
	cases := map[string]string{
		"${PG_URL}":                          "postgres://app:s3cret@db:5432/app",
		"postgres://app:${PG_PASSWORD}@db/a": "postgres://app:p$ss@db/a",
		"postgres://app:pa$word@db/a":        "postgres://app:pa$word@db/a", // a bare $ is literal
	}
	for in, want := range cases {
		m := Check{Type: TypePostgres, Target: in}
		got, err := m.ResolvedTarget()
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", in, got, err, want)
		}
	}
	// Only database targets are expanded.
	m := Check{Type: TypeHTTP, Target: "https://x/${PG_URL}"}
	if got, _ := m.ResolvedTarget(); got != m.Target {
		t.Errorf("http target expanded: %q", got)
	}
}

func TestMaskPassword(t *testing.T) {
	cases := map[string]string{
		"postgres://app:s3cret@db:5432/app":      "postgres://app:••••••@db:5432/app",
		"redis://:s3cret@cache:6379/0":           "redis://:••••••@cache:6379/0",
		"mysql://root:p@ss@db/shop":              "mysql://root:••••••@db/shop",
		"postgres://app@db/app":                  "postgres://app@db/app",
		"postgres://db/app?password=x":           "postgres://db/app?password=x",
		"${DATABASE_URL}":                        "${DATABASE_URL}",
		"postgres://app:${PG_PASSWORD}@db/app":   "postgres://app:••••••@db/app",
		"postgres://db/app?options=a@b:c":        "postgres://db/app?options=a@b:c",
		"redis://cache:6379":                     "redis://cache:6379",
		"not a url with user:pass@host":          "not a url with user:pass@host",
		"rediss://default:tok@x.upstash.io:6379": "rediss://default:••••••@x.upstash.io:6379",
	}
	for in, want := range cases {
		if got := MaskPassword(in); got != want {
			t.Errorf("%s: got %q, want %q", in, got, want)
		}
	}
	// Non-database targets are shown as is.
	m := Check{Type: TypeHTTP, Target: "https://u:p@x"}
	if m.DisplayTarget() != m.Target {
		t.Error("http target masked")
	}
	if !strings.Contains(Check{Type: TypeRedis, Target: "redis://:pw@c"}.DisplayTarget(), "••••••") {
		t.Error("redis target not masked")
	}
}

func TestResolvedTargetInvalid(t *testing.T) {
	t.Setenv("PG_URL", "db.internal:5432 user=app password=hunter2")
	t.Setenv("PG_PASSWORD", "hunter2")
	t.Setenv("PG_HOST", "")
	cases := map[string]string{
		"${PG_URL}": "PG_URL isn't a postgres URL",
		"postgres://app:${PG_PASSWORD}@${PG_HOST}/a": "the target with PG_PASSWORD, PG_HOST filled in isn't a postgres URL",
	}
	for in, want := range cases {
		m := Check{Type: TypePostgres, Target: in}
		got, err := m.ResolvedTarget()
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%s: got %v, want %q", in, err, want)
			continue
		}
		if got != "" || strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "db.internal") {
			t.Errorf("%s: the value leaked: %q, %v", in, got, err)
		}
	}
	// A monitor using one is still valid, so it can be saved or loaded.
	m := healthcheck("a", Check{Type: TypePostgres, Target: "${PG_URL}"})
	if err := m.Normalize(); err != nil {
		t.Errorf("normalize: %v", err)
	}
}
