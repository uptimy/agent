import type { HeartbeatState, Monitor, Status } from "./api";

export type { MonitorKind } from "./api";

// A monitor is a healthcheck (the agent probes a target) or a heartbeat (a
// scheduled job pings the agent). Their pages, forms and history differ; this
// is what they share.

export const kinds = {
  healthcheck: { path: "/healthchecks", noun: "healthcheck", title: "Healthchecks" },
  heartbeat: { path: "/heartbeats", noun: "heartbeat", title: "Heartbeats" },
} as const;

export const monitorPath = (m: Pick<Monitor, "id" | "kind">) => `${kinds[m.kind].path}/${m.id}`;

const statusOrder: Record<Status, number> = { down: 0, pending: 1, up: 2, paused: 3 };

/** Needs-attention first, then by name. */
export function byAttention(a: { status: Status; name: string }, b: { status: Status; name: string }) {
  return statusOrder[a.status] - statusOrder[b.status] || a.name.localeCompare(b.name);
}

export function countByStatus(list: { status: Status }[]) {
  const counts: Record<Status, number> = { up: 0, down: 0, pending: 0, paused: 0 };
  for (const m of list) counts[m.status]++;
  return counts;
}

/** How a heartbeat's state reads, and its color. */
export const heartbeatStates: Record<HeartbeatState, { label: string; tone: "up" | "down" | "late" | "muted" }> = {
  // The style guide's status words for heartbeats; what exactly happened (a
  // missed or failed run) is in the line under the status.
  waiting: { label: "Waiting", tone: "muted" },
  on_time: { label: "Healthy", tone: "up" },
  late: { label: "At risk", tone: "late" },
  missed: { label: "Down", tone: "down" },
  failed: { label: "Down", tone: "down" },
  paused: { label: "Paused", tone: "muted" },
};
