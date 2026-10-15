import { Link } from "react-router";
import type { HealthcheckSummary } from "@/lib/api";
import { cn, formatLatency, formatUptime } from "@/lib/utils";
import { useCheckTypeLabel } from "@/lib/types";
import { monitorPath } from "@/lib/monitors";
import { Badge } from "@/components/ui/badge";
import { ResultBars, StatusDot } from "@/components/status";
import { AcknowledgedBadge, ManagedIcon, MaintenanceBadge } from "@/components/MonitorEmpty";

/** Rows of healthchecks, as on the dashboard and the Healthchecks page. */
export function HealthcheckList({ healthchecks }: { healthchecks: HealthcheckSummary[] }) {
  return (
    <div className="divide-y overflow-hidden rounded-lg border">
      {healthchecks.map((h) => (
        <HealthcheckRow key={h.id} h={h} />
      ))}
    </div>
  );
}

function HealthcheckRow({ h }: { h: HealthcheckSummary }) {
  const typeLabel = useCheckTypeLabel();
  return (
    <Link
      to={monitorPath(h)}
      className={cn(
        "grid grid-cols-[auto_1fr_auto] items-center gap-x-4 gap-y-2 px-4 py-3.5 transition-colors hover:bg-nav-hover md:grid-cols-[auto_minmax(0,1.2fr)_minmax(0,1fr)_5rem_4.5rem]",
        h.paused && "opacity-60",
      )}
    >
      <StatusDot status={h.status} />
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate font-medium">{h.name}</span>
          <Badge>{typeLabel(h.check.type)}</Badge>
          {h.in_maintenance && <MaintenanceBadge />}
          {h.status === "down" && h.acknowledged && <AcknowledgedBadge ack={h.acknowledged} />}
          <ManagedIcon source={h.source} />
        </div>
        <div className="truncate text-xs text-muted-foreground">{h.target}</div>
      </div>
      <div className="col-span-3 col-start-1 md:col-span-1 md:col-start-auto">
        <ResultBars results={h.recent} />
      </div>
      <div className="hidden text-right text-sm tabular-nums md:block">
        {formatUptime(h.uptime_24h)}
        <div className="text-xs text-muted-foreground">24h</div>
      </div>
      <div className="hidden text-right text-sm tabular-nums md:block">
        {formatLatency(h.avg_latency_24h)}
        <div className="text-xs text-muted-foreground">avg</div>
      </div>
    </Link>
  );
}
