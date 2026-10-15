import { useEffect } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import type { HealthcheckDetail, HealthcheckSummary, HeartbeatSummary, MonitorEvent, Result } from "./api";
import { appendResult, applyResult, applyStatus, patchList, prependEvent, type LiveMessage } from "./liveCache";

/**
 * Uptime percentages, on-time rates and averages can't be derived from a
 * single message, so they're refetched on this slower cadence, and only if
 * something changed. Everything else updates straight from the stream.
 */
const AGGREGATE_REFRESH_MS = 60_000;

/**
 * Subscribes to the agent's Server-Sent Events stream. Healthcheck results,
 * which arrive constantly, are applied to the cached data directly: they
 * extend the bars and charts without a request per check, so load stays flat
 * as monitors and open tabs grow. Heartbeat changes (a run, a late or missed
 * one) are rare, so they simply refetch what's on screen.
 */
export function useLiveUpdates(enabled: boolean) {
  const qc = useQueryClient();

  useEffect(() => {
    if (!enabled) return;
    const es = new EventSource("/api/events");
    let connectedBefore = false;
    let aggregatesStale = false;
    const timers = new Map<string, number>();

    // Coalesce bursts (several monitors created at once, a run and the
    // status change it causes) into one refetch.
    const soon = (key: string, fn: () => void) => {
      window.clearTimeout(timers.get(key));
      timers.set(key, window.setTimeout(fn, 300));
    };
    const reloadHealthchecks = () =>
      soon("healthchecks", () => qc.invalidateQueries({ queryKey: ["healthchecks"], exact: true }));
    const reloadHeartbeat = (id: number) =>
      soon(`heartbeat-${id}`, () => {
        qc.invalidateQueries({ queryKey: ["heartbeats"], exact: true });
        qc.invalidateQueries({ queryKey: ["heartbeat", id] });
      });

    es.onmessage = (e) => {
      let msg: LiveMessage;
      try {
        msg = JSON.parse(e.data);
      } catch {
        return; // ignore malformed frames
      }
      const id = msg.monitor_id;
      switch (msg.type) {
        case "monitors":
          reloadHealthchecks();
          reloadHeartbeat(id);
          qc.invalidateQueries({ queryKey: ["healthcheck", id] });
          qc.invalidateQueries({ queryKey: ["activity"] });
          qc.invalidateQueries({ queryKey: ["maintenance"] });
          return;
        case "run":
        case "heartbeat":
          reloadHeartbeat(id);
          return;
        case "status":
          aggregatesStale = true;
          applyStatusChange(qc, id, msg.data);
          qc.invalidateQueries({ queryKey: ["activity"] });
          if (isHeartbeat(qc, id)) reloadHeartbeat(id);
          else if (!applyToHealthchecks(qc, msg)) reloadHealthchecks();
          return;
        case "delivery":
          soon(`delivery-${id}`, () => {
            qc.invalidateQueries({ queryKey: ["events", id] });
            qc.invalidateQueries({ queryKey: ["notifiers"] });
          });
          return;
        case "result":
          aggregatesStale = true;
          if (!applyToHealthchecks(qc, msg)) reloadHealthchecks();
          applyResultToDetail(qc, id, msg.data);
          return;
      }
    };

    es.onopen = () => {
      // After a reconnect, messages may have been missed: resync once.
      if (connectedBefore) qc.invalidateQueries();
      connectedBefore = true;
    };

    const aggregates = window.setInterval(() => {
      if (!aggregatesStale) return;
      aggregatesStale = false;
      qc.invalidateQueries({ queryKey: ["healthchecks"], exact: true });
      qc.invalidateQueries({ queryKey: ["heartbeats"], exact: true });
      // Detail headers (uptime 24h/7d/30d), not their chart series or events.
      qc.invalidateQueries({ predicate: (q) => q.queryKey[0] === "healthcheck" && q.queryKey.length === 2 });
    }, AGGREGATE_REFRESH_MS);

    return () => {
      timers.forEach((t) => window.clearTimeout(t));
      window.clearInterval(aggregates);
      es.close();
    };
  }, [enabled, qc]);
}

function isHeartbeat(qc: QueryClient, id: number) {
  return qc.getQueryData<HeartbeatSummary[]>(["heartbeats"])?.some((h) => h.id === id) ?? false;
}

/** Patches the healthcheck list. Returns false if it needs a refetch. */
function applyToHealthchecks(qc: QueryClient, msg: LiveMessage): boolean {
  let known = true;
  qc.setQueryData<HealthcheckSummary[]>(["healthchecks"], (list) => {
    if (!list) return list;
    const next = patchList(list, msg);
    if (next === null) {
      known = false;
      return list;
    }
    return next;
  });
  return known;
}

/** Updates an open page's status and event list for a status change. */
function applyStatusChange(qc: QueryClient, id: number, e: MonitorEvent) {
  qc.setQueryData<HealthcheckDetail>(
    ["healthcheck", id],
    (d) => d && { ...d, healthcheck: applyStatus(d.healthcheck, e) },
  );
  qc.setQueryData<MonitorEvent[]>(["events", id], (events) => events && prependEvent(events, e, 50));
}

/** Extends an open healthcheck page's bars and chart with a result. */
function applyResultToDetail(qc: QueryClient, id: number, r: Result) {
  qc.setQueryData<HealthcheckDetail>(
    ["healthcheck", id],
    (d) => d && { ...d, healthcheck: applyResult(d.healthcheck, r) },
  );
  for (const [key, series] of qc.getQueriesData<Result[]>({ queryKey: ["healthcheck", id, "results"] })) {
    if (series) qc.setQueryData(key, appendResult(series, r, Number(key[3])));
  }
}
