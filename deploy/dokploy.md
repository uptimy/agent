# Dokploy template

In a Dokploy project, choose **Create Service → Template**, search for
**Uptimy Agent**, create it and click **Deploy**. Step by step:
[Deploy Uptimy Agent on Dokploy](https://www.upti.my/docs/guides/deploy-on-dokploy?utm_source=github&utm_medium=readme&utm_campaign=agent-deploy-docs).

The template lives in [Dokploy/templates](https://github.com/Dokploy/templates)
under `blueprints/uptimy-agent`. This page holds its settings, for maintaining
it or setting the agent up by hand as a Compose service.

## Service

| Setting | Value |
| --- | --- |
| Type | Docker Compose, one service `uptimy-agent` from the pinned image `ghcr.io/uptimy/agent:<version>` |
| Volume | `uptimy-agent-data`, mounted at `/data`. The image runs as non-root and Docker initializes the named volume from the image, so it needs no permission changes |
| Domain | Generated `*.sslip.io` domain, service `uptimy-agent`, port 8080 |
| Ports | None published: Traefik reaches the container on `dokploy-network` |

## Variables

| Variable | Template value | Why |
| --- | --- | --- |
| `ADMIN_PASSWORD` | `${password:24}` | Required. Password for the admin user, generated once and visible in the service's Environment tab. While it's set, the password is changed there, not in the UI |
| `ADMIN_USERNAME` | `admin` | Optional. The admin's username |

Dokploy writes the Environment tab to a `.env` file that the compose file reads,
and only the variables listed under the service's `environment:` reach the
container. To add one, such as `MONITORS_YAML`, `AGENT_NAME` or a database URL
for a `${POSTGRES_URL}` target, add it in both places and deploy again. See
[Configuration](../docs/configuration.md) for the rest.

## Updating the template

Bump the image tag in `docker-compose.yml` and the `version` in `meta.json`,
run Dokploy's validators, and test the base64 import (**Advanced → Import**)
on a Dokploy instance before opening the PR: they close template PRs that
weren't tested on a real instance.
