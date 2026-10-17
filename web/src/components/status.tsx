import type { Daily, HeartbeatState, Result, Run, Status } from "@/lib/api";
import { cn, formatDateTime, formatDuration, formatLatency, formatUptime } from "@/lib/utils";
import { heartbeatStates } from "@/lib/monitors";
import { useTooltip } from "@/components/ui/tooltip";

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

const labels: Record<Status, string> = { up: "Up", down: "Down", pending: "Pending", paused: "Paused" };

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
  const { bind, tip } = useTooltip();
  const padded: (Result | null)[] = [
    ...Array(Math.max(0, slots - results.length)).fill(null),
    ...results.slice(-slots),
  ];
  return (
    <div className="flex h-7 items-stretch gap-[2px]" role="img" aria-label="Recent checks">
      {padded.map((r, i) => (
        <span
          key={i}
          {...bind(r ? <ResultTip r={r} /> : "No data yet")}
          className={cn(
            "w-1.5 min-w-[3px] flex-1 rounded-sm transition hover:brightness-110",
            r == null ? "bg-muted" : r.ok ? "bg-up" : "bg-down",
          )}
        />
      ))}
      {tip}
    </div>
  );
}

function ResultTip({ r }: { r: Result }) {
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-neutral-400">{formatDateTime(r.time)}</span>
      <span className="flex items-center gap-1.5 font-medium">
        <span className={cn("size-2 rounded-full", r.ok ? "bg-up" : "bg-down")} />
        {r.ok ? "OK" : "Failed"}
        {r.latency_ms > 0 && <span className="font-normal text-neutral-400">· {formatLatency(r.latency_ms)}</span>}
      </span>
      {r.message && <span className="max-w-64 truncate text-neutral-300">{r.message}</span>}
    </div>
  );
}

/** Bar colour for a day's uptime, using the hosted status page thresholds. */
function dailyColor(ratio: number | null) {
  if (ratio == null) return "bg-no-data";
  const pct = ratio * 100;
  if (pct >= 99.5) return "bg-up";
  if (pct >= 99) return "bg-up-soft";
  if (pct >= 95) return "bg-degraded";
  return "bg-down";
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
  const { bind, tip } = useTooltip();
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
          {...bind(<DayTip d={d} measure={measure} />)}
          className={cn(
            "rounded-[3px] transition hover:brightness-110",
            barClassName,
            dailyColor(d.count ? d.ratio : null),
          )}
        />
      ))}
      {tip}
    </div>
  );
}

function DayTip({ d, measure }: { d: Daily; measure: "uptime" | "on time" }) {
  const date = new Date(d.date + "T00:00:00Z").toLocaleDateString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
    timeZone: "UTC",
  });
  // Laid out like the hosted tooltip: the day, the figure in its colour, a footnote.
  return (
    <div className="min-w-36">
      <div className="flex items-baseline justify-between gap-4">
        <span className="font-semibold">{date}</span>
        {d.count ? (
          <span className={cn("flex items-center gap-1.5 text-sm font-semibold tabular-nums", tipTone(d.ratio ?? 0))}>
            <span className="size-1.5 rounded-full bg-current" />
            {formatUptime(d.ratio)}
          </span>
        ) : null}
      </div>
      <div className="mt-1 text-[11px] text-neutral-400">
        {d.count
          ? measure === "on time"
            ? `On time · ${d.count} run${d.count === 1 ? "" : "s"}`
            : "Uptime"
          : "No data"}
      </div>
    </div>
  );
}

const tipTone = (ratio: number) =>
  ratio >= 0.99 ? "text-emerald-400" : ratio >= 0.95 ? "text-orange-400" : "text-red-400";

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
  const { bind, tip } = useTooltip();
  const padded: (Run | null)[] = [...Array(Math.max(0, slots - runs.length)).fill(null), ...runs.slice(-slots)];
  return (
    <div className="flex h-7 items-stretch gap-[2px]" role="img" aria-label="Recent runs">
      {padded.map((r, i) => (
        <span
          key={r?.id ?? `empty-${i}`}
          {...bind(r ? <RunTip r={r} /> : "No run yet")}
          className={cn(
            "w-1.5 min-w-[3px] flex-1 rounded-sm transition hover:brightness-110",
            r == null ? "bg-muted" : runOutcome(r).color,
            r?.outcome === "running" && "animate-pulse motion-reduce:animate-none",
          )}
        />
      ))}
      {tip}
    </div>
  );
}

function RunTip({ r }: { r: Run }) {
  const { label, color } = runOutcome(r);
  return (
    <div className="flex flex-col gap-0.5">
      <span className="text-neutral-400">{formatDateTime(runTime(r))}</span>
      <span className="flex items-center gap-1.5 font-medium">
        <span className={cn("size-2 rounded-full", color)} />
        {label}
        {r.duration_ms != null && (
          <span className="font-normal text-neutral-400">· took {formatDuration(r.duration_ms)}</span>
        )}
      </span>
      {r.message && <span className="max-w-64 truncate text-neutral-300">{r.message}</span>}
    </div>
  );
}
