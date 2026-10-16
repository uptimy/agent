# Status page

The status page is public at `/status`, or on its own domain (`status.example.com`), which serves only the status page so sign-in and the dashboard stay private. Set a logo (with a dark-mode version), an accent color and a website link, group monitors into sections with public names, and preview it as you go. New monitors stay off the page until you add them, and it never shows targets or internal hostnames.

## Maintenance

Schedule maintenance under **Maintenance** before deploys, upgrades or migrations: for all monitors or some, starting now or later. During the window the monitors keep being checked, but nobody is alerted. If one is still down when the window ends, its alert goes out then, even if the agent restarted in between; if it recovered, nothing is sent. Public windows are announced on the status page up to a week ahead and stay listed as completed for a week after, and the affected monitors are marked as under maintenance rather than down.

## Incidents and the announcement

Under **Incidents**, report an incident when something's wrong that visitors should hear about: a title, a severity (low, medium, high or critical), the monitors it affects, and a first update. Post updates as it moves from investigating to identified, monitoring and resolved; the status page shows the whole timeline, and resolved incidents stay listed for 14 days. While one is open, the page's headline says "Some Issues Detected", or "Outage Detected" for a critical one. Updates can be corrected later, and deleting a resolution reopens the incident. Posting doesn't alert anyone; monitors do that.

For news that isn't an incident, like a migration or a new region, post the status page's **announcement** at the top of **Status page**: a title and a message shown under the page's status, until you remove it or until a time you choose. Editing it keeps when it was posted.

## Badges

Badges are served while the status page is on, and only for monitors on it:

| Badge | URL |
|---|---|
| The page's overall status | `/badge/status.svg` |
| A monitor's status | `/badge/<id>/status.svg` |
| Its uptime, or a heartbeat's on-time rate | `/badge/<id>/uptime.svg?period=24h` (`7d`, `30d` (default), `90d`) |

`?label=` replaces the left-hand text. The status page editor lists them with Markdown to copy, linking to the page.
