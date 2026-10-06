// Typed client for the agent's JSON API. Types mirror the Go structs in
// internal/monitor, internal/notify and internal/api.

// Check types and notification channels are registered in Go and described
// by /api/check-types and /api/notifier-types, so the UI doesn't list them.
export type Status = "pending" | "up" | "down" | "paused";

// ── Monitors ──────────────────────────────────────────────────────────────
// A monitor is a healthcheck (the agent probes a target) or a heartbeat (a
// scheduled job pings the agent). Mirrors internal/monitor.

export type MonitorKind = "healthcheck" | "heartbeat";

export type ConfigValue = string | number | boolean | Record<string, string> | undefined;

/** A healthcheck's settings (internal/monitor.Check). */
export interface Check {
  type: string; // a registered check type, see /api/check-types
  target: string;
  interval_seconds: number;
  timeout_seconds: number;
  failure_threshold: number;
  config: Record<string, ConfigValue>;
}

/** A heartbeat's settings (internal/monitor.Heartbeat). Set every_seconds or cron. */
export interface HeartbeatSpec {
  token: string; // empty for viewers
  every_seconds?: number;
  cron?: string;
  timezone?: string;
  grace_seconds: number;
}

/** Where a monitor is defined. Only "ui" monitors are edited in the UI. */
export type MonitorSource = "ui" | "file" | "kubernetes";

export interface Monitor {
  id: number;
  kind: MonitorKind;
  name: string;
  paused: boolean;
  source: MonitorSource;
  /** For a discovered monitor, the object it came from: "service/shop/checkout". */
  source_ref?: string;
  // Status page placement, set in its editor.
  public: boolean;
  status_label: string;
  status_order: number;
  status_section: string;
  check?: Check;
  heartbeat?: HeartbeatSpec;
  created_at: string;
  updated_at: string;
}

export interface MonitorEvent {
  id: number;
  monitor_id: number;
  time: string;
  status: Status;
  message: string;
}

export interface ActivityItem extends MonitorEvent {
  monitor_name: string;
  kind: MonitorKind;
}

/** One day of history: the share of checks that were up, or of runs on time. */
export interface Daily {
  date: string;
  count: number;
  ratio: number | null;
}

// ── Healthchecks ──────────────────────────────────────────────────────────

export interface Result {
  monitor_id: number;
  time: string;
  ok: boolean;
  latency_ms: number;
  message: string;
}

export interface Healthcheck extends Monitor {
  check: Check;
}

export interface HealthcheckSummary extends Healthcheck {
  status: Status;
  target: string; // the check's target, password masked
  last_result: Result | null;
  recent: Result[];
  uptime_24h: number | null;
  avg_latency_24h: number | null;
  in_maintenance: boolean;
}

interface Uptime {
  checks: number;
  ratio: number | null;
  avg_latency_ms: number | null;
}

export interface HealthcheckDetail {
  healthcheck: HealthcheckSummary;
  uptime: Record<"24h" | "7d" | "30d", Uptime>;
  notifier_ids: number[]; // the channels limited to some monitors that include this one
}

// Whether a monitor is on the status page is set in the status page editor.
export interface HealthcheckInput {
  name: string;
  paused: boolean;
  check: Check;
  notifier_ids?: number[]; // omitted: unchanged
}

/** A check type (internal/monitor.CheckType). */
export interface CheckTypeInfo {
  type: string;
  label: string;
  summary: string;
  order: number;
  target: { label: string; placeholder?: string; hint?: string; optional?: boolean };
  fields: SchemaField[] | null;
  requires?: string;
}

// ── Heartbeats ────────────────────────────────────────────────────────────

export type HeartbeatState = "waiting" | "on_time" | "late" | "missed" | "failed" | "paused";

export interface Run {
  id: number;
  monitor_id: number;
  due_at: string | null;
  started_at: string | null;
  finished_at: string | null;
  outcome: "running" | "success" | "failure" | "missed";
  on_time: boolean;
  duration_ms: number | null;
  message: string;
}

export interface RunStats {
  runs: number;
  on_time: number;
  missed: number;
  failed: number;
  ratio: number | null;
  avg_duration_ms: number | null;
}

export interface Heartbeat extends Monitor {
  heartbeat: HeartbeatSpec;
}

export interface HeartbeatSummary extends Heartbeat {
  status: Status;
  schedule: string; // in words
  // due_at and deadline are left out while paused.
  tracking: { state: HeartbeatState; due_at?: string; deadline?: string; running_since: string | null };
  last_run: Run | null;
  recent: Run[];
  stats_30d: RunStats;
  in_maintenance: boolean;
}

export interface HeartbeatDetail {
  heartbeat: HeartbeatSummary;
  stats: Record<"24h" | "7d" | "30d", RunStats>;
  daily: Daily[];
  upcoming: string[];
  notifier_ids: number[];
}

export interface HeartbeatInput {
  name: string;
  paused: boolean;
  heartbeat: Omit<HeartbeatSpec, "token">;
  notifier_ids?: number[];
}

export type SchedulePreview = { schedule: string; upcoming: string[]; error?: undefined } | { error: string };

// ── Forms built from the server's type descriptions ───────────────────────

/** One setting in a type's form (internal/schema.Field). */
export interface SchemaField {
  key: string;
  label: string;
  input: "text" | "number" | "select" | "switch" | "textarea" | "password";
  placeholder?: string;
  hint?: string;
  options?: string[];
  default?: string | number | boolean;
  required?: boolean;
  wide?: boolean;
}

/** A notification channel (internal/notify.Channel). */
export interface NotifierChannel {
  type: string;
  label: string;
  help: string;
  order: number;
  fields: SchemaField[];
}

export interface Notifier {
  id: number;
  name: string;
  type: string;
  enabled: boolean;
  config: Record<string, ConfigValue>;
  created_at: string;
  // Alerts for every monitor, or only those in monitor_ids.
  all_monitors: boolean;
  monitor_ids: number[];
}

export type NotifierInput = Omit<Notifier, "id" | "created_at">;

// ── Incidents ─────────────────────────────────────────────────────────────

export type IncidentStatus = "investigating" | "identified" | "monitoring" | "resolved";
export type IncidentSeverity = "low" | "medium" | "high" | "critical";

/** An incident posted on the status page (internal/incident). */
export interface Incident {
  id: number;
  title: string;
  severity: IncidentSeverity;
  /** The latest update's. */
  status: IncidentStatus;
  monitor_ids: number[];
  created_at: string;
  resolved_at: string | null;
  updates: IncidentUpdate[]; // newest first
}

export interface IncidentUpdate {
  id: number;
  status: IncidentStatus;
  message: string;
  created_at: string;
}

export interface NewIncident {
  title: string;
  severity: IncidentSeverity;
  status: IncidentStatus;
  message: string;
  monitor_ids: number[];
}

// ── Maintenance ───────────────────────────────────────────────────────────

/** A maintenance window (internal/maintenance.Window). */
export interface MaintenanceWindow {
  id: number;
  title: string;
  description: string;
  starts_at: string;
  ends_at: string;
  all_monitors: boolean;
  monitor_ids: number[];
  public: boolean; // announced on the status page
  created_at: string;
  state: "scheduled" | "active" | "ended";
}

export type MaintenanceInput = Omit<MaintenanceWindow, "id" | "created_at" | "state">;

// ── API tokens and importing ──────────────────────────────────────────────

export interface APIToken {
  id: number;
  name: string;
  hint: string; // the token's last characters
  read_only: boolean;
  created_at: string;
  last_used_at: string | null;
}

/** What an Uptime Kuma database would become (internal/kuma.Plan). */
export interface KumaPlan {
  /** Kuma's status page groups, which become sections. */
  sections: StatusSection[];
  monitors: { kuma_id: number; monitor: Monitor; notes: string[] | null }[];
  notifiers: { kuma_id: number; notifier: Notifier; kuma_monitor_ids: number[] | null; notes: string[] | null }[];
  skipped: KumaSkipped[];
}

export interface KumaSkipped {
  what: "monitor" | "notification";
  name: string;
  type?: string;
  reason: string;
}

export interface KumaImportResult {
  monitors: { id: number; name: string; kind: MonitorKind }[];
  notifiers: { id: number; name: string }[];
  skipped: KumaSkipped[];
}

export interface Info {
  version: string;
  kubernetes: boolean;
  /** Kubernetes discovery is running (in a cluster, not turned off). */
  discovery: boolean;
  uptimy_heartbeat: boolean;
  status_page_enabled: boolean;
  status_page_title: string;
  monitors_file: boolean;
  retention_days: number;
}

/** How Kubernetes discovery is doing: its last scan and what it couldn't use. */
export interface DiscoveryStatus {
  /** "cluster", or "namespace <name>" when RBAC limits it. */
  scope: string;
  last_scan?: string;
  error?: string;
  monitors: number;
  warnings: string[];
}

/** An object discovery could monitor, labeled or not. */
export interface KubeResource {
  kind: string;
  namespace: string;
  name: string;
  /** Its upti.my/monitor value, if it has one, and what that means. */
  label?: string;
  label_state: "in" | "out" | "invalid" | "";
  monitors: { id: number; kind: MonitorKind; name: string }[];
}

export interface KubeResources {
  items: KubeResource[];
  /** Kinds with more objects than were listed. */
  truncated: string[];
}

export interface UptimyCheckIn {
  enabled: boolean;
  // Set when connected with "Connect to Uptimy" (vs. a pasted URL or env var).
  account: { workspace_name: string; connected_at: string; connected_by: string } | null;
  // Only on disconnect: something couldn't be cleaned up in Uptimy.
  warning?: string;
  url?: string;
  managed_by_env: boolean;
  interval_seconds: number;
  last_ping_at: string | null;
  last_ok: boolean;
  last_error?: string;
  // Paused in Uptimy: the agent doesn't check in and nobody is alerted.
  paused: boolean;
}

// When Uptimy alerts about a silent agent. Only available for a
// "Connect to Uptimy" connection; Uptimy holds the values.
export interface UptimyAlerting {
  available: boolean;
  alert_after_seconds?: number;
  paused: boolean;
  maintenance_until?: string;
  normal_alert_after_seconds?: number;
  alert_after_choices: number[]; // minutes
  maintenance_choices: number[]; // minutes
}

export type Role = "admin" | "viewer";

export interface User {
  id: number;
  username: string;
  role: Role;
  must_change_password: boolean;
  password_managed_by_env: boolean;
  created_at: string;
  last_login_at: string | null;
  /** Signing in also takes a code from an authenticator app. */
  two_factor: boolean;
}

export interface AuthState {
  authenticated: boolean;
  user: User | null;
}

export interface PublicMonitor {
  name: string;
  kind: MonitorKind;
  type_label: string;
  status: Status;
  last_change: string | null;
  // A healthcheck's uptime, or the share of a heartbeat's runs that were on time.
  ratio: number | null;
  days: Daily[];
  last_run?: string; // heartbeats
  in_maintenance?: boolean;
}

export interface PublicMaintenance {
  title: string;
  description: string;
  starts_at: string;
  ends_at: string;
  created_at: string;
  state: "scheduled" | "active" | "ended";
  active: boolean;
  monitors: string[]; // public names; empty means all
}

export interface StatusPageLogos {
  light?: string;
  dark?: string;
}

export interface PublicStatus {
  title: string;
  description: string;
  logos: StatusPageLogos;
  accent_color: string; // "" = Uptimy green
  website_url: string;
  overall: "operational" | "degraded" | "outage" | "maintenance";
  maintenance: PublicMaintenance[];
  sections: { name: string; monitors: PublicMonitor[] }[];
  /** The page's announcement, while it's shown. */
  announcement: Announcement | null;
  /** Posted incidents, open and recently resolved. */
  incidents: PublicIncident[];
  /** Monitors going down and recovering. */
  events: { monitor: string; status: Status; time: string }[];
  updated: string;
}

export interface PublicIncident {
  title: string;
  severity?: IncidentSeverity;
  status: IncidentStatus | "";
  monitors: string[]; // public names of affected monitors on the page
  created_at: string;
  resolved_at?: string;
  updates: { status?: IncidentStatus; message: string; time: string }[]; // newest first
}

/** The status page's one announcement (statuspage.Announcement). */
export interface Announcement {
  title: string;
  message: string;
  posted_at: string;
  updated_at: string;
  show_until: string | null;
}

export interface StatusSection {
  id: string;
  name: string;
}

export interface StatusPageSettings {
  enabled: boolean;
  title: string;
  description: string;
  show_events: boolean;
  accent_color: string;
  website_url: string;
  /** A hostname that serves only the status page, e.g. status.example.com. */
  domain: string;
  sections: StatusSection[];
}

export interface StatusPageMonitor {
  id: number;
  name: string;
  kind: MonitorKind;
  type_label: string;
  source: MonitorSource;
  public: boolean;
  label: string;
  section: string;
}

export interface StatusPageConfig extends StatusPageSettings {
  logos: StatusPageLogos;
  monitors: StatusPageMonitor[];
}

export class ApiError extends Error {
  constructor(
    public status: number,
    message: string,
    /** The response body, for errors that carry more than a message. */
    public body: Record<string, unknown> = {},
  ) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown, cache?: RequestCache): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      cache,
      credentials: "same-origin",
      // Always JSON: the server rejects non-JSON mutations as a CSRF guard.
      headers: { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    // The browser's own wording ("Failed to fetch", "Load failed") means
    // nothing to people; status 0 = no response at all.
    throw new ApiError(0, "Can't reach the agent. Check that it's running, then try again.");
  }
  if (res.status === 204) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText, data);
  return data as T;
}

/** Sends a file as the raw request body. */
async function upload<T>(path: string, file: File): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/octet-stream" },
      body: file,
    });
  } catch {
    throw new ApiError(0, "Can't reach the agent. Check that it's running, then try again.");
  }
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new ApiError(res.status, data.error ?? res.statusText);
  return data as T;
}

export const api = {
  authState: () => request<AuthState>("GET", "/api/auth/state"),
  login: (username: string, password: string, code?: string) =>
    request<AuthState>("POST", "/api/auth/login", { username, password, code }),
  twoFactor: () => request<{ enabled: boolean; recovery_codes_left: number }>("GET", "/api/auth/2fa"),
  setupTwoFactor: () => request<{ secret: string; uri: string }>("POST", "/api/auth/2fa/setup", {}),
  enableTwoFactor: (code: string) => request<{ recovery_codes: string[] }>("POST", "/api/auth/2fa/enable", { code }),
  disableTwoFactor: (password: string) => request<void>("POST", "/api/auth/2fa/disable", { password }),
  newRecoveryCodes: (password: string) =>
    request<{ recovery_codes: string[] }>("POST", "/api/auth/2fa/recovery-codes", { password }),
  resetTwoFactor: (userID: number) => request<void>("POST", `/api/users/${userID}/2fa/reset`, {}),
  logout: () => request("POST", "/api/auth/logout"),
  changePassword: (current: string, next: string) =>
    request<AuthState>("POST", "/api/auth/password", { current, new: next }),

  sessions: () => request<{ count: number }>("GET", "/api/auth/sessions"),
  apiTokens: () => request<APIToken[]>("GET", "/api/auth/tokens"),
  createAPIToken: (t: { name: string; read_only: boolean }) =>
    request<{ token: string; api_token: APIToken }>("POST", "/api/auth/tokens", t),
  deleteAPIToken: (id: number) => request<void>("DELETE", `/api/auth/tokens/${id}`),
  signOutOthers: () => request<{ count: number }>("POST", "/api/auth/sessions/sign-out-others"),

  users: () => request<User[]>("GET", "/api/users"),
  createUser: (u: { username: string; password: string; role: Role }) => request<User>("POST", "/api/users", u),
  updateUser: (id: number, patch: { role?: Role; password?: string }) =>
    request<User>("PUT", `/api/users/${id}`, patch),
  deleteUser: (id: number) => request<void>("DELETE", `/api/users/${id}`),
  info: () => request<Info>("GET", "/api/info"),

  checkTypes: () => request<CheckTypeInfo[]>("GET", "/api/check-types"),

  healthchecks: () => request<HealthcheckSummary[]>("GET", "/api/healthchecks"),
  healthcheck: (id: number) => request<HealthcheckDetail>("GET", `/api/healthchecks/${id}`),
  createHealthcheck: (h: HealthcheckInput) => request<Healthcheck>("POST", "/api/healthchecks", h),
  updateHealthcheck: (id: number, h: HealthcheckInput) => request<Healthcheck>("PUT", `/api/healthchecks/${id}`, h),
  checkNow: (id: number) => request<void>("POST", `/api/healthchecks/${id}/check`),
  results: (id: number, hours: number) => request<Result[]>("GET", `/api/healthchecks/${id}/results?hours=${hours}`),

  heartbeats: () => request<HeartbeatSummary[]>("GET", "/api/heartbeats"),
  heartbeat: (id: number) => request<HeartbeatDetail>("GET", `/api/heartbeats/${id}`),
  createHeartbeat: (h: HeartbeatInput) => request<Heartbeat>("POST", "/api/heartbeats", h),
  updateHeartbeat: (id: number, h: HeartbeatInput) => request<Heartbeat>("PUT", `/api/heartbeats/${id}`, h),
  rotateToken: (id: number) => request<Heartbeat>("POST", `/api/heartbeats/${id}/token`),
  discoveryStatus: () => request<DiscoveryStatus>("GET", "/api/kubernetes/discovery"),
  kubeResources: () => request<KubeResources>("GET", "/api/kubernetes/resources"),
  runs: (id: number, limit = 100) => request<Run[]>("GET", `/api/heartbeats/${id}/runs?limit=${limit}`),
  previewSchedule: (spec: Omit<HeartbeatSpec, "token">) =>
    request<SchedulePreview>("POST", "/api/heartbeats/preview", spec),

  // Both kinds: kind picks /api/healthchecks or /api/heartbeats.
  pauseMonitor: (kind: MonitorKind, id: number, paused: boolean) =>
    request<Monitor>("POST", `/api/${kind}s/${id}/pause`, { paused }),
  deleteMonitor: (kind: MonitorKind, id: number) => request<void>("DELETE", `/api/${kind}s/${id}`),
  events: (id: number) => request<MonitorEvent[]>("GET", `/api/monitors/${id}/events`),

  incidents: () => request<Incident[]>("GET", "/api/incidents"),
  createIncident: (i: NewIncident) => request<Incident>("POST", "/api/incidents", i),
  updateIncident: (id: number, i: { title: string; severity: IncidentSeverity; monitor_ids: number[] }) =>
    request<Incident>("PUT", `/api/incidents/${id}`, i),
  deleteIncident: (id: number) => request<void>("DELETE", `/api/incidents/${id}`),
  addIncidentUpdate: (id: number, u: { status: IncidentStatus; message: string }) =>
    request<Incident>("POST", `/api/incidents/${id}/updates`, u),
  editIncidentUpdate: (id: number, updateID: number, message: string) =>
    request<Incident>("PUT", `/api/incidents/${id}/updates/${updateID}`, { message }),
  deleteIncidentUpdate: (id: number, updateID: number) =>
    request<Incident>("DELETE", `/api/incidents/${id}/updates/${updateID}`),

  maintenance: () => request<MaintenanceWindow[]>("GET", "/api/maintenance"),
  createMaintenance: (w: MaintenanceInput) => request<MaintenanceWindow>("POST", "/api/maintenance", w),
  updateMaintenance: (id: number, w: MaintenanceInput) =>
    request<MaintenanceWindow>("PUT", `/api/maintenance/${id}`, w),
  endMaintenance: (id: number) => request<MaintenanceWindow>("POST", `/api/maintenance/${id}/end`),
  deleteMaintenance: (id: number) => request<void>("DELETE", `/api/maintenance/${id}`),

  planKumaImport: (file: File) => upload<KumaPlan>("/api/import/kuma", file),
  applyKumaImport: (plan: Pick<KumaPlan, "sections" | "monitors" | "notifiers">) =>
    request<KumaImportResult>("POST", "/api/import/kuma/apply", plan),
  activity: () => request<ActivityItem[]>("GET", "/api/activity"),

  notifiers: () => request<Notifier[]>("GET", "/api/notifiers"),
  notifierTypes: () => request<NotifierChannel[]>("GET", "/api/notifier-types"),
  createNotifier: (n: NotifierInput) => request<Notifier>("POST", "/api/notifiers", n),
  updateNotifier: (id: number, n: NotifierInput) => request<Notifier>("PUT", `/api/notifiers/${id}`, n),
  deleteNotifier: (id: number) => request<void>("DELETE", `/api/notifiers/${id}`),
  testNotifier: (id: number) => request("POST", `/api/notifiers/${id}/test`),

  uptimyCheckIn: () => request<UptimyCheckIn>("GET", "/api/uptimy/heartbeat"),
  connectUptimyURL: (url: string) => request<UptimyCheckIn>("PUT", "/api/uptimy/heartbeat", { url }),
  disconnectUptimy: () => request<UptimyCheckIn>("DELETE", "/api/uptimy/heartbeat"),
  testUptimyCheckIn: () => request<UptimyCheckIn>("POST", "/api/uptimy/heartbeat/test"),
  uptimyAlerting: () => request<UptimyAlerting>("GET", "/api/uptimy/heartbeat/settings"),
  updateUptimyAlerting: (patch: { alert_after_seconds?: number; paused?: boolean }) =>
    request<UptimyAlerting>("PUT", "/api/uptimy/heartbeat/settings", patch),
  startUptimyMaintenance: (minutes: number) =>
    request<UptimyAlerting>("POST", "/api/uptimy/heartbeat/maintenance", { minutes }),
  endUptimyMaintenance: () => request<UptimyAlerting>("DELETE", "/api/uptimy/heartbeat/maintenance"),
  startUptimyConnect: (origin: string) =>
    request<{ authorize_url: string }>("POST", "/api/uptimy/connect/start", { origin }),
  finishUptimyConnect: (state: string, code: string) =>
    request<UptimyCheckIn>("POST", "/api/uptimy/connect/finish", { state, code }),

  announcement: () => request<Announcement | null>("GET", "/api/status-page/announcement"),
  saveAnnouncement: (a: { title: string; message: string; show_until: string | null }) =>
    request<Announcement>("PUT", "/api/status-page/announcement", a),
  removeAnnouncement: () => request<void>("DELETE", "/api/status-page/announcement"),
  statusPageConfig: () => request<StatusPageConfig>("GET", "/api/status-page"),
  saveStatusPageConfig: (
    c: StatusPageSettings & { monitors: { id: number; public: boolean; label: string; section: string }[] },
  ) => request<StatusPageConfig>("PUT", "/api/status-page", c),
  // The image travels as base64 in JSON: the server only accepts JSON writes.
  uploadStatusPageLogo: (variant: "light" | "dark", data: string) =>
    request<StatusPageConfig>("PUT", `/api/status-page/logo/${variant}`, { data }),
  deleteStatusPageLogo: (variant: "light" | "dark") =>
    request<StatusPageConfig>("DELETE", `/api/status-page/logo/${variant}`),

  // The server lets browsers cache this for 30s. The editor's preview passes
  // fresh=true so it shows a save immediately.
  publicStatus: (fresh = false) =>
    request<PublicStatus>("GET", "/api/status", undefined, fresh ? "no-cache" : undefined),
};
