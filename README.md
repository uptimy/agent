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
  Self-hosted uptime monitoring with a status page, built to run <b>inside</b> your cluster or project.<br>
  One small Go binary. Web UI included. Apache-2.0.
</p>

<p align="center">
  <a href="https://artifacthub.io/packages/search?repo=uptimy-agent"><img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/uptimy-agent" alt="Artifact Hub"></a>
</p>

---

Uptimy Agent watches your websites, APIs, databases, DNS, TLS certificates, cron jobs and Kubernetes workloads, alerts you by email, Slack, Microsoft Teams, Discord, Telegram, ntfy, PagerDuty, webhooks or any of 30+ more services, and publishes a public status page.

Because it runs next to your services, it can check things that external monitors can't reach: `postgres.default.svc:5432`, `api.railway.internal`, a Deployment's ready replicas.

- **Healthchecks:** HTTP(S) with status and keyword assertions, Postgres, MySQL and Redis (real logins and queries, not just an open port), ping, TCP, DNS, TLS expiry and Kubernetes workloads
- **Heartbeats:** cron jobs, backups and workers ping a URL when they run, or, for a Kubernetes CronJob, nothing at all: the agent reads its Jobs. Schedules are an interval or a cron expression in any time zone; you see on-time rate, missed and failed runs, exit codes and how long each run took, and you're alerted when a run is missed or fails
- **Dashboard:** both at a glance, split into Healthchecks and Heartbeats like in Uptimy
- **Alerts:** email, Slack, Microsoft Teams, Discord, Telegram, ntfy, PagerDuty, webhooks, Uptimy (alerts become incidents there, which can run workflows), and 30+ more services through one [Shoutrrr](https://shoutrrr.nickfedor.com/latest/services/overview/) URL (Pushover, Gotify, Matrix, Google Chat, Mattermost, Opsgenie, Signal, ...), fired on down and on recovery, with a consecutive-failure threshold to avoid flapping. Each channel alerts for every monitor or only the ones you choose
- **Maintenance windows:** planned work doesn't page anyone, and is announced on the status page. A monitor that's still down when the window ends alerts then
- **Status page:** public, at `/status` or on its own domain (`status.example.com`), which serves only the status page so sign-in and the dashboard stay private. Admins set a logo (with an optional dark-mode version), accent color and website link, create sections and drag monitors into order with public names, with a live preview, much like the Uptimy app. New monitors stay off the page until you add them. It shows names with uptime (healthchecks) or on-time runs (heartbeats) only, never internal hostnames
- **Incidents and an announcement:** tell visitors what's going on with an incident (severity, affected monitors, and a timeline from investigating to resolved), posted under **Incidents**, or with the status page's announcement for news like a migration
- **Badges:** SVG badges for READMEs with the page's overall status, or a monitor's status or uptime over 24 hours, 7, 30 or 90 days; copy them from the status page editor
- **GitOps-friendly:** define monitors in YAML (a file, a ConfigMap or an env var), or click them together in the UI. Script everything else with [API tokens](#api)
- **Switching from Uptime Kuma:** import its monitors and notifications in a few clicks; see [below](#switching-from-uptime-kuma)
- **Light:** one static Go binary with an embedded SQLite database (pure Go, no CGO) in a distroless image: a 9 MB download. It uses 8 MiB of memory idle and 25 MiB with 350 monitors, a fraction of Uptime Kuma's 125–160 MiB ([benchmark](bench/uptime-kuma/README.md))

## Quick start

On Railway, it's one click (see [Railway](#railway) below for what it sets up):

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/uptimy-agent?referralCode=-G2iM8&utm_medium=integration&utm_source=template&utm_campaign=agent-readme)

### Docker

```bash
docker run -d --name uptimy-agent -p 8080:8080 -v uptimy-agent:/data ghcr.io/uptimy/agent
```

Open http://localhost:8080 and sign in as `admin` with the password printed in the logs (`docker logs uptimy-agent`). You'll choose your own password straight away. Or pass `-e ADMIN_PASSWORD=…` to set it up front.

### Docker Compose

See [`docker-compose.yml`](docker-compose.yml).

### Kubernetes (Helm)

```bash
helm install uptimy-agent oci://ghcr.io/uptimy/charts/uptimy-agent -n monitoring --create-namespace
kubectl -n monitoring port-forward svc/uptimy-agent 8080:80
```

The chart is released with the agent, at the same version, and signed with cosign. Its values are documented in the [chart README](deploy/helm/uptimy-agent/README.md).

Inside a cluster the agent uses its service account to check Deployments, StatefulSets, DaemonSets and Services. The chart creates a read-only role for that. Monitors can live in `values.yaml`:

```yaml
healthchecks:
  - name: Checkout API
    type: http
    target: http://checkout.shop.svc.cluster.local:8080/health
    interval: 30s
  - name: Checkout deployment
    type: kubernetes
    target: shop/deployment/checkout
heartbeats:
  - name: Nightly backup
    cron: "0 3 * * *"
    timezone: Europe/Berlin
```

#### Auto-discovery

Label a Service, Ingress, Gateway API HTTPRoute, Deployment, StatefulSet or DaemonSet with `upti.my/monitor: "true"` and the agent creates a monitor for it within 30 seconds, so a new app is monitored as soon as it's deployed:

| Resource | Monitor | Default name |
| --- | --- | --- |
| Service | HTTP check on `http://<name>.<namespace>.svc:<port>` with the path (and scheme) of its pods' readinessProbe, when they have one on that port, otherwise `/`. Ports with no HTTP readinessProbe that don't look like HTTP (named `http`, `https`, `web` or `http-…`, an HTTP `appProtocol`, or port 80, 443 or 8080) get a TCP connect | `<namespace>/<name>` |
| Ingress | HTTP check per hostname, over HTTPS when the Ingress has TLS for it | the hostname |
| HTTPRoute | HTTPS check per hostname | the hostname |
| Deployment, StatefulSet, DaemonSet | readiness check: all replicas ready | `<namespace>/<name> (<kind>)` |
| CronJob | heartbeat on its schedule and time zone, with runs read from its Jobs (see below) | `<namespace>/<name> (cronjob)` |

```yaml
apiVersion: v1
kind: Service
metadata:
  name: checkout
  namespace: shop
  labels:
    upti.my/monitor: "true"
  annotations:
    upti.my/name: Checkout API
    upti.my/path: /health
```

By default the agent checks a Service with its own request, end to end through DNS, the Service and the app, as described above. With `upti.my/type: kubernetes` it reuses the kubelet's readinessProbes instead: the Service is up while it has a ready endpoint, and the agent sends the app no traffic. That works on any port and protocol, but only knows what the readinessProbe tests. You can also add one by hand: a Kubernetes healthcheck on `<namespace>/service/<name>`.

Annotations adjust what's checked:

| Annotation | Applies to | Default |
| --- | --- | --- |
| `upti.my/name` | all | see above |
| `upti.my/path` | Service, Ingress, HTTPRoute | a Service's readinessProbe path, else `/`. On a Service it also makes the check HTTP |
| `upti.my/port` | Service | the first port (a name or a number) |
| `upti.my/type` | Service | `http`, `tcp` or `kubernetes`; picked as above |
| `upti.my/scheme` | Service, Ingress, HTTPRoute | `http` or `https`, picked as above |
| `upti.my/interval` | all but CronJobs | `1m` (e.g. `30s`) |
| `upti.my/grace` | CronJob | the Job's `activeDeadlineSeconds` plus a minute, or else the time between runs, at most `1h` |
| `upti.my/expected-status` | HTTP checks | `200-399` |
| `upti.my/keyword` | HTTP checks | – |

**CronJobs need no ping.** The agent reads each discovered CronJob's Jobs every 10 seconds and records every run at the times Kubernetes saw: when the Job started, when it completed or failed, and for a failure the reason with the container's exit code (`BackoffLimitExceeded; container backup exited with code 137 (OOMKilled)`). Missed runs come from the schedule as for any heartbeat. A run must finish within the grace period after it's due, so the default grace leaves room for the job itself. Suspending the CronJob pauses its heartbeat, and resuming it resumes it.

Discovered monitors are read-only in the UI, marked "Discovered in Kubernetes" with the object they came from, and follow the resource: changing an annotation updates the monitor (renaming it with `upti.my/name` keeps its history), and removing the label or the resource deletes it. `true`, `yes`, `1` and `on` opt a resource in; `false`, `no`, `0` and `off` opt it out explicitly; any other value is reported.

**Settings → Kubernetes** shows how discovery is doing (last scan, scope, how many monitors) and anything it couldn't use, like a mistyped label value or an invalid annotation, so a resource that doesn't show up says why. It also lists the cluster's Services, Ingresses, HTTPRoutes, workloads and CronJobs, monitored or not, with the `kubectl label` command for each one or for everything shown. The agent itself never changes the cluster: labeling stays with you and your manifests. You can still pause them and add them to the status page. Catch-all and wildcard hostnames are skipped. With `rbac.clusterWide: false` the agent only looks in its own namespace; set `discovery.enabled: false` in the chart (or `KUBERNETES_DISCOVERY=false`) to turn it off.

### Railway

[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/deploy/uptimy-agent?referralCode=-G2iM8&utm_medium=integration&utm_source=template&utm_campaign=agent-readme)

The template adds the agent to your project with a volume at `/data` and a generated `ADMIN_PASSWORD` (in the service's Variables tab). Services in the same project are reachable over private networking, so you can monitor `*.railway.internal` hosts that aren't exposed publicly, and your databases through reference variables. See it running in the [live demo](https://uptimy-agent-production-b9a3.up.railway.app/status), and [deploy/railway.md](deploy/railway.md) for the template settings and monitor examples. You can also deploy from this repo, which includes [`railway.json`](railway.json).

## Configuration

Environment variables set how the agent runs, its secrets, and (optionally) monitors as code. Everything else, including the status page, alert channels and Uptimy alerting, is set in the UI and kept in the agent's database.

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP port |
| `DATA_DIR` | `./data` (`/data` in the image) | Where the SQLite database lives |
| `ADMIN_USERNAME` | `admin` | Username of the default admin created on first start |
| `ADMIN_PASSWORD` | – | That admin's password (min 8 chars). If unset, a random one is printed in the log on first start and must be changed at first sign-in. Setting it later resets the password, which is also how you recover a lost admin login |
| `MONITORS_FILE` | – | Path to a YAML file of monitors |
| `MONITORS_YAML` | – | The YAML itself, for platforms where mounting files is awkward |
| `KUBERNETES_DISCOVERY` | `true` | Create monitors for resources labeled `upti.my/monitor: "true"` (see [Auto-discovery](#auto-discovery)). Only applies inside a cluster |
| `RETENTION_DAYS` | `30` | How long check results, heartbeat runs and events are kept |
| `UPTIMY_HEARTBEAT_URL` | – | Optional. Pins the [Watch the watcher](#watch-the-watcher) heartbeat, for agents without a persistent volume; otherwise connect it in the UI |
| `AGENT_NAME` | hostname | How this agent is labeled in Uptimy |

### Monitors in YAML

The file has two lists, `healthchecks` and `heartbeats`; see [`examples/monitors.yaml`](examples/monitors.yaml) for every check type and both kinds of schedule. Monitors from YAML are matched by name: changing one keeps its history, and removing one deletes it on the next start. They're shown read-only in the UI, though you can still pause them during maintenance and add them to the status page. Monitors created in the UI are never touched by the file. Unknown settings are an error, so a typo doesn't go unnoticed.

### Users and roles

The agent starts with one admin account (see `ADMIN_USERNAME` / `ADMIN_PASSWORD`). Admins can add people under **Users**:

- **Admin:** can change everything, including users
- **Viewer:** can see monitors, history and settings, but not change them. Credentials such as webhook URLs, auth headers, database passwords and heartbeat ping URLs are hidden from viewers

New users get a temporary password to pass on and choose their own at first sign-in. Admins can reset a password or change a role at any time; the agent won't let you delete or demote yourself or remove the last admin.

**Two-factor sign-in:** anyone can turn it on under **Account → Two-factor sign-in**: scan the QR code with an authenticator app (1Password, Google Authenticator, Authy, ...), confirm a code, and save the 10 recovery codes, each good for one sign-in without the phone. Turning it on signs out your other devices. API tokens aren't affected. An admin can turn it off for another user under **Users**; if nobody who can is able to sign in, run the agent's binary against its data:

```bash
kubectl -n monitoring exec deploy/uptimy-agent -- uptimy-agent reset-2fa admin
docker exec uptimy-agent uptimy-agent reset-2fa admin
```

### Database monitors

Postgres, MySQL and Redis monitors open a fresh connection on every check, log in, run a query (`SELECT 1` by default; `PING` for Redis) and disconnect. They catch what a TCP check can't: rejected logins, a full connection limit, a server that accepts connections but can't answer.

| Type | Target |
| --- | --- |
| `postgres` | `postgres://user:password@host:5432/db?sslmode=require` |
| `mysql` | `mysql://user:password@host:3306/db?tls=true` (`tls`: `true`, `false`, `skip-verify` or `preferred`, the default) |
| `redis` | `redis://:password@host:6379/0`, or `rediss://` for TLS |

- **Keep passwords out of the agent:** write `${VAR}` for the whole URL or just the password, and the agent reads that environment variable when the check runs, e.g. from a Kubernetes Secret or a Railway reference variable. The agent's own settings (`ADMIN_PASSWORD`, `UPTIMY_*`, ...) can't be referenced.
- **Custom query:** for Postgres and MySQL, set `query` and optionally `expected`, compared with the first column of the first row. For example, `SELECT pg_is_in_recovery()` with `expected: "false"` alerts when the database stops being the primary.
- **Read-only:** queries run in a read-only session, one statement at a time, and connections show up as `uptimy-agent`. That's a guard against mistakes, not a permission system: create a dedicated read-only user for monitoring.
- Passwords are masked everywhere the target is shown (lists, alerts) and hidden from viewers. The status page never shows targets.
- Postgres checks work through PgBouncer in transaction mode.

### Heartbeats

Healthchecks ask "is it up?". Heartbeats ask "did the job run, on time, and did it succeed?", for cron jobs, backups, queue workers and anything else that runs on a schedule. Create one under **Heartbeats** and have the job call its ping URL:

```bash
0 3 * * *  /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 3 https://agent.example.com/ping/<token>
```

| URL | Meaning |
| --- | --- |
| `/ping/<token>` | The job finished |
| `/ping/<token>/start` | The job started. Its finish then records how long it took |
| `/ping/<token>/fail` | The job failed |
| `/ping/<token>/<exit code>` | 0 is success; 1 to 255 is a failure, e.g. `curl …/ping/<token>/$?` |

GET and POST both work. A POST body (say, the end of the job's log) is kept with the run, up to 500 characters.

- **Schedule:** a fixed interval (`every: 6h`) or a cron expression with a time zone (`cron: "0 3 * * *"`, `timezone: Europe/Berlin`), so daylight saving time is handled.
- **Grace period:** how late a run may be before it counts as missed (5 minutes by default). You're alerted when a run is missed or a job reports a failure, and again when it's back on schedule.
- **History:** each run is kept with its result, how long it took and its message; the heartbeat's page shows the on-time rate over 24 hours, 7 days and 30 days.
- **Ping URLs** are generated and stay the same when you edit the heartbeat. Replace one from its page if it leaks. In YAML, set `token` to pin it (16 to 64 letters, digits, `-` or `_`); otherwise one is generated and kept.

### Ping

Ping monitors send three ICMP echo requests and are up if any reply comes back. The agent uses unprivileged ICMP sockets, so it needs no root or `NET_RAW`: Docker allows them by default, and the Helm chart sets the `net.ipv4.ping_group_range` sysctl. Elsewhere on Linux, run `sysctl -w net.ipv4.ping_group_range="0 2147483647"`.

### Alert routing

A channel alerts for every monitor (the default, including monitors added later) or only for the monitors you choose. Set it on the channel under **Notifications**, or tick channels under **Alerts** when editing a monitor.

### More services

For anything without its own channel, add a **More services (Shoutrrr)** channel with a [Shoutrrr URL](https://shoutrrr.nickfedor.com/latest/services/overview/): `pushover://shoutrrr:<token>@<user>`, `gotify://<host>/<token>`, `matrix://<user>:<password>@<host>`, `googlechat://chat.googleapis.com/v1/spaces/...`, `opsgenie://api.opsgenie.com/<key>`, and 30+ more. The URL holds the service's credentials, so it's stored like a password and never shown in errors.

### Maintenance

Schedule maintenance under **Maintenance** before deploys, upgrades or migrations: for all monitors or some, starting now or later. During the window the monitors keep being checked, but nobody is alerted. If one is still down when the window ends, its alert goes out then; if it recovered, nothing is sent. Public windows are announced on the status page up to a week ahead and stay listed as completed for a week after, and the affected monitors are marked as under maintenance rather than down.

### Incidents

Under **Incidents**, report an incident when something's wrong that visitors should hear about: a title, a severity (low, medium, high or critical), the monitors it affects, and a first update. Post updates as it moves from investigating to identified, monitoring and resolved; the status page shows the whole timeline, and resolved incidents stay listed for 14 days. While one is open, the page's headline says "Some Issues Detected", or "Outage Detected" for a critical one. Updates can be corrected later, and deleting a resolution reopens the incident. Posting doesn't alert anyone; monitors do that.

For news that isn't an incident, like a migration or a new region, post the status page's **announcement** at the top of **Status page**: a title and a message shown under the page's status, until you remove it or until a time you choose. Editing it keeps when it was posted.

### Badges

Badges are served while the status page is on, and only for monitors on it:

| Badge | URL |
|---|---|
| The page's overall status | `/badge/status.svg` |
| A monitor's status | `/badge/<id>/status.svg` |
| Its uptime, or a heartbeat's on-time rate | `/badge/<id>/uptime.svg?period=24h` (`7d`, `30d` (default), `90d`) |

`?label=` replaces the left-hand text. The status page editor lists them with Markdown to copy, linking to the page.

## API

For optional read-only AI investigation over Streamable HTTP, see [MCP support](MCP.md).

Everything in the UI goes through a JSON API under `/api`. For scripts and CI, create a token under **Account → API tokens** and send it as a bearer token:

```bash
curl -H "Authorization: Bearer upa_…" https://agent.example.com/api/healthchecks
```

A token acts as the user who made it. Choose read-only for dashboards and exporters; a viewer's tokens are always read-only. Tokens can't manage accounts, users or other tokens; that takes signing in.

### Prometheus metrics

`/metrics` serves Prometheus metrics, with an API token (read-only is enough):

```yaml
scrape_configs:
  - job_name: uptimy-agent
    authorization:
      credentials: upa_…
    static_configs:
      - targets: ["uptimy-agent.monitoring.svc:80"]
```

| Metric | What |
| --- | --- |
| `uptimy_agent_monitor_up` | 1 up, 0 down (not set while pending or paused) |
| `uptimy_agent_monitor_status` | 1 for the current status: `up`, `down`, `pending` or `paused` |
| `uptimy_agent_monitor_in_maintenance` | 1 while a maintenance window covers it |
| `uptimy_agent_checks_total` | Healthcheck probes by `result` (`success`, `failure`), counted since the agent started |
| `uptimy_agent_check_duration_seconds`, `uptimy_agent_check_last_timestamp_seconds` | The last probe's duration and time |
| `uptimy_agent_heartbeat_runs_total` | Heartbeat runs by `outcome` (`success`, `failure`, `missed`) |
| `uptimy_agent_heartbeat_next_due_timestamp_seconds`, `uptimy_agent_heartbeat_running` | When the next run is due, and whether one is running |
| `uptimy_agent_discovery_monitors`, `uptimy_agent_discovery_problems`, `uptimy_agent_discovery_last_scan_timestamp_seconds` | Kubernetes discovery |
| `uptimy_agent_info`, `uptimy_agent_go_*` | Version, goroutines and memory |

Monitors are labeled `id`, `name`, `kind`, `type` and `source`; targets aren't exported, since they can hold credentials. With the Prometheus Operator, the chart can create a ServiceMonitor (`metrics.serviceMonitor`).

## Switching from Uptime Kuma

Under **Settings → Import from Uptime Kuma**, upload Kuma's database (`kuma.db`, from its data folder: `/app/data` in Docker). Stop Kuma first so the copy is complete:

```bash
docker stop uptime-kuma && docker cp uptime-kuma:/app/data/kuma.db .
```

You'll see what each monitor and notification becomes, and choose what to import. HTTP, keyword, TCP port, ping, DNS, TLS, Postgres, MySQL, Redis and push monitors carry over, with their intervals, retries, accepted status codes, headers and basic or bearer auth. So do email, Slack, Teams, Discord, Telegram, ntfy, PagerDuty and webhook notifications, plus Pushover, Gotify, Matrix, Mattermost, Google Chat, Rocket.Chat and Opsgenie as "More services (Shoutrrr)" channels, along with which monitors alert where. Monitors that were on a Kuma status page are added to yours, with Kuma's groups as sections, in Kuma's order. Push monitors become heartbeats with new ping URLs, so update your jobs. Anything that can't be carried over (Docker, gRPC, inverted checks, ...) is listed with the reason. Kuma installs that use MariaDB aren't supported yet.

## Watch the watcher

A self-hosted monitor has one blind spot: itself. If the agent, its node, or the whole cluster goes down, nothing is left to tell you.

Open **Settings → Watch the watcher** and click **Connect to Uptimy**. Sign in to [Uptimy](https://www.upti.my/?utm_source=github&utm_medium=readme&utm_campaign=agent-heartbeat) (free), pick a workspace, and approve. The agent creates its own heartbeat and starts checking in every minute. If it stops, Uptimy alerts you from outside.

- The agent receives an **agent-scoped key**, which can only manage this agent's own heartbeat, not the rest of your workspace. It stays on the agent's server and is revoked when you click Disconnect, which also deletes the heartbeat.
- Reconnecting (say, after reinstalling) reuses the same heartbeat, so you keep its history and don't end up with orphaned heartbeats.
- **When you're alerted:** by default after 5 minutes of silence, enough to ride out a restart or redeploy. Choose 2, 5, 15 or 30 minutes in the same card. The setting lives on the heartbeat in Uptimy, so a change made there (or with `uptimyctl heartbeats update`) shows up here too.
- **Planned work:** **Allow downtime** for 30 minutes, 1 hour or 4 hours before working on the agent or its server. This doesn't pause the heartbeat: it pushes its alert deadline back, so the heartbeat stays active in Uptimy and still alerts if the agent isn't back when the window ends, and the agent restores normal alerting afterwards. **Pause until I resume** is there too, but nothing alerts until you resume.
- Set `AGENT_NAME` to label the agent in Uptimy; the default is the hostname, which in Kubernetes is the pod name.
- Prefer to set it up by hand? Choose **Or paste a heartbeat URL instead**, or set `UPTIMY_HEARTBEAT_URL`, which pins the connection read-only for GitOps setups.

## Development

You'll need Go 1.26+ and Node 22+.

```bash
make run    # http://localhost:8080, sign in as admin / uptimy-dev
make test   # Go and UI tests
make lint   # golangci-lint, ESLint and Prettier, as in CI
```

`make run` gives you the same single URL as production: the Go agent serves the API and the UI, proxying the UI to an internal Vite server so edits hot-reload, and saving a `.go` file rebuilds and restarts the agent.

Adding a monitor type or a notification channel is a small, self-contained change: see [CONTRIBUTING.md](CONTRIBUTING.md) for a walk-through and [ARCHITECTURE.md](ARCHITECTURE.md) for how the pieces fit together. Please report security issues privately, as described in [SECURITY.md](SECURITY.md).

## License

[Apache-2.0](LICENSE)
