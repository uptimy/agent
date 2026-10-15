# Railway template

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/uptimy-agent?referralCode=-G2iM8&utm_medium=integration&utm_source=template&utm_campaign=agent-railway-docs)

Uptimy Agent is published as a one-click Railway template. This page holds its
settings, for maintaining the template or setting the agent up by hand. Live
demo: https://uptimy-agent-production-b9a3.up.railway.app/status

## Service

| Setting | Value |
| --- | --- |
| Source | `ghcr.io/uptimy/agent:latest` (or this repo, which builds from `railway.json`) |
| Volume | Mount path `/data` |
| Public networking | Generate a domain (serves the UI and `/status`) |
| Healthcheck | `/healthz` (set in `railway.json`) |
| Replicas | 1. SQLite has a single writer |

## Variables

| Variable | Template value | Why |
| --- | --- | --- |
| `ADMIN_PASSWORD` | `${{secret(24)}}` | Password for the `admin` user, generated per deploy and visible in the service's Variables tab. More users can be added in the UI |
| `RAILWAY_RUN_UID` | `0` | Railway volumes are mounted as root; the image runs as a non-root user by default |
| `MONITORS_YAML` | *(empty, optional)* | Declarative monitors |

Railway's `PORT` is picked up automatically.

## Monitoring other services in the project

Services in the same project can reach each other over private networking at `<service>.railway.internal`. Use reference variables in `MONITORS_YAML` so targets follow renames:

```yaml
healthchecks:
  - name: API
    type: http
    target: http://${{api.RAILWAY_PRIVATE_DOMAIN}}:${{api.PORT}}/health
  - name: Postgres
    type: postgres
    target: ${PG_URL}
  - name: Redis
    type: redis
    target: ${REDIS_URL}
```

For database checks, add the connection URLs as variables on the agent service
(`PG_URL` = `${{Postgres.DATABASE_URL}}`, `REDIS_URL` = `${{Redis.REDIS_URL}}`,
`MYSQL_URL` = `${{MySQL.MYSQL_URL}}`) and reference them as `${PG_URL}` in
monitors, in YAML or in the UI. The agent reads them when a check runs, so the
passwords are never stored in its database. Those URLs use the private network.

## Template description (suggested)

> Self-hosted uptime monitoring and status page for your Railway project. Monitors public URLs and private `*.railway.internal` services, Postgres, MySQL and Redis, cron jobs and scheduled tasks via heartbeat URLs, and TLS certificates. Alerts to Slack, Discord, Telegram or webhooks. A single lightweight Go binary.
