// Public status page. Layout, copy and colours mirror the hosted Uptimy
// status pages (upti.my-status) so every agent status page reads as Uptimy.
import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { AlertCircle, AlertTriangle, CheckCircle, Clock, ExternalLink, XCircle, Wrench } from "lucide-react";
import { api, type PublicMonitor, type PublicStatus, type Status } from "@/lib/api";
import { cn, formatDateTime, timeAgo } from "@/lib/utils";
import { Card } from "@/components/ui/card";
import { DailyBars } from "@/components/status";
import { PoweredBy } from "@/components/brand";
import { MaintenanceCard } from "@/components/status-page/maintenance";
import { Moon, Sun } from "lucide-react";
import { useTheme } from "@/lib/theme";
import { ErrorNote } from "@/components/Layout";
import { IncidentCard } from "@/components/status-page/incidents";
import { AnnouncementCard } from "@/components/status-page/announcement";
import { StatusEmpty, StatusList, StatusSection, UptimeLegend } from "@/components/status-page/section";

const brandText = "text-[#268256] dark:text-[#65bd91]"; // emerald-600 / 400 in the Uptimy ramp

const banners: Record<
  PublicStatus["overall"],
  {
    icon: typeof CheckCircle;
    iconColor: string;
    iconBg: string;
    tint: string;
    titleColor: string;
    title: string;
    subtitle: string;
  }
> = {
  operational: {
    icon: CheckCircle,
    iconColor: "text-up",
    iconBg: "bg-up/10",
    tint: "bg-up/5",
    titleColor: brandText,
    title: "All systems operational",
    subtitle: "All systems are running smoothly",
  },
  maintenance: {
    icon: Wrench,
    iconColor: "text-sky-500",
    iconBg: "bg-sky-500/10",
    tint: "bg-sky-500/5",
    titleColor: "text-sky-600 dark:text-sky-400",
    title: "Maintenance in progress",
    subtitle: "Some services are under planned maintenance",
  },
  degraded: {
    icon: AlertTriangle,
    iconColor: "text-degraded",
    iconBg: "bg-degraded/10",
    tint: "bg-degraded/5",
    titleColor: "text-orange-600 dark:text-orange-400",
    title: "Some issues detected",
    subtitle: "Some services are experiencing issues",
  },
  outage: {
    icon: XCircle,
    iconColor: "text-down",
    iconBg: "bg-down/10",
    tint: "bg-down/5",
    titleColor: "text-red-600 dark:text-red-400",
    title: "Outage detected",
    subtitle: "We're working to resolve the issues",
  },
};

const accents: Record<Status, { border: string; dot: string; live?: boolean }> = {
  up: { border: "border-l-up", dot: "bg-up" },
  down: { border: "border-l-down", dot: "bg-down", live: true },
  pending: { border: "border-l-no-data", dot: "bg-pending" },
  paused: { border: "border-l-no-data", dot: "bg-pending" },
};

export function StatusPage() {
  // ?preview is set by the status page editor's preview frame.
  const preview = new URLSearchParams(window.location.search).has("preview");
  const status = useQuery({
    queryKey: ["public-status"],
    queryFn: () => api.publicStatus(preview),
    refetchInterval: 30_000,
  });
  const s = status.data;
  const daysToShow = useDaysToShow();
  const title = s?.title;
  const active = s?.incidents.filter((i) => !i.resolved_at) ?? [];
  const past = s?.incidents.filter((i) => i.resolved_at) ?? [];
  useEffect(() => {
    if (title) document.title = title;
  }, [title]);

  return (
    <div
      className="status-surface min-h-dvh overflow-x-hidden"
      // Section headings use the page's accent color (theme.primaryColor on
      // hosted pages); unset = Uptimy green.
      style={s?.accent_color ? ({ "--status-accent": s.accent_color } as React.CSSProperties) : undefined}
    >
      <div className="mx-auto max-w-4xl px-4 py-6 sm:px-6 sm:py-8 lg:px-8 lg:py-10">
        {status.error && <ErrorNote error={status.error} />}
        {!s && !status.error && <StatusSkeleton />}
        {s && (
          <>
            <div className="mb-6 sm:mb-8">
              <div className="flex items-center justify-between gap-4">
                <div className="min-w-0 flex-1">
                  {s.logos.light || s.logos.dark ? (
                    <>
                      <h1 className="sr-only">{s.title}</h1>
                      <StatusLogo logos={s.logos} alt={`${s.title} logo`} />
                    </>
                  ) : (
                    <h1 className="text-xl leading-tight font-semibold break-words sm:text-2xl lg:text-3xl">
                      {s.title}
                    </h1>
                  )}
                </div>
                {s.website_url && (
                  <a
                    href={s.website_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex min-h-11 shrink-0 items-center gap-2 rounded-md px-3 text-sm font-semibold transition-colors hover:bg-muted sm:min-h-9 sm:text-base"
                  >
                    <span className="hidden sm:inline">Visit website</span>
                    <ExternalLink className="size-4" />
                    <span className="sr-only sm:hidden">Visit website</span>
                  </a>
                )}
              </div>
              {s.description && (
                <p className="mt-2 text-sm whitespace-pre-line text-muted-foreground sm:text-base">{s.description}</p>
              )}
            </div>
            <Banner overall={s.overall} />
            {s.announcement && (
              <div className="mt-4">
                <AnnouncementCard announcement={s.announcement} />
              </div>
            )}

            {s.sections.length === 0 ? (
              <div className="mt-10">
                <StatusList>
                  <StatusEmpty icon={<AlertCircle className="size-4" />}>No monitors to show yet.</StatusEmpty>
                </StatusList>
              </div>
            ) : (
              <div className="mt-10 space-y-10">
                {s.sections.map((section) => (
                  <StatusSection
                    key={section.name}
                    title={section.name}
                    titleStyle={{ color: "var(--status-accent, var(--foreground))" }}
                    aside={<UptimeLegend />}
                  >
                    <StatusList>
                      {section.monitors.map((m) => (
                        <MonitorRow key={m.name} m={m} daysToShow={daysToShow} />
                      ))}
                    </StatusList>
                  </StatusSection>
                ))}
              </div>
            )}

            {active.length > 0 && (
              <StatusSection
                className="mt-12"
                title={<span className="text-red-600 dark:text-red-400">Active incidents</span>}
                icon={
                  <span className="relative flex size-2.5">
                    <span className="absolute inline-flex size-full animate-ping rounded-full bg-down opacity-60 motion-reduce:hidden" />
                    <span className="relative inline-flex size-2.5 rounded-full bg-down" />
                  </span>
                }
                aside={<span className="text-sm text-muted-foreground tabular-nums">{active.length}</span>}
              >
                <StatusList>
                  {active.map((i) => (
                    <IncidentCard key={`${i.created_at}-${i.title}`} incident={i} />
                  ))}
                </StatusList>
              </StatusSection>
            )}

            {past.length > 0 && (
              <StatusSection
                className="mt-12"
                title="Past incidents"
                icon={<Clock className="size-4 text-muted-foreground" />}
                aside={<span className="text-sm text-muted-foreground tabular-nums">{past.length}</span>}
              >
                <StatusList>
                  {past.map((i) => (
                    <IncidentCard key={`${i.created_at}-${i.title}`} incident={i} />
                  ))}
                </StatusList>
              </StatusSection>
            )}

            {s.maintenance.length > 0 && (
              <StatusSection
                className="mt-12"
                title="Scheduled maintenance"
                icon={<Wrench className="size-4 text-muted-foreground" />}
              >
                <StatusList>
                  {[...s.maintenance]
                    .sort((a, b) => maintenanceRank[a.state] - maintenanceRank[b.state])
                    .map((m) => (
                      <MaintenanceCard key={`${m.title}-${m.starts_at}`} m={m} />
                    ))}
                </StatusList>
              </StatusSection>
            )}

            {s.events.length > 0 && (
              <StatusSection className="mt-12" title="Recent events">
                <StatusList>
                  {s.events.map((e, i) => (
                    <div key={i} className="flex items-baseline justify-between gap-4 px-4 py-3 text-sm sm:px-5">
                      <span>
                        <span className="font-medium">{e.monitor}</span>{" "}
                        <span className={e.status === "down" ? "text-red-600 dark:text-red-400" : brandText}>
                          {e.status === "down" ? "went down" : "recovered"}
                        </span>
                      </span>
                      <time className="shrink-0 text-xs text-muted-foreground" dateTime={e.time}>
                        {formatDateTime(e.time)}
                      </time>
                    </div>
                  ))}
                </StatusList>
              </StatusSection>
            )}

            <footer className="mt-10 flex flex-col items-center gap-3 text-xs text-muted-foreground">
              <div className="flex items-center gap-3">
                <PoweredBy medium="status_page" />
                <span className="text-border">·</span>
                <ThemeToggle />
              </div>
              <span>Last updated {timeAgo(s.updated)}</span>
            </footer>
          </>
        )}
      </div>
    </div>
  );
}

/** In progress first, then upcoming, then recently completed. */
const maintenanceRank = { active: 0, scheduled: 1, ended: 2 } as const;

function Banner({ overall }: { overall: PublicStatus["overall"] }) {
  const b = banners[overall];
  const Icon = b.icon;
  return (
    <Card className="overflow-hidden shadow-none">
      <div className={cn("flex justify-center p-4 sm:p-6 lg:p-8", b.tint)}>
        <div className="flex flex-col items-center gap-3 text-center sm:flex-row sm:gap-4 sm:text-left">
          <div className={cn("shrink-0 rounded-full p-2 sm:p-2.5", b.iconBg)}>
            <Icon className={cn("size-5 sm:size-6", b.iconColor)} />
          </div>
          <div>
            <h2 className={cn("mb-0.5 text-lg font-semibold sm:text-xl lg:text-2xl", b.titleColor)}>{b.title}</h2>
            <p className="text-sm text-muted-foreground">{b.subtitle}</p>
          </div>
        </div>
      </div>
    </Card>
  );
}

const maintenanceAccent = { border: "border-l-sky-500", dot: "bg-sky-500", live: false };

/**
 * One monitor, as on the hosted Uptimy status pages (StatusMonitorRow): a row
 * with its status, name and uptime, then its daily bars.
 */
function MonitorRow({ m, daysToShow }: { m: PublicMonitor; daysToShow: number }) {
  const accent = m.in_maintenance ? maintenanceAccent : accents[m.status];
  const pct = m.ratio == null ? null : m.ratio * 100;
  // A healthcheck's uptime, or the share of a heartbeat's runs that were on time.
  const heartbeat = m.kind === "heartbeat";
  const measure = heartbeat ? "on time" : "uptime";
  const when = heartbeat
    ? { label: "Last run", time: m.last_run }
    : { label: "Last change", time: m.last_change ?? undefined };
  const days = m.days.slice(-daysToShow);
  // The edge only for what needs attention: down, or in maintenance.
  const edge = m.in_maintenance ? "bg-sky-500" : m.status === "down" ? "bg-down" : null;

  return (
    <div className="relative px-4 py-4 sm:px-5">
      {edge && <span aria-hidden="true" className={cn("absolute inset-y-0 left-0 w-[3px]", edge)} />}
      <div className="flex items-center justify-between gap-3">
        <div className="flex min-w-0 items-center gap-3">
          <span className="relative flex size-2.5 shrink-0">
            {accent.live && (
              <span
                className={cn(
                  "absolute inline-flex size-full animate-ping rounded-full opacity-60 motion-reduce:hidden",
                  accent.dot,
                )}
              />
            )}
            <span className={cn("relative inline-flex size-2.5 rounded-full", accent.dot)} />
          </span>
          <div className="min-w-0">
            <div className="flex min-w-0 items-center gap-2">
              <h3 className="truncate text-sm font-semibold sm:text-base">{m.name}</h3>
              <span className="hidden shrink-0 rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground sm:inline-flex">
                {m.type_label}
              </span>
              {m.in_maintenance && (
                <span className="inline-flex shrink-0 items-center rounded-full bg-sky-500/15 px-2 py-0.5 text-[11px] font-medium text-sky-600 dark:text-sky-400">
                  Maintenance
                </span>
              )}
            </div>
            <div className="truncate text-xs text-muted-foreground">
              {when.label}: {when.time ? formatDateTime(when.time) : "never"}
            </div>
          </div>
        </div>
        <div
          className={cn(
            "shrink-0 text-right text-base font-semibold tabular-nums sm:text-lg",
            pct == null
              ? "text-muted-foreground"
              : pct >= 99
                ? brandText
                : pct >= 95
                  ? "text-orange-600 dark:text-orange-400"
                  : "text-red-600 dark:text-red-400",
          )}
        >
          {pct == null ? <span className="text-sm font-medium">No data yet</span> : `${pct.toFixed(2)}%`}
        </div>
      </div>

      <div className="mt-3">
        <DailyBars days={days} measure={measure} barClassName="h-7" />
        <div className="mt-1.5 flex justify-between text-xs text-muted-foreground">
          <span>{days.length} days ago</span>
          <span>Today</span>
        </div>
      </div>
    </div>
  );
}

/**
 * Days of bars by screen width, as on hosted status pages: 90 on desktop,
 * 60 on tablets, 30 on phones. Capped by the history the API sends.
 */
function useDaysToShow() {
  const pick = () => (window.innerWidth < 768 ? 30 : window.innerWidth < 1024 ? 60 : 90);
  const [n, setN] = useState(pick);
  useEffect(() => {
    const onResize = () => setN(pick());
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return n;
}

/** Loading placeholder in the page's shape, so nothing jumps when data arrives. */
function StatusSkeleton() {
  const block = "animate-pulse rounded-md bg-muted";
  return (
    <div role="status" aria-busy="true" className="space-y-10">
      <span className="sr-only">Loading status page…</span>
      <div className="flex items-center justify-between">
        <div className={cn(block, "h-10 w-40")} />
        <div className={cn(block, "h-9 w-24")} />
      </div>
      <div className="h-24 rounded-xl border bg-card" />
      {[0, 1].map((g) => (
        <div key={g}>
          <div className={cn(block, "mb-3 h-4 w-32")} />
          <div className="divide-y overflow-hidden rounded-xl border bg-card">
            {[0, 1, 2].map((r) => (
              <div key={r} className="px-4 py-4 sm:px-5">
                <div className="flex items-center justify-between gap-3">
                  <div className={cn(block, "h-4 w-40")} />
                  <div className={cn(block, "h-5 w-16")} />
                </div>
                <div className={cn(block, "mt-3 h-7 w-full")} />
              </div>
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}

/** Flips light/dark, like the toggle on hosted Uptimy status pages. */
function ThemeToggle() {
  const { resolved, setTheme } = useTheme();
  const next = resolved === "dark" ? "light" : "dark";
  return (
    <button
      type="button"
      onClick={() => setTheme(next)}
      className="grid size-8 place-items-center rounded-md border transition-colors hover:bg-muted hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
    >
      {resolved === "dark" ? <Moon className="size-4" /> : <Sun className="size-4" />}
      <span className="sr-only">Switch to {next} theme</span>
    </button>
  );
}

/** The logo, with the dark variant in dark mode when there is one. */
function StatusLogo({ logos, alt }: { logos: PublicStatus["logos"]; alt: string }) {
  const light = logos.light ?? logos.dark!;
  const dark = logos.dark ?? logos.light!;
  const size = "h-10 w-auto max-w-[200px] object-contain object-left sm:h-12 sm:max-w-[260px] lg:h-14 lg:max-w-[320px]";
  return (
    <>
      <img src={light} alt={alt} className={cn(size, "dark:hidden")} />
      <img src={dark} alt={alt} className={cn(size, "hidden dark:block")} />
    </>
  );
}
