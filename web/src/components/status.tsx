import type { Daily, HeartbeatState, Result, Run, Status } from "@/lib/api";
import { cn, formatDateTime, formatDuration, formatLatency, formatUptime } from "@/lib/utils";
import { heartbeatStates } from "@/lib/monitors";
import { tip, type TipTone } from "@/components/ui/tip-layer";

const dotColor: Record<Status, string> = {
  up: "bg-up",
  down: "bg-down",
  pending: "bg-pending",
  paused: "bg-paused",
};

export function StatusDot({ status, className }: { status: Status; className?: string }) {
  return (
    <span className={cn("relative inline-flex size-2.5 shrink-0", className)}>
      {status === "down" && (
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-down opacity-60 motion-reduce:hidden" />
      )}
      <span className={cn("relative inline-flex size-2.5 rounded-full", dotColor[status])} />
    </span>
  );
}

// The status words of the Uptimy style guide: Healthy, Down, Paused, Waiting.
const labels: Record<Status, string> = { up: "Healthy", down: "Down", pending: "Waiting", paused: "Paused" };

export function StatusLabel({ status }: { status: Status }) {
  return (
    <span className="inline-flex items-center gap-2 text-sm font-medium">
      <StatusDot status={status} />
      {labels[status]}
    </span>
  );
}

/** Recent check results as thin bars, newest on the right. */
export function ResultBars({ results, slots = 40 }: { results: Result[]; slots?: number }) {
  const padded: (Result | null)[] = [
    ...Array(Math.max(0, slots - results.length)).fill(null),
    ...results.slice(-slots),
  ];
  return (
    <div className="flex h-7 items-stretch gap-[2px]" role="img" aria-label="Recent checks">
      {padded.map((r, i) => (
        <span
          key={i}
          {...(r ? resultTip(r) : { "data-tip": "No data yet" })}
          className={cn(
            "w-1.5 min-w-[3px] flex-1 rounded-sm transition hover:brightness-110",
            r == null ? "bg-muted" : r.ok ? "bg-up" : "bg-down",
          )}
        />
      ))}
    </div>
  );
}

const resultTip = (r: Result) =>
  tip({
    title: formatDateTime(r.time),
    value: r.ok ? "OK" : "Failed",
    tone: r.ok ? "up" : "down",
    body: r.message || undefined,
    meta: r.latency_ms > 0 ? formatLatency(r.latency_ms) : undefined,
  });

// A day is coloured by how long it was down, like Atlassian Statuspage and the
// hosted Uptimy status pages: under a minute green, yellow up to 20 minutes,
// orange up to an hour, red beyond. Minutes are estimated from the day's
// ratio (share of 1,440). Uptime figures keep the 99% / 95% thresholds.
const downMinutes = (ratio: number) => (1 - ratio) * 1440;

const dayTone = (ratio: number): TipTone => {
  const m = downMinutes(ratio);
  return m < 1 ? "up" : m < 20 ? "minor" : m < 60 ? "degraded" : "down";
};

// A heartbeat's day counts runs, not minutes: every run on time is green, one
// late or missed yellow, two or three orange, more red.
const runsOff = (d: Daily) => Math.round((1 - (d.ratio ?? 1)) * d.count);

const runDayTone = (d: Daily): TipTone => {
  const n = runsOff(d);
  return n === 0 ? "up" : n === 1 ? "minor" : n <= 3 ? "degraded" : "down";
};

const toneOf = (d: Daily, measure: "uptime" | "on time"): TipTone =>
  !d.count ? "muted" : measure === "on time" ? runDayTone(d) : dayTone(d.ratio ?? 0);

const dayBar: Record<TipTone, string> = {
  up: "bg-up",
  minor: "bg-minor",
  degraded: "bg-degraded",
  down: "bg-down",
  info: "bg-sky-500",
  muted: "bg-no-data",
};

/** "≈ 12 min down", "2 runs not on time"; nothing for a clean day. */
function dayText(d: Daily, measure: "uptime" | "on time") {
  if (measure === "on time") {
    const n = runsOff(d);
    return n ? `${n} run${n === 1 ? "" : "s"} not on time` : undefined;
  }
  const m = downMinutes(d.ratio ?? 1);
  if (m < 1) return undefined;
  return m < 90 ? `≈ ${Math.round(m)} min down` : `≈ ${Math.round(m / 60)} h down`;
}

/**
 * One bar per day, oldest on the left, as on the hosted status pages. The
 * ratio is a healthcheck's uptime or the share of a heartbeat's runs that
 * were on time; measure names it in the tooltip.
 */
export function DailyBars({
  days,
  measure = "uptime",
  barClassName = "h-8 sm:h-10",
}: {
  days: Daily[];
  measure?: "uptime" | "on time";
  /** Bar height; the status page uses shorter bars than the monitor pages. */
  barClassName?: string;
}) {
  return (
    <div
      className="grid gap-0.5"
      style={{ gridTemplateColumns: `repeat(${days.length}, minmax(0, 1fr))` }}
      role="img"
      aria-label={`${days.length} days, ${measure}`}
    >
      {days.map((d) => (
        <span
          key={d.date}
          {...dayTip(d, measure)}
          className={cn("rounded-[3px] transition hover:brightness-110", barClassName, dayBar[toneOf(d, measure)])}
        />
      ))}
    </div>
  );
}

const dayTip = (d: Daily, measure: "uptime" | "on time") =>
  tip({
    title: new Date(d.date + "T00:00:00Z").toLocaleDateString(undefined, {
      weekday: "short",
      month: "short",
      day: "numeric",
      timeZone: "UTC",
    }),
    value: d.count ? formatUptime(d.ratio) : undefined,
    tone: d.count ? toneOf(d, measure) : undefined,
    body: d.count ? dayText(d, measure) : undefined,
    meta: d.count
      ? measure === "on time"
        ? `On time · ${d.count} run${d.count === 1 ? "" : "s"}`
        : "Uptime"
      : "No data yet",
  });

const stateDot = {
  up: "bg-up",
  down: "bg-down",
  late: "bg-paused",
  muted: "bg-pending",
};

/** A heartbeat's state as a dot, colored like a status. */
export function HeartbeatDot({ state, className }: { state: HeartbeatState; className?: string }) {
  const tone = heartbeatStates[state].tone;
  return (
    <span className={cn("relative inline-flex size-2.5 shrink-0", className)}>
      {tone === "down" && (
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-down opacity-60 motion-reduce:hidden" />
      )}
      <span className={cn("relative inline-flex size-2.5 rounded-full", stateDot[tone])} />
    </span>
  );
}

export function HeartbeatStateLabel({ state }: { state: HeartbeatState }) {
  return (
    <span className="inline-flex items-center gap-2 text-sm font-medium">
      <HeartbeatDot state={state} />
      {heartbeatStates[state].label}
    </span>
  );
}

/** How a run went, for colors and tooltips. */
export function runOutcome(r: Run): { label: string; color: string } {
  switch (r.outcome) {
    case "running":
      return { label: "Running", color: "bg-sky-500" };
    case "missed":
      return { label: "Missed", color: "bg-down/60" };
    case "failure":
      return { label: "Failed", color: "bg-down" };
    default:
      return r.on_time ? { label: "On time", color: "bg-up" } : { label: "Late", color: "bg-degraded" };
  }
}

/** When a run happened: finished, started (running) or was due (missed). */
export const runTime = (r: Run) => r.finished_at ?? r.started_at ?? r.due_at ?? "";

/** Recent heartbeat runs as bars, newest on the right. */
export function RunBars({ runs, slots = 30 }: { runs: Run[]; slots?: number }) {
  const padded: (Run | null)[] = [...Array(Math.max(0, slots - runs.length)).fill(null), ...runs.slice(-slots)];
  return (
    <div className="flex h-7 items-stretch gap-[2px]" role="img" aria-label="Recent runs">
      {padded.map((r, i) => (
        <span
          key={r?.id ?? `empty-${i}`}
          {...(r ? runTip(r) : { "data-tip": "No run yet" })}
          className={cn(
            "w-1.5 min-w-[3px] flex-1 rounded-sm transition hover:brightness-110",
            r == null ? "bg-muted" : runOutcome(r).color,
            r?.outcome === "running" && "animate-pulse motion-reduce:animate-none",
          )}
        />
      ))}
    </div>
  );
}

const runTone: Record<Run["outcome"], TipTone> = { success: "up", running: "info", missed: "down", failure: "down" };

const runTip = (r: Run) =>
  tip({
    title: formatDateTime(runTime(r)),
    value: runOutcome(r).label,
    tone: r.outcome === "success" && !r.on_time ? "degraded" : runTone[r.outcome],
    body: r.message || undefined,
    meta: r.duration_ms != null ? `took ${formatDuration(r.duration_ms)}` : undefined,
  });
