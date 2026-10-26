# Changelog

Notable changes to Uptimy Agent. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Changed
- **The UI has one box level, like Uptimy Cloud.** Sections are headings on the page and only their content is framed: no card around a titled block, no box inside a box. The figures at the top of the dashboard and of a monitor's page are one strip instead of separate tiles, the monitor lists drop their extra frame, setting rows share their section's frame with hairlines between them, the status page editor uses line tabs that keep their labels on phones, and its sections are a heading with one list each.
- **The status page matches the hosted Uptimy status pages again:** the overall status is a coloured line instead of a tinted card (the loading placeholder too), the footer sits under a hairline, and the uptime legend is 12px.
- **Uptimy's style guide throughout.** Status words are Healthy, Down, Paused and Waiting (heartbeats: At risk when late), uptime always has two decimals (100.00%), response times read 278 ms and 1.8 s, and relative times are compact (5m ago, in 3h). Every tooltip is the one shared tooltip, like Cloud's, instead of the browser's slow native one. The healthcheck and heartbeat lists get status chips next to search. Daily bars are green from 99.5%, yellow from 99%, orange from 95% and red below, the same as the hosted Uptimy status pages and close to Atlassian Statuspage's colours, nothing is smaller than 12px, and the sidebar has no drop shadow.

### Fixed
- **An alert could be sent twice** when an attempt finished while the agent was already picking up the next due alerts: it was sent again from before that attempt, and its delivery then showed the wrong state and attempt count.

## [0.3.0] - 2026-10-20

### Added
- **History that's true past `RETENTION_DAYS`.** Each monitor's day is rolled up into one row (uptime or on-time rate, and response times p50/p95/p99/max) when it ends, and kept for `HISTORY_DAYS` (400 by default; Helm `historyDays`). Uptime over 7, 30 and 90 days, the status page's bars and the 90-day badge are now exact even when every check is kept for only 30 days: before, the 90d badge and anything older than `RETENTION_DAYS` quietly covered only the days still kept. On upgrade, every day still in the database is rolled up on the first start.
- **The status page shows 30, 60 or 90 days** (**Days of history** in the editor; 90 by default, so pages that showed 30 bars now show up to 90). Each monitor's uptime next to its bars covers the same days.
- **Response time over 30 and 90 days** on a healthcheck's page: the daily median with a band up to the 95th percentile. Uptime 90d next to 7d and 30d, and a heartbeat's on-time rate over 90 days.
- `GET /api/healthchecks/{id}/daily?days=90`: a healthcheck's days with uptime and response time percentiles.

### Changed
- **Checks during public maintenance don't count towards uptime** (or a heartbeat's on-time rate), wherever it's shown: the status page, badges, the monitor pages. Planned work announced on the status page no longer reads as downtime. Private windows still only silence alerts. Response times still include those checks.
- Uptime over 7, 30 and 90 days covers whole UTC days, today included, as the status page's bars do; 24 hours is still the last 24 hours.
- Raw results and runs of days that haven't been rolled up yet are never pruned, whatever their age.
- **The status page matches Uptimy's hosted status pages.** Each section is one frame of compact rows: a monitor shows its status, uptime and daily bars in one row (fewer on narrow screens), and only monitors that are down or in maintenance get a colored edge. Incidents and maintenance are rows too, with their timeline oldest first and a final **Resolved** step. Day tooltips are restyled, and a loading placeholder keeps the page's shape.
- The banner follows hosted pages' order: an open high incident is now an outage (before, only critical), and maintenance in progress now shows over a lower-severity incident.
- **Cron heartbeats keep their schedule in Uptimy Cloud.** Cloud shows the cron expression and time zone instead of an interval guessed from the next two runs, which was wrong for schedules like weekdays only.

### Upgrading from 0.2.x
- Back up `/data` first: rolling back to 0.2.x after upgrading isn't possible, because the database schema is newer.
- The first start rolls up every day still in the database, in the background; until it's done, those days are read as before.
- Status pages show up to 90 days of bars instead of 30, and each monitor's uptime covers those 90 days. Days before the agent had data are grey; set **Days of history** to 30 to keep the old look.
- `HISTORY_DAYS` must be at least 90 if you set it. `RETENTION_DAYS` keeps its meaning for every check and run; nothing is pruned that isn't rolled up.

## [0.2.0] - 2026-10-16

### Added
- **Connect to Uptimy Cloud.** Settings → Uptimy Cloud adds the agent to your Uptimy workspace as a private location. Its healthchecks and heartbeats show up there read-only, with their status, uptime and history, for status pages, SLA reports, alert rules, workflows and on-call; change them in the agent and Cloud follows within seconds. The agent still decides every status and alerts on its own; Cloud records what it decided. Each upload checks the agent in, so Uptimy alerts from outside when it goes quiet. Results queue up while Uptimy can't be reached. Passwords and secret query parameters (`password`, `token`, `api_key`, `signature`, ...) in targets are masked before anything is sent, in the UI's lists and alerts too. `UPTIMY_INGEST_URL` points elsewhere for development.
- **Alerts are retried when a channel is down.** Each alert is saved with the status change that caused it and sent to every channel on its own: a channel that's down or answers with an error is retried with backoff for up to an hour, while one that refuses it (a deleted webhook, a revoked token) isn't retried. Alerts about a monitor reach a channel in order, so a recovery never arrives before its outage. Alerts waiting to go out survive restarts and crashes.
- **See whether an alert arrived.** Each event on a monitor's page shows how its alert went on every channel (sent, retrying, refused, given up, or not sent and why) with **Retry**, and **Notifications** shows each channel's last alert and recent failures. New metrics: `uptimy_agent_alert_deliveries_total` and `uptimy_agent_alert_deliveries_pending`.
- **Recovery threshold:** `recovery_threshold` (in the form, YAML and the `upti.my/recovery-threshold` annotation) is how many good checks in a row bring a down healthcheck back up, so a flapping target doesn't send down/up pairs. `upti.my/failure-threshold` sets the failure threshold of discovered monitors.
- **Reminders while down:** a channel can remind you every 15 minutes to once a day while a monitor stays down, so a muted channel or a missed message doesn't leave an outage unnoticed.
- **Acknowledge** a down monitor to stop its reminders and tell your channels who's on it. Lists and the monitor's page show who acknowledged it.
- `uptimy-agent validate monitors.yaml` checks a monitors file in CI without starting the agent: every problem with its monitor, unset `${VAR}`s as warnings (`--strict-env` for errors), and `--format json`.
- `TRUSTED_PROXIES` sets which proxies' `X-Forwarded-For` and `X-Forwarded-Proto` headers are believed. By default, loopback and private networks.
- Webhook alerts carry an `id` that's the same on every attempt, and reminders and acknowledgements a `type`.
- The Uptime Kuma importer points out monitors that re-sent their alerts while down, so you can turn on reminders.
- **Import from Railway:** Settings → Import from Railway turns the services of a Railway project into HTTP checks (private when the agent runs in the project), database checks and cron heartbeats, from a project token that isn't stored.

### Changed
- **Mistakes in `monitors.yaml` no longer stop the agent.** A file that doesn't parse (or has two monitors with one name) is held back and the monitors stay as they were; otherwise the valid monitors are applied and each invalid one keeps its last good settings. The error is logged and shown on the dashboard, the monitor lists and Settings.
- A database target whose `${VAR}` is set to something that isn't a valid URL fails only that monitor's checks, naming the variable (not its value), instead of stopping the agent. `uptimy-agent validate` warns about it.
- Requests carry `User-Agent: uptimy-agent/<version>` (was `uptimy-agent`), in the same form as Uptimy Cloud's checkers but distinct from them.
- The upgrade logs what it removed (Uptimy alert channels, a pasted check-in URL), and the agent warns at startup while `UPTIMY_HEARTBEAT_URL` is still set.

### Upgrading from 0.1.8
- Back up `/data` first: rolling back to 0.1.8 after upgrading isn't possible, because the database schema is newer.
- The pasted heartbeat URL and `UPTIMY_HEARTBEAT_URL` are removed. Connect via **Settings → Uptimy Cloud** instead.
- Agents connected on 0.1.8 that are over the plan's agent limit won't sync until one is disconnected or the plan is upgraded.
- The Cloud connection belongs to the data volume (its install id). A cloned data volume (two agents sharing an install id) isn't supported, and a stateless deployment (no volume) can't keep a Cloud connection.

### Removed
- **Watch the watcher** as a separate heartbeat: it's part of **Connect to Uptimy Cloud** now, which checks the agent in with every upload (every minute, every 5 minutes on Free) and alerts after two missed uploads unless you choose a longer delay. Pasting a heartbeat URL and `UPTIMY_HEARTBEAT_URL` (Helm `uptimy.heartbeatUrl`) are gone; an agent that was checking in with one isn't watched until you connect it.
- The **Uptimy** alert channel. Connect to Uptimy Cloud instead: agent monitors become real Uptimy monitors, whose outages open incidents there. Existing Uptimy channels are deleted on upgrade (Uptimy stopped accepting them on 2026-10-14).

### Fixed
- A down alert held for a maintenance window is no longer forgotten when the agent restarts during the window.
- Alerts no longer get lost when the agent shuts down right after a status change.
- A monitor down for longer than `RETENTION_DAYS` no longer comes back as pending after a restart and alerts again: its current status is kept.
- Behind a proxy, failed sign-ins were counted against the proxy's address, so one person guessing passwords could lock everyone out for a while. They're now counted per client, from `X-Forwarded-For` sent by a trusted proxy.
- `X-Forwarded-Proto` is only believed from trusted proxies when deciding whether the session cookie is Secure.

## [0.1.8] - 2026-10-07

### Changed
- **Connect to Uptimy** now signs in with OAuth. Uptimy's consent page sends the browser back with a one-time code, and the agent's server exchanges it, with a secret it never shares, for the agent key. The key never passes through your browser, and Uptimy only creates it once you approve, so a connection you abandon leaves nothing behind. Connecting while signed out of Uptimy, or without an Uptimy account yet, now brings you back to the consent page after you sign in or sign up. Agents up to 0.1.7 keep connecting the old way.

## [0.1.7] - 2026-10-04

### Added
- The Uptime Kuma importer carries over Pushover, Gotify, Matrix, Mattermost, Google Chat, Rocket.Chat and Opsgenie notifications as "More services (Shoutrrr)" channels, and Kuma's status page groups as sections, in Kuma's order. Tested on a database from Uptime Kuma 2.5.5.

## [0.1.6] - 2026-10-03

### Added
- **Incidents** on the status page, under the new **Incidents** page: an incident has a severity, the monitors it affects and a timeline of updates from investigating to resolved, and stays listed for 14 days once resolved. An open incident turns the page's headline to "Some Issues Detected" ("Outage Detected" when critical).
- **An announcement** on the status page: one message under its status, for news like a migration or a new region, posted from the status page editor and shown until you remove it or until a time you choose.
- **Badges** for READMEs: `/badge/status.svg` for the page's overall status, and `/badge/<id>/status.svg` and `/badge/<id>/uptime.svg?period=24h|7d|30d|90d` for each monitor on the status page. The status page editor lists them with Markdown to copy.
- **Custom domain for the status page:** set one (e.g. `status.example.com`) under Status page and that address shows the status page at its root, and serves nothing else but heartbeat pings: no sign-in, API or `/metrics`, so the dashboard can stay on a private address. DNS and TLS stay with your Ingress, Railway or proxy. The address you're using for the dashboard can't be chosen.

### Changed
- The status page editor is laid out like the hosted Uptimy app's: tabs for Editor, Announcement, Custom domain, Badges and a live Preview, with page settings, branding and sections full width in the Editor tab.
- The status page looks like the hosted Uptimy pages: incidents in Active and Past Incidents sections with cards that expand into a timeline, maintenance in a Scheduled Maintenance section (in progress with a progress bar, upcoming, and completed in the last week), and "30 days ago … Today" under the daily bars.
- The public status API lists monitors going down and recovering under `events`; `incidents` now holds posted incidents, and `announcement` the page's announcement.
- The favicon is also served as `favicon.ico` and an Apple touch icon, so Safari shows it.
- Two-factor setup: numbered steps, a framed QR code with the key behind "Can't scan it?", a code field that submits itself, and recovery codes you confirm you've saved before closing.

## [0.1.5] - 2026-10-03

### Added
- Two-factor sign-in with an authenticator app (TOTP): set up under Account with a QR code, 10 one-time recovery codes, other devices signed out when it's turned on, and a used code can't be used again. Admins can turn it off for another user; `uptimy-agent reset-2fa <username>` does it from the server when nobody can sign in. API tokens aren't affected.
- Prometheus metrics at `/metrics`, with an API token: each monitor's status, check results and durations, heartbeat runs by outcome and next due time, maintenance, Kubernetes discovery, and the agent's version, goroutines and memory. Targets aren't exported. The chart can create a ServiceMonitor (`metrics.serviceMonitor`).

## [0.1.4] - 2026-10-02

### Added
- **CronJobs without pings:** label a CronJob `upti.my/monitor: "true"` and the agent creates a heartbeat on its schedule and time zone, and records each run from the CronJob's Jobs at the times Kubernetes saw them: start, finish, duration, and for a failure the reason with the container's exit code (`OOMKilled`, exit code 1, ...). Missed runs are detected from the schedule, and suspending the CronJob pauses the heartbeat. `upti.my/grace` sets how long after its scheduled time a run may finish (default: the Job's `activeDeadlineSeconds` plus a minute, or else the time between runs, at most an hour).
- **Kubernetes Service checks** (`<namespace>/service/<name>`): up while the Service has a ready endpoint, reusing the pods' readinessProbes with no traffic to the app. Discovery uses them for Services annotated `upti.my/type: kubernetes`.
- **Settings → Kubernetes:** discovery's status (last scan, scope, monitors found) and every problem it found, such as a mistyped label value, an invalid annotation or missing RBAC, plus a browser of the cluster's resources, monitored or not, with the `kubectl label` command for one or for all shown. Empty healthcheck and heartbeat pages point to it when the agent runs in a cluster.
- **More services (Shoutrrr):** one alert channel URL reaches any of 30+ services, including Pushover, Gotify, Matrix, Google Chat, Mattermost, Rocket.Chat, Opsgenie, Signal, Zulip and Home Assistant. Errors never repeat the URL or its credentials.
- The heartbeat form picks the interval and grace period on a slider (1 minute to 1 year), with the exact value editable below it.
- Discovered monitors show the object they came from ("Discovered in Kubernetes · service shop/checkout"), and a discovered CronJob's page explains how its runs are read.

### Changed
- Discovered Services are checked on their pods' readinessProbe path and scheme instead of `/`, and ports with an HTTP readinessProbe get an HTTP check even when they don't look like HTTP.
- Discovered monitors are tied to their Kubernetes object instead of their name: renaming one with `upti.my/name` keeps its history, and two objects may share a name. Monitors discovered by 0.1.3 are matched up on the first scan.
- `upti.my/monitor` also accepts `yes`, `1` and `on`; `false`, `no`, `0` and `off` opt a resource out explicitly; other values are reported instead of ignored.
- A heartbeat whose run has started isn't shown as late while it runs; it counts as missed only if it hasn't finished by the deadline. A run noticed after its deadline that finished in time counts as on time.
- The chart lets the agent read Services, EndpointSlices, CronJobs and Jobs, and list pods when discovery is on.

### Fixed
- An invalid `upti.my/` annotation no longer deletes the object's monitor and its history; it's kept as it was and reported.
- The heartbeat page lists the newest runs first, 10 at a time.
- Switching a heartbeat between a fixed interval and a cron expression no longer shifts the form, and rows of cards line up with the cards above them on the dashboard and heartbeat pages.

## [0.1.3] - 2026-10-02

### Added
- Kubernetes auto-discovery: label a Service, Ingress, Gateway API HTTPRoute, Deployment, StatefulSet or DaemonSet with `upti.my/monitor: "true"` and the agent monitors it within 30 seconds. `upti.my/` annotations set the name, path, port, interval and more. Discovered monitors are read-only in the UI and are removed with the label. On by default in the chart (`discovery.enabled`), which now lets the agent list those resources.
- The Helm chart is published with each release: `helm install uptimy-agent oci://ghcr.io/uptimy/charts/uptimy-agent`, at the same version as the agent, and listed on Artifact Hub.
- Images and charts are signed with cosign (keyless, from CI).

## [0.1.2] - 2026-10-02

Not published: the release build failed before the image and chart were pushed. Use 0.1.3.

## [0.1.1] - 2026-10-01

### Fixed
- Status page on phones: a monitor's type badge no longer runs into its "Last change" time, and badges stay on one line.

### Added
- A one-click Railway template ("Deploy on Railway" in the README) and a live demo.

## [0.1.0] - 2026-10-01

First public release.

### Monitoring
- Healthchecks: HTTP(S) with status and keyword checks, ping (unprivileged ICMP), TCP, DNS, TLS certificate expiry, Kubernetes workloads, and PostgreSQL, MySQL and Redis (real logins and queries).
- Heartbeats for cron jobs, backups and workers: an interval or a cron schedule in any time zone with a grace period; start, fail and exit-code pings; run history with duration and the job's output; on-time rate, missed and failed runs.
- Alerts by email (SMTP), Slack, Microsoft Teams, Discord, Telegram, ntfy, PagerDuty (incidents open and resolve) and webhooks, on down and on recovery. Each channel alerts for every monitor or only chosen ones.
- Maintenance windows: no alerts during planned work, a held alert when a monitor is still down afterwards, and a notice on the status page.
- Healthchecks and heartbeats in YAML (`MONITORS_FILE` / `MONITORS_YAML`) alongside the UI.

### Status page
- Public page at `/status` with a logo (and dark-mode logo), accent color, website link and sections. Shows names with uptime or on-time rate only, never targets. Sections and their monitors are managed in the status page editor (drag to reorder, rename inline), as on the Uptimy platform. Announces maintenance, in progress and up to a week ahead.

### Uptimy
- "Watch the watcher": connect to Uptimy in one click, and Uptimy alerts from outside when the agent stops checking in, with a configurable delay and maintenance windows.
- An Uptimy alert channel: paste the webhook URL of an Uptimy Agent integration, and down and recovery alerts open and resolve incidents in Uptimy, which can trigger workflows.

### Running it
- One binary with the UI built in; Docker images, a Helm chart and a Railway template.
- Users with admin and viewer roles; light and dark themes.
- API tokens (full or read-only) for scripts and CI.
- Import monitors and notifications from Uptime Kuma (`kuma.db`), with a review step.

[Unreleased]: https://github.com/uptimy/agent/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/uptimy/agent/compare/v0.1.8...v0.2.0
[0.1.8]: https://github.com/uptimy/agent/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/uptimy/agent/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/uptimy/agent/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/uptimy/agent/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/uptimy/agent/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/uptimy/agent/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/uptimy/agent/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/uptimy/agent/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/uptimy/agent/releases/tag/v0.1.0
