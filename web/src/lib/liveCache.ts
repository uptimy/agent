// Pure functions that apply live SSE messages to cached query data, so the UI
// updates without refetching. Kept free of React so they're easy to test.
import type { HealthcheckSummary, MonitorEvent, Result } from "./api";

export type LiveMessage =
  | { type: "result"; monitor_id: number; data: Result } // a healthcheck was probed
  | { type: "run"; monitor_id: number } // a heartbeat run started, finished or was missed
  | { type: "heartbeat"; monitor_id: number } // a heartbeat became late (or on time again)
  | { type: "status"; monitor_id: number; data: MonitorEvent }
  | { type: "monitors"; monitor_id: number }
  | { type: "delivery"; monitor_id: number }; // an alert was sent, failed, retried, held or released

/** Bars shown per healthcheck; matches the server's recentResults. */
export const RECENT_SLOTS = 40;

const time = (iso: string) => new Date(iso).getTime();

/** Adds a probe result to a healthcheck summary (bars, last result). */
export function applyResult(m: HealthcheckSummary, r: Result): HealthcheckSummary {
  // Ignore duplicates and out-of-order results, e.g. an event that arrives
  // just after a refetch that already included it.
  if (m.last_result && time(r.time) <= time(m.last_result.time)) return m;
  return { ...m, last_result: r, recent: [...m.recent, r].slice(-RECENT_SLOTS) };
}

/** Applies a status change to a summary. */
export function applyStatus<T extends { status: MonitorEvent["status"] }>(m: T, e: MonitorEvent): T {
  return m.status === e.status ? m : { ...m, status: e.status };
}

/**
 * Applies a message to the healthcheck list. Returns null when the
 * healthcheck isn't in the list (created elsewhere), meaning the list should
 * be refetched.
 */
export function patchList(list: HealthcheckSummary[], msg: LiveMessage): HealthcheckSummary[] | null {
  const i = list.findIndex((m) => m.id === msg.monitor_id);
  if (i < 0) return null;
  let next: HealthcheckSummary;
  if (msg.type === "result") next = applyResult(list[i], msg.data);
  else if (msg.type === "status") next = applyStatus(list[i], msg.data);
  else return list;
  if (next === list[i]) return list;
  const copy = list.slice();
  copy[i] = next;
  return copy;
}

/** Appends a result to a chart series covering the last `hours`, dropping points that fell out of the window. */
export function appendResult(results: Result[], r: Result, hours: number, now = Date.now()): Result[] {
  const last = results[results.length - 1];
  if (last && time(r.time) <= time(last.time)) return results;
  const cutoff = now - hours * 3_600_000;
  let start = 0;
  while (start < results.length && time(results[start].time) < cutoff) start++;
  return [...results.slice(start), r];
}

/** Prepends a status event to a newest-first list, keeping at most `limit`. */
export function prependEvent(events: MonitorEvent[], e: MonitorEvent, limit: number): MonitorEvent[] {
  if (events.some((x) => x.id === e.id)) return events;
  return [e, ...events].slice(0, limit);
}
