# Contributing to Uptimy Agent

Thanks for helping. Bug reports, fixes, new check types and notification channels are all welcome. This page covers how to get set up, how the code is laid out, and the two most common contributions step by step.

For anything bigger than a fix (a new feature, a change in behavior), please [open an issue](https://github.com/uptimy/agent/issues/new/choose) first so we can agree on the approach before you spend time on it.

## Setting up

You need Go 1.26+ and Node 22+.

```bash
git clone https://github.com/uptimy/agent && cd agent
make run
```

Open http://localhost:8080 and sign in as `admin` / `uptimy-dev`. Saving a `.go` file rebuilds and restarts the agent; UI changes hot-reload. If port 8080 is taken: `PORT=9090 VITE_PORT=5174 make run`.

| Command | What it does |
| --- | --- |
| `make run` | Local development with hot reload |
| `make test` | `go vet`, Go tests with the race detector, TypeScript typecheck, UI tests |
| `make lint` | [golangci-lint](https://golangci-lint.run/welcome/install/), ESLint and Prettier, the same as CI |
| `make build` | The UI plus a single binary in `bin/` |

The database checks have integration tests that run against real servers when these are set (CI always runs them):

```bash
docker run -d --rm -p 55432:5432 -e POSTGRES_PASSWORD=pw postgres:17-alpine
docker run -d --rm -p 53306:3306 -e MYSQL_ROOT_PASSWORD=pw mysql:8.4
docker run -d --rm -p 56379:6379 redis:7-alpine

AGENT_TEST_POSTGRES_URL='postgres://postgres:pw@localhost:55432/postgres?sslmode=disable' \
AGENT_TEST_MYSQL_URL='mysql://root:pw@127.0.0.1:53306/mysql' \
AGENT_TEST_REDIS_URL='redis://localhost:56379' \
go test ./internal/checks/
```

## How the code is laid out

See [ARCHITECTURE.md](ARCHITECTURE.md) for how the pieces fit together. In short:

```
cmd/uptimy-agent/    entrypoint and wiring
internal/monitor/    healthchecks (check_*.go, one per check type) and heartbeats (heartbeat.go)
internal/checks/     the probes that touch the network, one file per type
internal/notify/     notification channels, one file per channel
internal/maintenance/ maintenance windows
internal/statuspage/ the status page's settings, sections and logos
internal/kuma/       reads an Uptime Kuma database for importing
internal/schema/     describes a type's settings so the UI can render its form
internal/scheduler/  runs each healthcheck, tracks each heartbeat's schedule, sends alerts
internal/store/      SQLite
internal/api/        JSON API, sign-in and API tokens, live updates, heartbeat pings, the public status page
web/                 React + Vite + Tailwind UI, embedded into the binary
```

## Adding a check type

A check type (HTTP, Postgres, DNS, ...) is two Go files and a test; the UI builds its form from the type's description, so it needs no changes.

**1. Describe it** in `internal/monitor/check_<type>.go`: its label, the target field, its settings and how they're validated.

```go
package monitor

import (
	"errors"

	"github.com/uptimy/agent/internal/schema"
)

const TypeSMTP Type = "smtp"

func init() {
	RegisterCheckType(CheckType{
		Type:    TypeSMTP,
		Label:   "SMTP",
		Summary: "Mail server answers",
		Order:   90, // position in the type picker
		Target:  TargetSpec{Label: "Host and port", Placeholder: "mail.example.com:587"},
		Fields: []schema.Field{
			{Key: "starttls", Label: "Require STARTTLS", Input: schema.Switch},
		},
		Normalize: func(c *Check) error {
			if c.Target == "" {
				return errors.New("target is required")
			}
			return nil
		},
	})
}
```

Settings live in the shared `Config` struct (`internal/monitor/check.go`) under their YAML/JSON key; add a field there if you need a new one. `Normalize` gets the check after its generic fields (interval, timeout, threshold) are checked; fill defaults and return a clear, user-facing error. Settings that belong to other types are dropped automatically.

**2. Probe it** in `internal/checks/<type>.go`:

```go
func init() {
	register(monitor.TypeSMTP, func(ctx context.Context, c *Checker, check monitor.Check) Outcome {
		// Connect, check, and report. ctx carries the check's timeout.
		return Outcome{OK: true, Message: "220 mail.example.com ESMTP"}
	})
}
```

The `Message` is shown in the UI and in alerts: make failures say what went wrong ("connection refused", "certificate expired"). Never include secrets in it; see `dbFailure` for scrubbing a password out of a driver error.

**3. Test it** in `internal/checks/<type>_test.go`, ideally against a small fake server (see `fakeRedis` in `database_test.go`), and add an example under `healthchecks:` in `examples/monitors.yaml`; a test fails until every type has one. `TestEveryTypeHasAProbe` catches a type without a probe.

**4. Document it**: a line in the README's feature list, and a section if it has setup worth explaining.

## Adding a notification channel

Check the [Shoutrrr services](https://shoutrrr.nickfedor.com/latest/services/overview/) first: anything there already works through the "More services (Shoutrrr)" channel. A native channel is worth it when it does more, like PagerDuty resolving incidents or richer formatting.

One file, `internal/notify/<channel>.go`:

```go
func init() {
	Register(Channel{
		Type:  "gotify",
		Label: "Gotify",
		Help:  "Create an application in Gotify and paste its message URL, with the token.",
		Order: 70,
		Fields: []schema.Field{
			{Key: "url", Label: "Message URL", Input: schema.Password, Placeholder: "https://gotify.example.com/message?token=…", Required: true, Wide: true},
		},
		Validate: validateWebhookURL, // may also fill defaults: it gets a *Config
		Reminders: true, // a channel people read: reminders while down, and acknowledgements
		Send: func(ctx context.Context, s *Sender, c Config, a Alert) error {
			return s.PostJSON(ctx, c.URL, map[string]any{"title": a.Title(), "message": a.Text(), "priority": 8})
		},
	})
}
```

Settings live in the shared `Config` struct (`internal/notify/notify.go`); add a field if you need a new one. Settings that belong to other channels are dropped automatically. Mark anything secret (tokens, webhook URLs) as `schema.Password`: it's masked in the form and never sent to viewers.

`a.Title()` is the alert in a few words ("Checkout API is down", "Checkout API is still down (for 45m)") and `a.Text()` a full line; `a.Test` is set for "Send test", and `a.Type` for reminders and acknowledgements. Return the error the service gave: retries are handled for you. Errors from `PostJSON` say whether retrying can help (a 404 or 401 won't); for other protocols, wrap a refusal with `notify.Permanent` so it isn't retried. Leave `Reminders` off for paging and incident tools that track outages themselves. Add a test in `internal/notify/`, against an `httptest` server (see `channels_test.go`). If Uptime Kuma has the same channel, map its settings in `internal/kuma/convert.go` too.

## Pull requests

- Keep a PR to one change, with tests. `make lint` and `make test` should pass; CI runs both, plus the database tests.
- Describe what changes for people using the agent, not only the code.
- UI changes: include a screenshot, and check both light and dark mode and a phone-width screen.
- Match the code around you: comments explain *why*, errors are sentences a user can act on, and nothing logs or returns a secret.
- By contributing you agree your work is licensed under the [Apache 2.0 license](LICENSE).

Found a security issue? Please don't open an issue; see [SECURITY.md](SECURITY.md).
