# Changelog

Notable changes to Uptimy Agent. The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow [Semantic Versioning](https://semver.org/).

## [Unreleased]

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

[Unreleased]: https://github.com/uptimy/agent/compare/v0.1.8...HEAD
[0.1.8]: https://github.com/uptimy/agent/compare/v0.1.7...v0.1.8
[0.1.7]: https://github.com/uptimy/agent/compare/v0.1.6...v0.1.7
[0.1.6]: https://github.com/uptimy/agent/compare/v0.1.5...v0.1.6
[0.1.5]: https://github.com/uptimy/agent/compare/v0.1.4...v0.1.5
[0.1.4]: https://github.com/uptimy/agent/compare/v0.1.3...v0.1.4
[0.1.3]: https://github.com/uptimy/agent/compare/v0.1.2...v0.1.3
[0.1.2]: https://github.com/uptimy/agent/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/uptimy/agent/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/uptimy/agent/releases/tag/v0.1.0
