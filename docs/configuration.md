# Configuration

Environment variables set how the agent runs, its secrets, and (optionally) monitors as code. Everything else, including the status page, alert channels and Uptimy alerting, is set in the UI and kept in the agent's database.

| Variable | Default | Description |
| --- | --- | --- |
| `PORT` | `8080` | HTTP port |
| `DATA_DIR` | `./data` (`/data` in the image) | Where the SQLite database lives |
| `ADMIN_USERNAME` | `admin` | Username of the default admin created on first start |
| `ADMIN_PASSWORD` | – | That admin's password (min 8 chars). If unset, a random one is printed in the log on first start and must be changed at first sign-in. Setting it later resets the password, which is also how you recover a lost admin login |
| `MONITORS_FILE` | – | Path to a YAML file of monitors |
| `MONITORS_YAML` | – | The YAML itself, for platforms where mounting files is awkward |
| `KUBERNETES_DISCOVERY` | `true` | Create monitors for resources labeled `upti.my/monitor: "true"` (see [Auto-discovery](kubernetes.md#auto-discovery)). Only applies inside a cluster |
| `RETENTION_DAYS` | `30` | How long every check result, heartbeat run, event and alert is kept. A monitor's current status is kept however old |
| `HISTORY_DAYS` | `400` | How long each monitor's daily history is kept: one row a day with its uptime (or on-time rate) and response times (p50, p95, p99, max). Uptime over 7, 30 and 90 days, the status page's bars and the 30d and 90d charts come from it, so they stay exact past `RETENTION_DAYS`. At least 90 |
| `TRUSTED_PROXIES` | `private` | Proxies whose `X-Forwarded-For` and `X-Forwarded-Proto` headers are believed: who signed in (for the sign-in rate limit) and whether they used HTTPS (for Secure cookies). Comma-separated addresses and ranges (`10.0.0.0/8, 192.0.2.1`), `private` for loopback and private networks (where an Ingress, Railway's edge or a local proxy connects from), or `none` |
| `AGENT_NAME` | hostname | How this agent is labeled in Uptimy |

## Monitors in YAML

The file has two lists, `healthchecks` and `heartbeats`; see [`examples/monitors.yaml`](../examples/monitors.yaml) for every check type and both kinds of schedule. Monitors from YAML are matched by name: changing one keeps its history, and removing one deletes it on the next start. They're shown read-only in the UI, though you can still pause them during maintenance and add them to the status page. Monitors created in the UI are never touched by the file. Unknown settings are an error, so a typo doesn't go unnoticed.

A file with mistakes doesn't stop the agent. If it doesn't parse (a YAML syntax error, an unknown setting) or two monitors share a name, none of it is applied and the monitors stay as the last good file left them. If only some monitors are invalid, the rest are applied; an invalid one keeps its last good settings, or isn't created if it's new. Either way the error is logged and shown on the dashboard and the monitor lists until a restart with a fixed file. A database target whose `${VAR}` is unset or isn't a valid URL is applied too; only its checks fail, saying which variable is wrong.

Check a file before it ships, in CI or a pre-commit hook, without starting the agent:

```sh
uptimy-agent validate monitors.yaml           # or - for stdin; with no file, MONITORS_FILE / MONITORS_YAML
docker run --rm -v "$PWD:/w" ghcr.io/uptimy/agent validate /w/monitors.yaml
```

It reports every problem with its monitor and exits 1 if there are any. A `${VAR}` a database target uses that isn't set, or doesn't make a valid URL, is a warning, since CI rarely has the secrets; `--strict-env` makes it an error. `--format json` prints a report for tools.

## Users and roles

The agent starts with one admin account (see `ADMIN_USERNAME` / `ADMIN_PASSWORD`). Admins can add people under **Users**:

- **Admin:** can change everything, including users
- **Viewer:** can see monitors, history and settings, but not change them. Credentials such as webhook URLs, auth headers, database passwords and heartbeat ping URLs are hidden from viewers

New users get a temporary password to pass on and choose their own at first sign-in. Admins can reset a password or change a role at any time; the agent won't let you delete or demote yourself or remove the last admin.

**Two-factor sign-in:** anyone can turn it on under **Account → Two-factor sign-in**: scan the QR code with an authenticator app (1Password, Google Authenticator, Authy, ...), confirm a code, and save the 10 recovery codes, each good for one sign-in without the phone. Turning it on signs out your other devices. API tokens aren't affected. An admin can turn it off for another user under **Users**; if nobody who can is able to sign in, run the agent's binary against its data:

```bash
kubectl -n monitoring exec deploy/uptimy-agent -- uptimy-agent reset-2fa admin
docker exec uptimy-agent uptimy-agent reset-2fa admin
```
