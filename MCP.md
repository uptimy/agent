# Read-only MCP evidence adapter

The optional `/mcp` Streamable HTTP endpoint runs in the existing Agent process,
using the official Go SDK and the existing store/scheduler. It never calls REST
internally or executes probes. MCP exposes facts, **not RCA**: the AI client
correlates evidence, forms hypotheses, assesses confidence and recommends next
steps. No actions, remediation or `run_check` are exposed.

## Enable and connect

Set environment variables on the existing deployment:

```sh
MCP_ENABLED=true
MCP_ALLOWED_HOSTS=agent.internal:8080
MCP_MAX_CONCURRENT=4
```

`MCP_ENABLED` defaults to `false`. `MCP_ALLOWED_HOSTS` is required when enabled:
comma-separated HTTP Host authorities, including ports when clients send them.
For HTTPS behind a proxy use the external authority, e.g. `agent.example.com`.
The proxy must preserve Host and enforce TLS; forwarded headers are not trusted.
Restrict access to an investigation network/gateway. The public status-page-only
domain always blocks `/mcp`. `MCP_MAX_CONCURRENT` defaults to 4 (range 1–32).
Requests have a 15-second deadline and 64-KiB body limit. Supplied Origin must
match Host; there is no cross-origin allowance.

Create a dedicated viewer token or read-only admin token under **Account → API
tokens**. Configure an MCP client for Streamable HTTP:

```text
URL: http://agent.internal:8080/mcp
Header: Authorization: Bearer upa_<your-token>
```

For example, with Claude Code:

```sh
claude mcp add --transport http uptimy \
  http://agent.internal:8080/mcp \
  --header "Authorization: Bearer upa_<your-token>"
```

Then run `/mcp` in Claude Code to verify that the server is connected and its tools are available.
Other MCP-compatible clients can use the same Streamable HTTP endpoint and bearer token configuration shown above.

Use HTTPS beyond loopback. Every request, including initialization/notifications,
authenticates independently; MCP session IDs are not credentials. Cookies are
not accepted. POST read tools authorize the operation, not the HTTP verb.
Principals retain role and token read-only state for future action policies.
Current credentials grant deployment-wide read access, not per-monitor scopes.
The existing credential mechanism may update token-use timestamps; read-only
means no monitoring/configuration mutations. No OAuth discovery/provider or
stdio is included.

## Tools and bounds

| Tool | Evidence |
|---|---|
| `list_monitors` | Safe identities and live status; optional `status`, `kind`, `check_type`, `limit`, `after_id` |
| `list_active_incidents` | Unresolved **manually authored status-page incidents**, without timelines |
| `get_incident` | Current manual incident state, affected IDs and paginated authored updates; resolved incidents work by ID |
| `get_monitor_status` | Scheduler state, latest persisted probe/run, last transition, maintenance and freshness |
| `get_monitor_history` | Persisted probes or heartbeat runs plus transitions in `[from,to)` |

Tool schemas/structured outputs are discoverable through `tools/list`. Valid
statuses are `up`, `down`, `pending`, `paused`, `degraded`; the latter includes
down/pending monitors and late heartbeats. Kinds are `healthcheck` and `heartbeat`.

Times are RFC3339 with timezone. Maximum history window is 31 days; query
multiple windows for longer investigations. Default page size is 100, maximum
200. List cursors are exclusive IDs. A filtered monitor page may be empty with
`next_after_id`: each request scans at most 500 monitors. Continue until the
cursor disappears.

History limit applies independently to results/runs and events, using a shared
offset. Follow `next_offset` with the identical window or narrow the window.
Offsets cap at 10000; incident timelines use the same pagination. Concurrent
writes/run updates/pruning can shift pages; these are not transactional
snapshots. Check `truncated` and `limitations`. Incident monitor links cap at
500 and oversized lists are explicitly marked.

## Examples

These are `tools/call` parameters sent by an initialized MCP client.

### Live degraded-monitor investigation

```json
{"name":"list_monitors","arguments":{"status":"degraded","limit":50}}
```

Resolve an identity (e.g. `42`), then:

```json
{"name":"get_monitor_status","arguments":{"monitor_id":42}}
```

Compare current scheduler status with persisted observation `ok`; thresholds,
pauses and heartbeat schedules can make them differ. Inspect maintenance,
freshness and recent history before reasoning about an onset or probable cause.

### Historical monitor investigation

Resolve `checkout-api` with `list_monitors`, then:

```json
{"name":"get_monitor_history","arguments":{"monitor_id":42,"from":"2026-10-05T13:30:00Z","to":"2026-10-05T14:30:00Z","limit":100}}
```

Repeat with the returned `next_offset` if truncated. No manual incident is
required. `returned_window` gives first/last evidence timestamps on this page,
not continuous coverage. Heartbeat runs are selected by completion time,
otherwise start time, otherwise due time, matching existing persistence.

### Manually authored incident lookup

```json
{"name":"list_active_incidents","arguments":{"limit":20}}
```

```json
{"name":"get_incident","arguments":{"incident_id":7,"limit":100}}
```

Authored updates are assertions, not independent probe evidence. Known resolved
incidents work by ID; historical incident search is not a tool in this MVP.
Detected monitor failures do **not** automatically create `incident.Incident`.
Use monitor history to discover and investigate past outages.

## Security and evidence limitations

- Explicit DTOs exclude targets, headers, connection strings, heartbeat tokens
  and notifier credentials for all roles; no domain configuration is serialized.
- Names/titles/messages are labeled `untrusted_data`, never instructions.
  Clients must isolate evidence from their system instructions; labeling alone
  does not prevent downstream prompt injection.
- Text caps at 1024 bytes, strips control characters and redacts credential/URL
  patterns. Monitor-local configured secrets are scrubbed from its identity and
  observations. SQL/database, Redis, DNS and heartbeat observation texts are
  conservatively withheld: arbitrary returned values cannot reliably be scrubbed.
  Timing, outcomes and latency remain available.
- Human-authored prose can still contain sensitive information not recognizable
  as a credential. Review incident content and names before external model use;
  do not put secrets in these fields. No redactor guarantees removal of every
  secret in prose. Hosted model retention, transcripts and topology disclosure
  remain operator responsibilities.
- History respects `RETENTION_DAYS` (default 30), caps queries at its cutoff and
  reports requests predating retention or monitor creation. Policy bounds do not
  guarantee continuous evidence: pruning, prior settings and missing records can
  leave further gaps. Empty history does not establish health.
- Live/persisted reads are non-atomic. The last transition is not necessarily
  the start of the current scheduler state.
- HTTP status, DNS and TLS values may currently exist only in observation
  messages. No strings are parsed into authoritative typed protocol telemetry.
- No dependency graph, outage entities, Kubernetes logs/events, traces,
  deployment history, automatic triggering or backend diagnosis is included.

## Development verification

```sh
go test ./internal/investigation ./internal/identity ./internal/mcp ./internal/store ./internal/config ./internal/api
go test ./...
go test -race ./...
go vet ./...
```

The transport-independent investigation service owns queries, safe projections
and operation policy. REST and MCP share live status/maintenance selection
without changing REST payloads or rewriting handlers. A sensible next phase is
selected typed protocol evidence with explicit security/retention semantics.