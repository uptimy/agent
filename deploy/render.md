# Render Blueprint

[![Deploy to Render](https://render.com/images/deploy-to-render-button.svg)](https://render.com/deploy?repo=https://github.com/uptimy/agent)

Uptimy Agent deploys to Render from [`render.yaml`](../render.yaml) at the root
of this repo. This page holds its settings, for maintaining the Blueprint or
setting the agent up by hand.

## Service

| Setting | Value |
| --- | --- |
| Type | Web service from the image `ghcr.io/uptimy/agent:latest` |
| Instance type | Starter. Disks need a paid instance, and free web services sleep when idle, which would stop the checks |
| Disk | `data`, mounted at `/data`, 1 GB |
| Health check path | `/healthz` |
| Instances | 1. SQLite has a single writer, and a service with a disk can't scale out anyway |

Render's `PORT` is picked up automatically.

## Variables

| Variable | Blueprint value | Why |
| --- | --- | --- |
| `ADMIN_PASSWORD` | generated | Required. Password for the admin user, generated once and visible in the service's Environment tab. While it's set, the password is changed there, not in the UI |
| `ADMIN_USERNAME` | `admin` | Optional. The admin's username |

Add as needed: `MONITORS_YAML` (monitors as code), `AGENT_NAME` (the agent's
label in Uptimy Cloud). See [Configuration](../docs/configuration.md) for the rest.

## Monitoring your other Render services

After deploying, **Settings → Import from Render** creates a monitor for each
service an API key can see (Render → Account Settings → API Keys). Web services
and static sites are checked at their public address. Services in the agent's
workspace and region can be checked over Render's private network instead,
and private services only that way. Cron jobs become heartbeats. See
[Importing](../docs/importing.md#from-render).
