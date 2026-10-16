# Alerts

## Routing

A channel alerts for every monitor (the default, including monitors added later) or only for the monitors you choose. Set it on the channel under **Notifications**, or tick channels under **Alerts** when editing a monitor.

## Delivery

Every alert is saved with the status change that caused it and then delivered to each channel on its own, so one failing channel never holds up or repeats the others:

- A channel that's unreachable or answers with an error like 500 or 429 is retried after 10 seconds, 30 seconds, 1, 2 and then every 5 minutes (sooner or later if it sends `Retry-After`), for up to an hour. One that refuses the alert outright, like a deleted webhook (404) or a revoked token (401), isn't retried.
- Alerts about a monitor reach a channel in order: a recovery never arrives before the outage it ends. If a channel never got any alert about an outage, it doesn't get the recovery either.
- Alerts waiting to be sent survive restarts and crashes and go out when the agent is back. Delivery is at least once: an alert cut off after the channel took it is sent again. Each alert has an `id` that's the same on every attempt, so a webhook receiver can drop the copies.
- Each event on a monitor's page shows how its alert went on every channel (sent, retrying, refused, given up, or not sent and why), with **Retry**. **Notifications** shows each channel's last alert and anything that failed in the last day. `/metrics` counts deliveries by result.

**Thresholds:** a healthcheck goes down after `failure_threshold` failed checks in a row (2 by default) and comes back up after `recovery_threshold` good ones (1 by default). Raise the recovery threshold for a target that flaps, so it doesn't send down/up pairs.

**Reminders and acknowledging:** set **Remind while down** on a channel (every 15 minutes to once a day) to be reminded while a monitor stays down, so a muted channel or a missed message doesn't leave an outage unnoticed. **Acknowledge** on a down monitor stops the reminders for that outage and tells the channels who's on it. Reminders pause during maintenance, and the next status change ends both. PagerDuty tracks outages itself, so it doesn't get reminders.

The webhook channel posts the alert as JSON:

```json
{
  "id": 42,
  "monitor_id": 7,
  "monitor_name": "Checkout API",
  "kind": "healthcheck",
  "target": "https://shop.example.com/health",
  "status": "down",
  "message": "unexpected status 503",
  "time": "2026-10-09T20:49:22.334Z"
}
```

Reminders and acknowledgements have `"status": "down"` and a `type` of `"reminder"` or `"acknowledged"`, with `down_since` and, for acknowledgements, `acknowledged_by`. Status changes have no `type`.

## More services (Shoutrrr)

For anything without its own channel, add a **More services (Shoutrrr)** channel with a [Shoutrrr URL](https://shoutrrr.nickfedor.com/latest/services/overview/): `pushover://shoutrrr:<token>@<user>`, `gotify://<host>/<token>`, `matrix://<user>:<password>@<host>`, `googlechat://chat.googleapis.com/v1/spaces/...`, `opsgenie://api.opsgenie.com/<key>`, and 30+ more. The URL holds the service's credentials, so it's stored like a password and never shown in errors.
