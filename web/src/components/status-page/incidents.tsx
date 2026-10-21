// How posted incidents and notices look, on the status page and in the
// dashboard. Colors and labels mirror the hosted Uptimy status pages.
import { useState, type ReactNode } from "react";
import { AlertCircle, AlertTriangle, CheckCircle2, ChevronDown, Clock, Eye, MessageSquare, Zap } from "lucide-react";
import type { IncidentSeverity, IncidentStatus, PublicIncident } from "@/lib/api";
import { cn, formatDateTime, recentAgo } from "@/lib/utils";

export const statusTheme: Record<
  IncidentStatus,
  { label: string; icon: typeof AlertTriangle; rail: string; dot: string; chip: string; text: string }
> = {
  investigating: {
    label: "Investigating",
    icon: AlertTriangle,
    rail: "bg-down",
    dot: "bg-down",
    chip: "bg-down/10 text-red-700 ring-down/20 dark:text-red-300",
    text: "text-red-600 dark:text-red-400",
  },
  identified: {
    label: "Identified",
    icon: AlertCircle,
    rail: "bg-degraded",
    dot: "bg-degraded",
    chip: "bg-degraded/10 text-orange-700 ring-degraded/20 dark:text-orange-300",
    text: "text-orange-600 dark:text-orange-400",
  },
  monitoring: {
    label: "Monitoring",
    icon: Eye,
    rail: "bg-sky-500",
    dot: "bg-sky-500",
    chip: "bg-sky-500/10 text-sky-700 ring-sky-500/20 dark:text-sky-300",
    text: "text-sky-600 dark:text-sky-400",
  },
  resolved: {
    label: "Resolved",
    icon: CheckCircle2,
    rail: "bg-up",
    dot: "bg-up",
    chip: "bg-up/10 text-emerald-700 ring-up/20 dark:text-emerald-300",
    text: "text-emerald-600 dark:text-emerald-400",
  },
};

export const severityTheme: Record<IncidentSeverity, { label: string; dot: string }> = {
  critical: { label: "Critical", dot: "bg-down" },
  high: { label: "High", dot: "bg-orange-500" },
  medium: { label: "Medium", dot: "bg-degraded" },
  low: { label: "Low", dot: "bg-sky-500" },
};

export const STATUSES: IncidentStatus[] = ["investigating", "identified", "monitoring", "resolved"];
export const SEVERITIES: IncidentSeverity[] = ["low", "medium", "high", "critical"];

/** "2h 15m": how long it lasted, or has lasted so far. */
export function incidentDuration(from: string, to?: string | null) {
  const mins = Math.max(0, Math.floor(((to ? new Date(to) : new Date()).getTime() - new Date(from).getTime()) / 60000));
  const d = Math.floor(mins / 1440);
  const h = Math.floor((mins % 1440) / 60);
  const m = mins % 60;
  if (d > 0) return `${d}d ${h}h`;
  if (h > 0) return `${h}h ${m}m`;
  return `${m}m`;
}

export function StatusChip({ status }: { status: IncidentStatus }) {
  const t = statusTheme[status];
  const Icon = t.icon;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset",
        t.chip,
      )}
    >
      <Icon className="size-3" />
      {t.label}
    </span>
  );
}

export function SeverityChip({ severity }: { severity: IncidentSeverity }) {
  const t = severityTheme[severity];
  return (
    <span className="inline-flex items-center gap-1.5 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
      <span className={cn("size-1.5 rounded-full", t.dot)} />
      {t.label}
    </span>
  );
}

/** The timeline, newest first: each update with its status and time. */
export function Timeline({
  updates,
  actions,
}: {
  updates: { status?: IncidentStatus | ""; message: string; time: string }[];
  /** Per-update controls, for the dashboard. */
  actions?: (index: number) => ReactNode;
}) {
  return (
    <ol className="relative flex flex-col gap-4 border-l pl-5">
      {updates.map((u, i) => (
        <li key={i} className="relative">
          <span
            className={cn(
              "absolute top-1.5 -left-[25px] size-2.5 rounded-full ring-4 ring-card",
              u.status ? statusTheme[u.status].dot : "bg-primary",
            )}
          />
          <div className="flex flex-wrap items-baseline gap-x-2 gap-y-0.5">
            {u.status && (
              <span className={cn("text-sm font-semibold", statusTheme[u.status].text)}>
                {statusTheme[u.status].label}
              </span>
            )}
            <time className="text-xs text-muted-foreground tabular-nums" dateTime={u.time}>
              {formatDateTime(u.time)}
            </time>
            {actions && <span className="ml-auto">{actions(i)}</span>}
          </div>
          <p className="mt-1 text-sm leading-relaxed whitespace-pre-line break-words">{u.message}</p>
        </li>
      ))}
    </ol>
  );
}

/**
 * An incident on the public page, as on the hosted Uptimy status pages: a
 * summary that expands into the timeline. Open incidents start expanded.
 */
export function IncidentCard({ incident }: { incident: PublicIncident }) {
  const resolved = !!incident.resolved_at;
  const [expanded, setExpanded] = useState(!resolved);
  const status = (incident.status || "investigating") as IncidentStatus;
  const theme = statusTheme[status];
  const latest = incident.updates[0];
  const count = incident.updates.length;
  const duration = incidentDuration(incident.created_at, incident.resolved_at);
  const when = formatDateTime(resolved ? incident.resolved_at! : incident.created_at);

  return (
    // A row in a framed list (StatusList); the edge marks open incidents only.
    <div className="relative">
      {!resolved && <span className={cn("absolute inset-y-0 left-0 w-[3px]", theme.rail)} />}
      <button
        type="button"
        onClick={() => setExpanded((v) => !v)}
        aria-expanded={expanded}
        className="relative flex w-full items-start justify-between gap-3 px-4 py-4 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset sm:gap-4 sm:px-5 sm:py-5"
      >
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2 pr-6 sm:pr-0">
            <span className="relative flex size-2.5 shrink-0">
              {!resolved && (
                <span
                  className={cn(
                    "absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:hidden",
                    theme.dot,
                  )}
                />
              )}
              <span className={cn("relative inline-flex size-2.5 rounded-full", theme.dot)} />
            </span>
            <StatusChip status={status} />
            {incident.severity && <SeverityChip severity={incident.severity} />}
          </div>
          <h3 className="mt-2.5 text-sm leading-snug font-semibold text-foreground sm:text-[15px]">{incident.title}</h3>
          {/* Phones: duration and date here; wider screens have the right rail. */}
          <div className="mt-1.5 flex flex-wrap items-center gap-x-1.5 text-xs text-muted-foreground sm:hidden">
            <span className="font-semibold text-foreground tabular-nums">{duration}</span>
            <span>·</span>
            <span className="tabular-nums">{when}</span>
          </div>
          {latest && (
            <p
              className={cn(
                "mt-2 text-[13px] leading-relaxed whitespace-pre-wrap text-muted-foreground",
                !expanded && "line-clamp-2",
              )}
            >
              {latest.message}
            </p>
          )}
          {!expanded && (
            <p className="mt-2 hidden truncate text-xs text-muted-foreground sm:block">
              {resolved ? `Resolved ${when} · Duration ${duration}` : `Started ${when} · ${duration} and counting`}
              {` · ${count} ${count === 1 ? "update" : "updates"}`}
            </p>
          )}
          {incident.monitors.length > 0 && (
            <p className="mt-2 text-xs text-muted-foreground">Affects: {incident.monitors.join(", ")}</p>
          )}
        </div>
        <div className="hidden shrink-0 items-start gap-2.5 sm:flex sm:gap-3">
          <div className="text-right">
            <div className="text-sm font-semibold text-foreground tabular-nums sm:text-base">{duration}</div>
            <div className="text-xs text-muted-foreground">{when}</div>
          </div>
          <ChevronDown
            className={cn(
              "mt-0.5 size-4 shrink-0 text-muted-foreground transition-transform duration-200",
              expanded && "rotate-180",
            )}
          />
        </div>
        <ChevronDown
          className={cn(
            "absolute top-4 right-4 size-4 text-muted-foreground transition-transform duration-200 sm:hidden",
            expanded && "rotate-180",
          )}
        />
      </button>

      {expanded && (
        <div className="space-y-4 border-t border-border/70 px-4 pt-4 pb-4 sm:px-5 sm:pb-5">
          <PublicTimeline incident={incident} />
        </div>
      )}
    </div>
  );
}

/** Oldest first, from when the incident started, like the hosted timeline. */
function PublicTimeline({ incident }: { incident: PublicIncident }) {
  const updates = [...incident.updates].reverse();
  return (
    <div>
      <div className="mb-4 flex items-center gap-2">
        <Clock className="size-3.5 text-muted-foreground" />
        <h4 className="text-xs font-medium tracking-wider text-muted-foreground uppercase">Timeline</h4>
      </div>
      <div className="relative pl-8">
        <div className="absolute top-3 bottom-3 left-[11px] w-px bg-border" />
        <div className="space-y-5">
          <div className="relative">
            <div className="absolute top-0 -left-8 z-10 flex size-6 items-center justify-center rounded-full border border-red-500/30 bg-red-500/10">
              <Zap className="size-3 text-red-500" />
            </div>
            <div className="flex min-h-6 flex-col gap-0.5 sm:flex-row sm:items-baseline sm:justify-between">
              <span className="text-sm font-medium text-red-600 dark:text-red-400">Incident started</span>
              <TimelineTime iso={incident.created_at} />
            </div>
          </div>
          {updates.map((u, i) => {
            const theme = u.status ? statusTheme[u.status] : null;
            return (
              <div key={i} className="relative">
                <div
                  className={cn(
                    "absolute top-0 -left-8 z-10 flex size-6 items-center justify-center rounded-full border",
                    u.status ? updateIcon[u.status] : "border-blue-500/30 bg-blue-500/10",
                  )}
                >
                  <MessageSquare className={cn("size-3", theme ? theme.text : "text-blue-500")} />
                </div>
                <div className="min-h-6">
                  <div className="flex flex-col gap-0.5 sm:flex-row sm:items-baseline sm:justify-between">
                    <span
                      className={cn("text-sm font-medium", theme ? theme.text : "text-blue-600 dark:text-blue-400")}
                    >
                      {theme ? theme.label : "Update"}
                    </span>
                    <TimelineTime iso={u.time} />
                  </div>
                  <p className="mt-1 text-sm leading-relaxed whitespace-pre-wrap text-muted-foreground">{u.message}</p>
                </div>
              </div>
            );
          })}
          {incident.resolved_at && (
            <div className="relative">
              <div className="absolute top-0 -left-8 z-10 flex size-6 items-center justify-center rounded-full border border-emerald-500/30 bg-emerald-500/10">
                <CheckCircle2 className="size-3 text-up" />
              </div>
              <div className="flex min-h-6 flex-col gap-0.5 sm:flex-row sm:items-baseline sm:justify-between">
                <span className="text-sm font-medium text-emerald-700 dark:text-emerald-400">Resolved</span>
                <TimelineTime iso={incident.resolved_at} />
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}

/** The circle behind each update's icon, in its status's color. */
const updateIcon: Record<IncidentStatus, string> = {
  investigating: "border-red-500/30 bg-red-500/10",
  identified: "border-orange-500/30 bg-orange-500/10",
  monitoring: "border-sky-500/30 bg-sky-500/10",
  resolved: "border-emerald-500/30 bg-emerald-500/10",
};

function TimelineTime({ iso }: { iso: string }) {
  return (
    <span className="shrink-0 text-xs text-muted-foreground tabular-nums">
      {formatDateTime(iso)}
      {recentAgo(iso) && <span className="ml-1 opacity-60">· {recentAgo(iso)}</span>}
    </span>
  );
}
