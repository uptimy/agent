// Planned work on the public page, as on the hosted Uptimy status pages.
import { Calendar, CalendarCheck, CalendarClock, CheckCircle2, CircleDot, Clock } from "lucide-react";
import type { PublicMaintenance } from "@/lib/api";
import { cn, recentAgo } from "@/lib/utils";

const theme: Record<
  PublicMaintenance["state"],
  { label: string; icon: typeof Calendar; rail: string; dot: string; chip: string }
> = {
  scheduled: {
    label: "Scheduled",
    icon: Calendar,
    rail: "bg-sky-500",
    dot: "bg-sky-500",
    chip: "bg-sky-500/10 text-blue-700 ring-sky-500/20 dark:text-blue-300",
  },
  active: {
    label: "In progress",
    icon: CircleDot,
    rail: "bg-degraded",
    dot: "bg-degraded",
    chip: "bg-degraded/10 text-orange-700 ring-degraded/20 dark:text-orange-300",
  },
  ended: {
    label: "Completed",
    icon: CheckCircle2,
    rail: "bg-up",
    dot: "bg-up",
    chip: "bg-up/10 text-emerald-700 ring-up/20 dark:text-emerald-300",
  },
};

/** "Oct 10, 2026, 02:00 AM" in the viewer's time zone. */
function formatWhen(iso: string) {
  return new Date(iso).toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function length(m: PublicMaintenance) {
  const mins = Math.round((new Date(m.ends_at).getTime() - new Date(m.starts_at).getTime()) / 60000);
  const h = Math.floor(mins / 60);
  return h > 0 ? `${h}h ${mins % 60}m` : `${mins}m`;
}

function progress(m: PublicMaintenance) {
  const now = Date.now();
  const start = new Date(m.starts_at).getTime();
  const end = new Date(m.ends_at).getTime();
  if (now <= start) return 0;
  if (now >= end) return 100;
  return Math.round(((now - start) / (end - start)) * 100);
}

export function MaintenanceCard({ m }: { m: PublicMaintenance }) {
  const t = theme[m.state];
  const Icon = t.icon;
  const active = m.state === "active";
  const pct = progress(m);
  // Scheduled when it was created; started and completed as its window says.
  const timeline = [
    {
      label: "Scheduled",
      at: m.created_at,
      icon: CalendarClock,
      ring: "border-sky-500/30 bg-sky-500/10",
      color: "text-sky-500",
      text: "text-blue-600 dark:text-blue-400",
    },
    ...(m.state !== "scheduled"
      ? [
          {
            label: "In progress",
            at: m.starts_at,
            icon: CircleDot,
            ring: "border-degraded/30 bg-degraded/10",
            color: "text-degraded",
            text: "text-orange-600 dark:text-orange-400",
          },
        ]
      : []),
    ...(m.state === "ended"
      ? [
          {
            label: "Completed",
            at: m.ends_at,
            icon: CheckCircle2,
            ring: "border-up/30 bg-up/10",
            color: "text-up",
            text: "text-emerald-600 dark:text-emerald-400",
          },
        ]
      : []),
  ];

  return (
    // A row in a framed list (StatusList); the edge marks maintenance in progress only.
    <div className="relative">
      {active && <span className={cn("absolute inset-y-0 left-0 w-[3px]", t.rail)} />}
      <div className="space-y-4 px-4 py-4 sm:px-5 sm:py-5">
        <div className="flex flex-wrap items-center gap-2">
          <span className="relative flex size-2.5 shrink-0">
            {active && (
              <span
                className={cn(
                  "absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:hidden",
                  t.dot,
                )}
              />
            )}
            <span className={cn("relative inline-flex size-2.5 rounded-full", t.dot)} />
          </span>
          <span
            className={cn(
              "inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-xs font-medium ring-1 ring-inset",
              t.chip,
            )}
          >
            <Icon className="size-3" />
            {t.label}
          </span>
          <span className="inline-flex items-center gap-1 rounded-full bg-muted px-2 py-0.5 text-xs font-medium text-muted-foreground">
            <Clock className="size-3" />
            {length(m)}
          </span>
        </div>

        <div className="space-y-1.5">
          <h3 className="text-sm leading-snug font-semibold text-foreground sm:text-[15px]">{m.title}</h3>
          {m.description && (
            <p className="text-[13px] leading-relaxed whitespace-pre-wrap text-muted-foreground sm:text-sm">
              {m.description}
            </p>
          )}
        </div>

        <div className="border-t border-border/70 pt-4">
          <h4 className="mb-2.5 text-xs font-medium tracking-wide text-muted-foreground uppercase">
            Affected services
          </h4>
          <div className="flex flex-wrap gap-2">
            {(m.monitors.length ? m.monitors : ["All services"]).map((name) => (
              <span
                key={name}
                className="inline-flex items-center gap-1.5 rounded-full bg-degraded/10 px-2.5 py-1 text-xs font-medium text-orange-700 ring-1 ring-degraded/20 ring-inset dark:text-orange-300"
              >
                <span className="size-1.5 rounded-full bg-degraded" />
                {name}
              </span>
            ))}
          </div>
        </div>

        <dl className="grid grid-cols-1 gap-x-8 gap-y-3 border-t border-border/70 pt-4 sm:grid-cols-2 lg:grid-cols-3">
          <When icon={Calendar} label="Start time" at={m.starts_at} />
          <When icon={CalendarCheck} label="End time" at={m.ends_at} />
        </dl>

        {active && (
          <div>
            <div className="mb-1.5 flex items-center justify-between text-xs font-medium">
              <span className="text-muted-foreground">Maintenance in progress</span>
              <span className="text-orange-600 tabular-nums dark:text-orange-400">{pct}%</span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full rounded-full bg-degraded transition-all duration-500 ease-out"
                style={{ width: `${pct}%` }}
              />
            </div>
          </div>
        )}

        <div className="border-t border-border/70 pt-4">
          <div className="mb-4 flex items-center gap-2">
            <Clock className="size-3.5 text-muted-foreground" />
            <h4 className="text-xs font-medium tracking-wide text-muted-foreground uppercase">Timeline</h4>
          </div>
          <div className="relative pl-8">
            <div className="absolute top-3 bottom-3 left-[11px] w-px bg-border" />
            <div className="space-y-5">
              {timeline.map((e) => (
                <div key={e.label} className="relative">
                  <div
                    className={cn(
                      "absolute top-0 -left-8 z-10 flex size-6 items-center justify-center rounded-full border",
                      e.ring,
                    )}
                  >
                    <e.icon className={cn("size-3", e.color)} />
                  </div>
                  <div className="flex flex-col gap-0.5 sm:flex-row sm:items-baseline sm:justify-between">
                    <span className={cn("text-sm font-medium", e.text)}>{e.label}</span>
                    <span className="text-xs text-muted-foreground tabular-nums">
                      {formatWhen(e.at)}
                      {recentAgo(e.at) && <span className="ml-1 opacity-60">· {recentAgo(e.at)}</span>}
                    </span>
                  </div>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

function When({ icon: Icon, label, at }: { icon: typeof Calendar; label: string; at: string }) {
  return (
    <div className="flex items-center gap-2.5">
      <Icon className="size-4 shrink-0 text-muted-foreground" />
      <div className="min-w-0">
        <dt className="text-xs font-medium tracking-wide text-muted-foreground uppercase">{label}</dt>
        <dd className="text-[13px] font-medium text-foreground tabular-nums">{formatWhen(at)}</dd>
      </div>
    </div>
  );
}
