<p align="center">
  <a href="https://www.upti.my/?utm_source=github&utm_medium=readme&utm_campaign=agent">
    <picture>
      <source media="(prefers-color-scheme: dark)" srcset="https://www.upti.my/brand/uptimy-logo-white.svg">
      <img src="https://www.upti.my/brand/uptimy-logo.svg" alt="Uptimy" height="48">
    </picture>
  </a>
</p>

<h3 align="center">Uptimy Agent</h3>

<p align="center">
  Uptime monitoring that runs inside your network.<br>
  Check private services, databases and cron jobs. Get alerted anywhere. Publish a status page.
</p>

<p align="center">
  <a href="https://github.com/uptimy/agent/releases/latest"><img src="https://img.shields.io/github/v/release/uptimy/agent" alt="Release"></a>
  <a href="https://artifacthub.io/packages/search?repo=uptimy-agent"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/uptimy-agent" alt="Artifact Hub"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/uptimy/agent" alt="License"></a>
</p>

<p align="center">
  <a href="https://uptimy-agent-production-b9a3.up.railway.app/status">Live demo</a> ·
  <a href="#install">Install</a> ·
  <a href="docs">Docs</a>
</p>

---

External monitors only see what's on the internet. The agent runs next to your services, so it checks what they can't:

```yaml
healthchecks:
  - name: API
    type: http
    target: http://api.railway.internal:8080/health   # private networking
  - name: Database
    type: postgres
    target: ${DATABASE_URL}                           # logs in and runs a query
  - name: Checkout
    type: kubernetes
    target: shop/deployment/checkout                  # all replicas ready
heartbeats:
  - name: Nightly backup
    cron: "0 3 * * *"                                 # alerts when a run is missed or fails
```

One small Go binary with its own UI and database. Free, open source, no account needed.

## Install

### Docker

```bash
docker run -d --name uptimy-agent -p 8080:8080 -v uptimy-agent:/data ghcr.io/uptimy/agent
```

Open <http://localhost:8080> and sign in as `admin` with the password from `docker logs uptimy-agent`.

### Kubernetes

```bash
helm install uptimy-agent oci://ghcr.io/uptimy/charts/uptimy-agent -n monitoring --create-namespace
```

### Railway

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/uptimy-agent?referralCode=-G2iM8&utm_medium=integration&utm_source=template&utm_campaign=agent-readme)

Or with [Docker Compose](docker-compose.yml).

## What it does

- **Healthchecks:** HTTP, Postgres, MySQL, Redis, TCP, ping, DNS, TLS expiry and Kubernetes workloads
- **Heartbeats** for cron jobs, backups and workers: missed runs, failures and how long each run took
- **Alerts** by email, Slack, Teams, Discord, Telegram, ntfy, PagerDuty, webhooks and [30+ more](docs/alerts.md#more-services-shoutrrr), retried until delivered
- **Status page** on your own domain, with incidents, maintenance windows and badges
- **Monitors as code** in YAML, or from Kubernetes labels, alongside the ones you click together
- **Light:** a 9 MB image that idles at 8 MiB of memory ([benchmark](bench/uptime-kuma/README.md))

## Kubernetes: label it and it's monitored

```yaml
metadata:
  labels:
    upti.my/monitor: "true"
```

Services, Ingresses, HTTPRoutes and workloads get a check within 30 seconds. CronJobs get a heartbeat that reads their Jobs, so they need no ping. See [Kubernetes](docs/kubernetes.md).

## Railway: monitor the whole project

Deploy the template into your project, then **Settings → Import from Railway**: every service gets a check over private networking, databases included, and cron services become heartbeats. See [Importing](docs/importing.md#from-railway).

## Coming from Uptime Kuma

Upload `kuma.db` under **Settings → Import from Uptime Kuma**. Monitors, notifications and status page groups come over, and you review everything first. See [Importing](docs/importing.md#from-uptime-kuma).

## Uptimy Cloud (optional)

A monitor can't tell you it's down itself. Connect the agent to a free [Uptimy](https://www.upti.my/?utm_source=github&utm_medium=readme&utm_campaign=agent-heartbeat) account and Uptimy alerts you from outside when the agent stops checking in. Your monitors also show up there for status pages, SLA reports and on-call. The agent keeps working on its own either way. See [Uptimy Cloud](docs/uptimy-cloud.md).

## Docs

- [Configuration](docs/configuration.md): environment variables, monitors in YAML, users and two-factor sign-in
- [Monitors](docs/monitors.md): database checks, heartbeats and ping URLs, the agent's User-Agent
- [Kubernetes](docs/kubernetes.md): the Helm chart, auto-discovery and annotations
- [Alerts](docs/alerts.md): routing, retries, reminders and the webhook format
- [Status page](docs/status-page.md): maintenance, incidents and badges
- [API and metrics](docs/api.md): API tokens and Prometheus
- [Importing](docs/importing.md): from Uptime Kuma and Railway
- [Uptimy Cloud](docs/uptimy-cloud.md)
- [Railway template](deploy/railway.md)

## Contributing

```bash
make run    # http://localhost:8080, sign in as admin / uptimy-dev
make test
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [ARCHITECTURE.md](ARCHITECTURE.md). Report security issues privately ([SECURITY.md](SECURITY.md)).

## License

[Apache-2.0](LICENSE)
