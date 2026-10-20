# Monitors

Healthchecks ask "is it up?". Heartbeats ask "did the job run, on time, and did it succeed?".

## Database monitors

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

## Heartbeats

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
- **History:** each run is kept with its result, how long it took and its message; the heartbeat's page shows the on-time rate over 24 hours, 7, 30 and 90 days.
- **Ping URLs** are generated and stay the same when you edit the heartbeat. Replace one from its page if it leaks. In YAML, set `token` to pin it (16 to 64 letters, digits, `-` or `_`); otherwise one is generated and kept.

## Ping

Ping monitors send three ICMP echo requests and are up if any reply comes back. The agent uses unprivileged ICMP sockets, so it needs no root or `NET_RAW`: Docker allows them by default, and the Helm chart sets the `net.ipv4.ping_group_range` sysctl. Elsewhere on Linux, run `sysctl -w net.ipv4.ping_group_range="0 2147483647"`.

## User-Agent

The agent identifies itself as `uptimy-agent/<version>` (e.g. `uptimy-agent/0.2.0`) on its checks, alert webhooks and calls to Uptimy. To let it through a firewall or WAF, allow that; a monitor's own `User-Agent` header replaces it. It's deliberately unlike Uptimy Cloud's checkers (`upti.my-healthcheck/1.0`), so blocking agents someone else runs never blocks Cloud checks.
