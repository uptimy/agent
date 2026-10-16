# Importing

## From Uptime Kuma

Under **Settings → Import from Uptime Kuma**, upload Kuma's database (`kuma.db`, from its data folder: `/app/data` in Docker). Stop Kuma first so the copy is complete:

```bash
docker stop uptime-kuma && docker cp uptime-kuma:/app/data/kuma.db .
```

You'll see what each monitor and notification becomes, and choose what to import. HTTP, keyword, TCP port, ping, DNS, TLS, Postgres, MySQL, Redis and push monitors carry over, with their intervals, retries, accepted status codes, headers and basic or bearer auth. So do email, Slack, Teams, Discord, Telegram, ntfy, PagerDuty and webhook notifications, plus Pushover, Gotify, Matrix, Mattermost, Google Chat, Rocket.Chat and Opsgenie as "More services (Shoutrrr)" channels, along with which monitors alert where. Monitors that were on a Kuma status page are added to yours, with Kuma's groups as sections, in Kuma's order. Push monitors become heartbeats with new ping URLs, so update your jobs. Anything that can't be carried over (Docker, gRPC, inverted checks, ...) is listed with the reason. Kuma installs that use MariaDB aren't supported yet.

## From Railway

Under **Settings → Import from Railway**, paste a project token (Railway → Project → Settings → Tokens) to get a monitor for each service in its environment: an HTTP check for web services (over private networking when the agent runs in that project, otherwise at their public domain), a database check for Postgres, MySQL, MariaDB and Redis through a `${VAR}` you add to the agent, and a heartbeat for cron services. You review them first; the token is only used for that and isn't stored. Importing again skips services already imported.
