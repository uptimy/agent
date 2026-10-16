# Architecture

Uptimy Agent is one Go binary with an embedded React UI and a SQLite database. It's meant to run next to what it watches (in a Kubernetes cluster, a Railway project, a private network), so it has few dependencies and no required external services.

```mermaid
flowchart LR
    subgraph Agent["uptimy-agent (one process)"]
        API["api<br/>JSON API · sign-in · SSE"]
        UI["web<br/>React UI (go:embed)"]
        Sched["scheduler<br/>one runner per monitor"]
        Checks["checks<br/>probes"]
        Notify["notify<br/>channels"]
        Store[("store<br/>SQLite")]
        Hub["events<br/>live update hub"]
        Files["filesync<br/>YAML monitors"]
        Discovery["discovery<br/>labeled k8s resources"]
        Sync["cloudsync<br/>Uptimy Cloud sync"]
    end

    Browser((Browser)) --> API
    API --> UI
    API --> Store
    API --> Sched
    Files --> Store
    Discovery --> Store
    Discovery --> Sched
    Sched --> Checks
    Checks --> Targets[(Websites · APIs · DBs<br/>DNS · TLS · Kubernetes)]
    CronJobs[(Cron jobs · workers)] -- /ping --> API
    Sched --> Store
    Sched --> Hub
    Hub -- SSE --> Browser
    Sched --> Notify
    Notify --> Channels[(Slack · Discord<br/>Telegram · webhooks)]
    Store --> Sync
    Sync -- mirrors + results --> Uptimy[(Uptimy)]
```

## Two kinds of monitor

A **monitor** is a healthcheck or a heartbeat (`internal/monitor`). Both have a name, a status (up, down, pending, paused), a place on the status page, events and alerts. What they track differs, so each kind has its own settings, history and pages:

| | Healthcheck | Heartbeat |
| --- | --- | --- |
| Who acts | the agent probes a target | a job pings the agent |
| Settings | `monitor.Check`: type, target, interval, timeout, threshold | `monitor.Heartbeat`: interval or cron + time zone, grace period, ping token |
| History | `results`: one row per probe, with latency | `runs`: one row per run, with its due time, start, finish, outcome and message |
| Measure | uptime and response time | on-time rate, missed and failed runs, duration |
| API | `/api/healthchecks` | `/api/heartbeats`, plus `/ping/<token>` for jobs |

In SQLite a `monitors` row holds what's shared, and a `healthchecks` or `heartbeats` row holds the rest.

## A healthcheck, end to end

1. On start, `cmd/uptimy-agent` opens the store, syncs monitors from the YAML file (`filesync`), and hands them all to the **scheduler**. Inside Kubernetes, `discovery` lists resources labeled `upti.my/monitor=true` every 30 seconds and hands what changed to the scheduler too. Both sync through `managed`: each source owns its monitors by name, and the API refuses to edit them. A discovered CronJob is a heartbeat whose runs `discovery/jobs.go` reads from its Jobs every 10 seconds and hands to the scheduler with `Scheduler.Report`, at the times Kubernetes recorded, instead of a ping.
2. The scheduler runs one goroutine per monitor. For a healthcheck it calls `checks.Run` on every interval, with the check's timeout.
3. `checks.Run` finds the check type's **probe** (registered by `internal/checks/<type>.go`) and returns an `Outcome`: OK, plus a message.
4. The scheduler stores the result and publishes it to the **events hub**, which streams it to open browsers over Server-Sent Events.
5. The healthcheck goes down after `failure_threshold` consecutive failures and up again after `recovery_threshold` consecutive successes. Each change is recorded as an **event**; going down, and recovering from down, also sends an **alert** to every enabled notification channel.

## A heartbeat, end to end

1. The scheduler computes when the next run is due from the schedule and the last run (or the last edit, so pausing or rescheduling never causes an instant miss). The deadline is the due time plus the grace period.
2. The job calls `/ping/<token>`, optionally `/start` first, `/fail` or `/<exit code>` instead. `internal/api/ping.go` hands the ping to the heartbeat's goroutine, so pings are applied in order.
3. A finish closes the open run (from `/start`, which gives its duration) or records a new one: on time if it arrived by the deadline, a success or failure depending on the ping.
4. If nothing arrives by the deadline, a **missed** run is recorded. A late ping that arrives before the next due time still counts, as a late run.
5. Missed and failed runs take the heartbeat down; the next successful run brings it back up. As with healthchecks, each change is an event, and going down or recovering sends an alert.

## Alerts

Alerts go through an outbox, so none is lost to a failing channel, a crash or a restart:

1. The scheduler records a status change and its alert in **one transaction** (`store.InsertEventAlert`): an `alerts` row with the alert as it will be sent, and an `alert_deliveries` row for each enabled channel that covers the monitor (those for every monitor, and those it's been added to). A change is never recorded without its alert.
2. The **dispatcher** (`internal/delivery`) sends due deliveries, each channel separately. Failures are retried with backoff for up to `delivery.MaxAge` (an hour); a refusal (`notify.IsPermanent`: a 4xx other than 408/429, or an SMTP 5xx) fails at once. Deliveries along one channel and monitor go strictly in order, so a recovery waits for its outage. A recovery is suppressed on a channel that was alerted about the outage but never got any of those alerts.
3. Each attempt's outcome is saved on the delivery and pushed to the UI. Pending deliveries are picked up again on the next start, so delivery is at least once; the alert's `id` lets receivers drop copies.

During a maintenance window covering the monitor, a down alert's deliveries are **held** (`scheduler/maintenance.go`). When the window ends, a monitor that's still down (by its newest event, so this works right after a restart) has its alert released as "still down after maintenance"; one that recovered had both its outage and recovery suppressed, since nobody heard it went down.

While a monitor stays down, channels with a repeat interval get **reminders** (`scheduler/reminders.go`), counted from the last alert each one got. **Acknowledging** the outage stores who's on it, stops the reminders and tells the channels. Any status change ends both, in the same transaction as the event. Reminders and acknowledgements are alerts of their own `type`, delivered like any other, and only to channels with `Reminders` (not paging or incident tools, which track outages themselves).

## Uptimy Cloud sync

`internal/cloudsync` runs for a "Connect to Uptimy Cloud" connection (design: `AGENT_CLOUD_SYNC_PLAN.md` in the uptimy repo). The agent stays the source of truth; Cloud gets a read-only copy.

1. **Set up** once per connection: register the agent's private location (`PUT /v1/api/agent/location`, keyed by the install id) and its `agent_liveness` check-in heartbeat at the plan's upload interval. Every upload is the check-in.
2. **Mirror** monitors: each cycle sends the ones whose definition changed (`cloud_monitors.hash`) and deletes mirrors of deleted ones; every 10 minutes it also compares with Cloud's own lists. Healthchecks go to upti.my-api, heartbeats to upti.my-heartbeat with `source: agent`.
3. **Upload** to `ingest.upti.my`: results after `results.rowid` and finished runs after `runs.id` (never past one still running), each with the status the agent had then (from `events`: down, or up/degraded by the result), thinned to one result per monitor per interval plus every status change. A batch's id comes from its cursors, so a retry is deduplicated; cursors move only once Cloud accepts it. 429 waits for `Retry-After`, 409 sets the location up again, and a plan limit keeps the cursor until the agent fits the plan.

A cycle runs every upload interval and a few seconds after a monitor or its status changes (`events` hub).

## Extension points

Check types and notification channels are registries, so a new one is self-contained (and if Uptime Kuma has it too, `internal/kuma/convert.go` maps it for importing):

| To add | Describe it in | Implement it in |
| --- | --- | --- |
| A check type | `internal/monitor/check_<type>.go` (`monitor.RegisterCheckType`): label, target, settings, validation | `internal/checks/<type>.go` (`register`): the probe |
| A notification channel | `internal/notify/<channel>.go` (`notify.Register`): label, settings, validation, and how to send | the same file |

Settings are described with `internal/schema.Field`. The API serves those descriptions (`/api/check-types`, `/api/notifier-types`) and the UI renders its forms from them, so new types need no frontend work. Tests check that every type has a probe, an example in `examples/monitors.yaml`, and a complete description. See [CONTRIBUTING.md](CONTRIBUTING.md) for a walk-through.

## Storage

SQLite through `modernc.org/sqlite` (pure Go, so the binary builds without cgo), with one connection in WAL mode: writes are serialized and reads never block them. Migrations are numbered SQL in `internal/store/store.go`, applied in order on start and recorded in `PRAGMA user_version`; an agent refuses to open a database written by a newer version.

### Where data lives

| What | Where | Model |
| --- | --- | --- |
| Users, sessions | `users`, `sessions` | `store.User` |
| Alert channels | `notifiers` | `notify.Notifier` |
| Healthchecks, heartbeats | `monitors` + `healthchecks` / `heartbeats` | `monitor.Monitor` |
| History | `results`, `runs`, `events` | `monitor.Result`, `monitor.Run`, `monitor.Event` |
| Alert routing | `notifiers.all_monitors` + `notifier_monitors` | `notify.Notifier` (`AllMonitors`, `MonitorIDs`) |
| Alerts and their delivery to each channel (the outbox) | `alerts` + `alert_deliveries` | `notify.Alert`, `notify.Delivery` |
| Who's on an outage | `acknowledgements` | `monitor.Acknowledgement` |
| Maintenance windows | `maintenance` + `maintenance_monitors` | `maintenance.Window` |
| Incidents | `incidents` + `incident_updates` + `incident_monitors` | `incident.Incident` |
| The status page's announcement | `settings` (JSON, apart from the page's settings) | `statuspage.Announcement` |
| API tokens (hashed) | `api_tokens` | `store.APIToken` |
| Status page settings and logos | `settings` (JSON) | `statuspage.Settings`, `statuspage.Logo` |
| Which monitors the status page shows, and how | columns on `monitors`, written only by the status page editor | `monitor.Monitor` (`Public`, `Status*`) |
| The Uptimy connection | `settings` (JSON) | `connect.Connection` |
| Monitors mirrored to Uptimy Cloud, and the upload cursors | `cloud_monitors`, `settings` | `store.CloudMonitor` |

Domain packages own the types and `internal/store` persists them; users, which only the store and sign-in use, are the exception. Settings rows are read and written only through typed methods (`StatusPage`, `SaveStatusPage`, `UptimyConnection`, ...), and their keys never leave the store. Changes that belong together (the page's settings and its layout, a connection and the settings saved with it) are saved in one transaction.

Environment variables don't hold data. They set how the process runs (`PORT`, `DATA_DIR`, `RETENTION_DAYS`, ...) and two things that suit config better than a database:

- `ADMIN_PASSWORD` creates the first admin and resets that admin's password on every start (the recovery path). The UI won't change a password the environment sets.
- `MONITORS_FILE` / `MONITORS_YAML` declare monitors. They're synced into the database on start, matched by name, and read-only in the UI. A file that doesn't parse is held back whole and invalid monitors are skipped (`filesync.Apply`); the problem goes to the log and `/api/info`, never stops the agent.

## The UI

`web/` is React 19, Vite, Tailwind CSS and TanStack Query. `vite build` writes `web/dist`, which Go embeds; in production the agent serves the API and the UI on one port. In development (`make run`), the agent proxies the UI to a Vite dev server for hot reload, so you still use a single URL.

Live data comes from the SSE stream. Healthcheck results, which arrive constantly, are written straight into the query cache (`web/src/lib/liveCache.ts`), so a check updates the page without refetching. Heartbeat changes are rare, so they refetch what's on screen.

## Security model

- **Users** are admins or viewers. Every change needs an admin. Changes must be JSON (or, for imports, a raw file upload): types a cross-site form can't send without a CORS preflight, which the agent never grants. That blocks cross-site form posts.
- **API tokens** (`upa_…`) act as their user, read-only if chosen or if the user is a viewer. Only a SHA-256 hash is stored. Tokens can't manage accounts, users or tokens, so a leaked one can't lock you out or create more.
- **Credentials never reach viewers**: webhook URLs, auth headers, heartbeat ping tokens, database passwords and bot tokens are removed from anything a viewer can load, and masked wherever a target is shown (lists, alerts).
- **The public status page** shows names with uptime or on-time rate only, never targets, schedules or internal hostnames.
- **Ping URLs** are the only credential a job needs, so they're random (144 bits) and can be replaced from the heartbeat's page. A ping can only record a run for its own heartbeat.
- **Database targets** can read passwords from environment variables (`${VAR}`) so they aren't stored, but never the agent's own settings (`ADMIN_PASSWORD`, `UPTIMY_*`). Custom queries run read-only.
- **The Uptimy connection** uses an agent-scoped key that can only manage the agent's own location, its mirrored monitors and its check-in; it's kept on the server and revoked on disconnect. Mirrored targets have passwords and secret query parameters masked.
- **Errors** are scrubbed of secrets before they're shown or logged (URLs with tokens, connection strings).

To report a vulnerability, see [SECURITY.md](SECURITY.md).
