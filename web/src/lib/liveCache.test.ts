import { describe, expect, it } from "vitest";
import type { HealthcheckSummary, MonitorEvent, Result } from "./api";
import { appendResult, applyResult, patchList, prependEvent, RECENT_SLOTS } from "./liveCache";

const at = (s: number) => new Date(Date.UTC(2026, 8, 30, 12, 0, s)).toISOString();
const result = (s: number, ok = true): Result => ({ monitor_id: 1, time: at(s), ok, latency_ms: 50, message: "" });
const event = (id: number, status: MonitorEvent["status"]): MonitorEvent => ({
  id,
  monitor_id: 1,
  time: at(id),
  status,
  message: "",
});

function summary(over: Partial<HealthcheckSummary> = {}): HealthcheckSummary {
  return {
    id: 1,
    kind: "healthcheck",
    in_maintenance: false,
    acknowledged: null,
    name: "api",
    check: {
      type: "http",
      target: "https://x.test",
      interval_seconds: 30,
      timeout_seconds: 10,
      failure_threshold: 2,
      recovery_threshold: 1,
      config: {},
    },
    target: "https://x.test",
    paused: false,
    public: true,
    status_label: "",
    status_order: 0,
    status_section: "",
    source: "ui",
    created_at: at(0),
    updated_at: at(0),
    status: "up",
    last_result: null,
    recent: [],
    uptime_24h: 1,
    avg_latency_24h: 50,
    ...over,
  };
}

describe("applyResult", () => {
  it("appends to the bars and sets the last result", () => {
    const m = applyResult(summary(), result(1, false));
    expect(m.recent).toHaveLength(1);
    expect(m.last_result?.ok).toBe(false);
  });

  it("keeps only the most recent bars", () => {
    const full = summary({
      recent: Array.from({ length: RECENT_SLOTS }, (_, i) => result(i)),
      last_result: result(RECENT_SLOTS - 1),
    });
    const m = applyResult(full, result(100));
    expect(m.recent).toHaveLength(RECENT_SLOTS);
    expect(m.recent.at(-1)?.time).toBe(at(100));
  });

  it("ignores duplicates and out-of-order results", () => {
    const m = summary({ recent: [result(5)], last_result: result(5) });
    expect(applyResult(m, result(5))).toBe(m);
    expect(applyResult(m, result(3))).toBe(m);
  });
});

describe("patchList", () => {
  it("updates only the matching monitor's status", () => {
    const other = summary({ id: 2 });
    const list = [summary(), other];
    const next = patchList(list, { type: "status", monitor_id: 1, data: event(9, "down") })!;
    expect(next[0].status).toBe("down");
    expect(next[1]).toBe(other);
  });

  it("asks for a refetch when the monitor is unknown", () => {
    expect(patchList([summary()], { type: "result", monitor_id: 99, data: result(1) })).toBeNull();
  });
});

describe("appendResult", () => {
  it("adds the point and drops ones outside the window", () => {
    const now = new Date(at(3600 + 10)).getTime();
    const series = [result(0), result(20), result(3600)];
    const next = appendResult(series, result(3600 + 10), 1, now);
    expect(next.map((r) => r.time)).toEqual([at(20), at(3600), at(3610)]);
  });

  it("ignores points it already has", () => {
    const series = [result(10)];
    expect(appendResult(series, result(10), 24)).toBe(series);
  });
});

describe("prependEvent", () => {
  it("adds newest first, dedupes and caps the list", () => {
    const list = [event(2, "up"), event(1, "down")];
    expect(prependEvent(list, event(2, "up"), 10)).toBe(list);
    expect(prependEvent(list, event(3, "down"), 2).map((e) => e.id)).toEqual([3, 2]);
  });
});
