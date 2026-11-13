# Railway template

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/uptimy-agent?referralCode=-G2iM8&utm_medium=integration&utm_source=template&utm_campaign=agent-railway-docs)

Uptimy Agent is published as a one-click Railway template. This page holds its
settings, for maintaining the template or setting the agent up by hand. Live
demo: https://uptimy-agent-production-b9a3.up.railway.app/status

To set it up step by step, from deploying to importing a project's services, see
[Deploy Uptimy Agent on Railway](https://www.upti.my/docs/guides/deploy-on-railway?utm_source=github&utm_medium=readme&utm_campaign=agent-railway-docs).

## Service

| Setting | Value |
| --- | --- |
| Source | `ghcr.io/uptimy/agent:latest`, with patch auto-updates at any time (or this repo, which builds from `railway.json`) |
| Volume | Mount path `/data`, daily backups |
| Public networking | A generated domain on port 8080 (serves the UI and `/status`) |
| Healthcheck | `/healthz` (set in `railway.json`) |
| Replicas | 1. SQLite has a single writer |

## Variables

| Variable | Template value | Why |
| --- | --- | --- |
| `ADMIN_PASSWORD` | `${{secret(24)}}` | Required. Password for the admin user, generated once when the template is deployed and visible in the service's Variables tab. While it's set, the password is changed here, not in the UI; redeploying with a new value resets it. More users can be added in the UI |
| `RAILWAY_RUN_UID` | `0` | Required. Railway volumes are mounted as root; the image runs as a non-root user by default |
| `ADMIN_USERNAME` | `admin` | Optional. The admin's username |

Not in the template, added as needed: `MONITORS_YAML` (monitors as code, below),
database URLs (below), `AGENT_NAME` (the agent's label in Uptimy Cloud; the
hostname is a container ID on Railway). See [Configuration](../docs/configuration.md)
for the rest.

Railway's `PORT` is picked up automatically.

## Monitoring other services in the project

After deploying, **Settings → Import from Railway** creates a monitor for each service in the project from a project token, using the private addresses below.

Services in the same project can reach each other over private networking at `<service>.railway.internal`. Use reference variables in `MONITORS_YAML` so targets follow the service's private hostname:

```yaml
healthchecks:
  - name: API
    type: http
    target: http://${{api.RAILWAY_PRIVATE_DOMAIN}}:8080/health   # the port api listens on
  - name: Postgres
    type: postgres
    target: ${POSTGRES_URL}
  - name: Redis
    type: redis
    target: ${REDIS_URL}
```

For database checks, add the connection URLs as variables on the agent service
(`POSTGRES_URL` = `${{Postgres.DATABASE_URL}}`, `REDIS_URL` = `${{Redis.REDIS_URL}}`,
`MYSQL_URL` = `${{MySQL.MYSQL_URL}}`) and reference them as `${POSTGRES_URL}` in
monitors, in YAML or in the UI. These are the names Import from Railway asks
for: the service's name, plus `_URL`. The agent reads them when a check runs, so
the passwords are never stored in its database. Those URLs use the private network.

## Template description (suggested)

> Self-hosted uptime monitoring and status page for your Railway project. Monitors public URLs and private `*.railway.internal` services, Postgres, MySQL and Redis, cron jobs and scheduled tasks via heartbeat URLs, and TLS certificates. Alerts to Slack, Discord, Telegram or webhooks. A single lightweight Go binary.
