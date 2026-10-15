import { Link } from "react-router";
import type { HeartbeatSummary } from "@/lib/api";
import { cn, formatUptime, timeAgo } from "@/lib/utils";
import { monitorPath } from "@/lib/monitors";
import { HeartbeatDot, RunBars } from "@/components/status";
import { AcknowledgedBadge, ManagedIcon, MaintenanceBadge } from "@/components/MonitorEmpty";

/** Rows of heartbeats, as on the dashboard and the Heartbeats page. */
export function HeartbeatList({ heartbeats }: { heartbeats: HeartbeatSummary[] }) {
  return (
    <div className="divide-y overflow-hidden rounded-lg border">
      {heartbeats.map((h) => (
        <HeartbeatRow key={h.id} h={h} />
      ))}
    </div>
  );
}

function HeartbeatRow({ h }: { h: HeartbeatSummary }) {
  const { state } = h.tracking;
  return (
    <Link
      to={monitorPath(h)}
      className={cn(
        "grid grid-cols-[auto_1fr_auto] items-center gap-x-4 gap-y-2 px-4 py-3.5 transition-colors hover:bg-nav-hover md:grid-cols-[auto_minmax(0,1.2fr)_minmax(0,1fr)_5rem_6rem]",
        h.paused && "opacity-60",
      )}
    >
      <HeartbeatDot state={state} />
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium">{h.name}</span>
          {h.in_maintenance && <MaintenanceBadge />}
          {h.status === "down" && h.acknowledged && <AcknowledgedBadge ack={h.acknowledged} />}
          <ManagedIcon source={h.source} />
        </div>
        <div className="truncate text-xs text-muted-foreground">
          {h.schedule} · {h.last_run?.finished_at ? `last run ${timeAgo(h.last_run.finished_at)}` : "no run yet"}
        </div>
      </div>
      <div className="col-span-3 col-start-1 md:col-span-1 md:col-start-auto">
        <RunBars runs={h.recent} />
      </div>
      <div className="hidden text-right text-sm tabular-nums md:block">
        {formatUptime(h.stats_30d.ratio)}
        <div className="text-xs text-muted-foreground">on time</div>
      </div>
      <div className="hidden text-right text-sm md:block">
        <NextRun tracking={h.tracking} />
      </div>
    </Link>
  );
}

/** When the next run is due, or what's happening instead. */
function NextRun({ tracking }: { tracking: HeartbeatSummary["tracking"] }) {
  if (tracking.state === "paused" || !tracking.due_at) return <span className="text-muted-foreground">Paused</span>;
  if (tracking.running_since) return <span className="font-medium text-sky-600 dark:text-sky-400">Running</span>;
  if (tracking.state === "late") return <span className="font-medium text-paused">Late</span>;
  return (
    <>
      <span className="tabular-nums">{timeAgo(tracking.due_at)}</span>
      <div className="text-xs text-muted-foreground">next run</div>
    </>
  );
}
