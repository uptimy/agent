# API and metrics

## API

Everything in the UI goes through a JSON API under `/api`. For scripts and CI, create a token under **Account → API tokens** and send it as a bearer token:

```bash
curl -H "Authorization: Bearer upa_…" https://agent.example.com/api/healthchecks
```

A token acts as the user who made it. Choose read-only for dashboards and exporters; a viewer's tokens are always read-only. Tokens can't manage accounts, users or other tokens; that takes signing in.

## Prometheus metrics

`/metrics` serves Prometheus metrics, with an API token (read-only is enough):

```yaml
scrape_configs:
  - job_name: uptimy-agent
    authorization:
      credentials: upa_…
    static_configs:
      - targets: ["uptimy-agent.monitoring.svc:80"]
```

| Metric | What |
| --- | --- |
| `uptimy_agent_monitor_up` | 1 up, 0 down (not set while pending or paused) |
| `uptimy_agent_monitor_status` | 1 for the current status: `up`, `down`, `pending` or `paused` |
| `uptimy_agent_monitor_in_maintenance` | 1 while a maintenance window covers it |
| `uptimy_agent_checks_total` | Healthcheck probes by `result` (`success`, `failure`), counted since the agent started |
| `uptimy_agent_check_duration_seconds`, `uptimy_agent_check_last_timestamp_seconds` | The last probe's duration and time |
| `uptimy_agent_heartbeat_runs_total` | Heartbeat runs by `outcome` (`success`, `failure`, `missed`) |
| `uptimy_agent_heartbeat_next_due_timestamp_seconds`, `uptimy_agent_heartbeat_running` | When the next run is due, and whether one is running |
| `uptimy_agent_discovery_monitors`, `uptimy_agent_discovery_problems`, `uptimy_agent_discovery_last_scan_timestamp_seconds` | Kubernetes discovery |
| `uptimy_agent_info`, `uptimy_agent_go_*` | Version, goroutines and memory |

Monitors are labeled `id`, `name`, `kind`, `type` and `source`; targets aren't exported, since they can hold credentials. With the Prometheus Operator, the chart can create a ServiceMonitor (`metrics.serviceMonitor`).
