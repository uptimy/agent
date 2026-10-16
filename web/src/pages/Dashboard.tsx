import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Activity, ArrowRight, Bell, FileText, HeartPulse, History, LayoutDashboard, Plus } from "lucide-react";
import { api, type ActivityItem, type HealthcheckSummary, type HeartbeatSummary } from "@/lib/api";
import { cn, timeAgo } from "@/lib/utils";
import { byAttention, countByStatus, kinds, monitorPath, type MonitorKind } from "@/lib/monitors";
import { buttonVariants } from "@/components/ui/button";
import { Section } from "@/components/ui/section";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { StatusDot } from "@/components/status";
import { MonitorEmpty } from "@/components/MonitorEmpty";
import { HealthcheckList } from "@/components/HealthcheckList";
import { HeartbeatList } from "@/components/HeartbeatList";
import { WatchTheWatcherNudge } from "@/components/WatchTheWatcher";
import { MonitorsFileNotice } from "@/components/MonitorsFileNotice";
import { useCanEdit } from "@/components/AuthGate";

// How many of each kind the dashboard shows before "View all".
const PREVIEW = 6;

export function Dashboard() {
  const healthcheckList = useQuery({ queryKey: ["healthchecks"], queryFn: api.healthchecks });
  const heartbeatList = useQuery({ queryKey: ["heartbeats"], queryFn: api.heartbeats });
  const activity = useQuery({ queryKey: ["activity"], queryFn: api.activity });
  const canEdit = useCanEdit();

  const healthchecks = (healthcheckList.data ?? []).slice().sort(byAttention);
  const heartbeats = (heartbeatList.data ?? []).slice().sort(byAttention);

  return (
    <>
      <PageHeader
        title="Dashboard"
        icon={LayoutDashboard}
        description="See the health of your checks and scheduled jobs at a glance, then jump straight into the one that needs attention."
      />
      <ErrorNote error={healthcheckList.error ?? heartbeatList.error} />
      <MonitorsFileNotice />

      {healthcheckList.isSuccess && heartbeatList.isSuccess && (
        <div className="flex flex-col gap-6">
          <Summary healthchecks={healthchecks} heartbeats={heartbeats} />

          {/* The summary's four columns and gap: the lists take three, activity one. */}
          <div className="grid gap-4 xl:grid-cols-4">
            <div className="flex min-w-0 flex-col gap-6 xl:col-span-3">
              <KindSection kind="healthcheck" icon={HeartPulse} count={healthchecks.length} canEdit={canEdit}>
                <HealthcheckList healthchecks={healthchecks.slice(0, PREVIEW)} />
              </KindSection>
              <KindSection kind="heartbeat" icon={Activity} count={heartbeats.length} canEdit={canEdit}>
                <HeartbeatList heartbeats={heartbeats.slice(0, PREVIEW)} />
              </KindSection>
            </div>

            <div className="flex flex-col gap-6">
              {canEdit && <WatchTheWatcherNudge />}
              <Section title="Recent activity" icon={History}>
                {activity.data?.length ? (
                  <ol className="flex flex-col gap-3">
                    {activity.data.slice(0, 12).map((e) => (
                      <ActivityRow key={e.id} e={e} />
                    ))}
                  </ol>
                ) : (
                  <p className="text-sm text-muted-foreground">Status changes will show up here.</p>
                )}
              </Section>
            </div>
          </div>
        </div>
      )}
    </>
  );
}

const healthcheckChange = { up: "is up", down: "went down", pending: "is pending", paused: "was paused" };

/** "API went down", "Nightly backup missed its run due 03:00 CEST". */
function ActivityRow({ e }: { e: ActivityItem }) {
  // A heartbeat's events say what happened in words; a healthcheck's message
  // is the probe's error, which the detail page shows instead.
  const what = e.kind === "heartbeat" && e.message ? e.message : healthcheckChange[e.status];
  return (
    <li className="flex gap-3 text-sm">
      <StatusDot status={e.status} className="mt-1.5" />
      <div className="min-w-0">
        {e.monitor_name ? (
          <Link to={monitorPath({ id: e.monitor_id, kind: e.kind })} className="font-medium hover:underline">
            {e.monitor_name}
          </Link>
        ) : (
          <span className="font-medium">Deleted monitor</span>
        )}{" "}
        <span className="text-muted-foreground">{what}</span>
        <div className="text-xs text-muted-foreground">{timeAgo(e.time)}</div>
      </div>
    </li>
  );
}

function KindSection({
  kind,
  icon,
  count: n,
  canEdit,
  children,
}: {
  kind: MonitorKind;
  icon: typeof HeartPulse;
  count: number;
  canEdit: boolean;
  children: React.ReactNode;
}) {
  const k = kinds[kind];
  const description =
    kind === "healthcheck"
      ? `${n} healthcheck${n === 1 ? "" : "s"} run from this agent.`
      : `${n} heartbeat${n === 1 ? "" : "s"} tracking scheduled jobs and expected pings.`;
  return (
    <Section
      title={k.title}
      icon={icon}
      description={description}
      action={
        n > 0 && (
          <>
            {canEdit && (
              <Link to={`${k.path}/new`} className={buttonVariants({ variant: "outline", size: "sm" })}>
                <Plus /> New
              </Link>
            )}
            <Link to={k.path} className={buttonVariants({ variant: "ghost", size: "sm" })}>
              View all <ArrowRight />
            </Link>
          </>
        )
      }
    >
      {n === 0 ? <MonitorEmpty kind={kind} compact /> : children}
      {n > PREVIEW && (
        <Link to={k.path} className="mt-3 block text-center text-sm text-muted-foreground hover:text-foreground">
          {n - PREVIEW} more {k.noun}
          {n - PREVIEW === 1 ? "" : "s"}
        </Link>
      )}
    </Section>
  );
}

type Tone = "up" | "down" | "paused" | "info";

const tones: Record<Tone, { hover: string; icon: string; badge: string }> = {
  up: { hover: "hover:border-up", icon: "text-up", badge: "bg-up/15 text-up" },
  down: { hover: "hover:border-down", icon: "text-down", badge: "bg-down/15 text-down" },
  paused: { hover: "hover:border-paused", icon: "text-paused", badge: "bg-paused/15 text-paused" },
  info: { hover: "hover:border-sky-500", icon: "text-sky-500", badge: "bg-sky-500/15 text-sky-600 dark:text-sky-400" },
};

/** The four cards at the top, like "Monitoring Summary" in the Uptimy app. */
function Summary({ healthchecks, heartbeats }: { healthchecks: HealthcheckSummary[]; heartbeats: HeartbeatSummary[] }) {
  const notifiers = useQuery({ queryKey: ["notifiers"], queryFn: api.notifiers });
  const info = useQuery({ queryKey: ["info"], queryFn: api.info });

  const hc = countByStatus(healthchecks);
  const hb = countByStatus(heartbeats);
  const channels = notifiers.data?.filter((n) => n.enabled).length ?? 0;
  const publicCount = [...healthchecks, ...heartbeats].filter((m) => m.public).length;
  const pageOn = info.data?.status_page_enabled ?? false;
  const paused = (n: number) => (n ? `, ${n} paused` : "");

  const cards: {
    title: string;
    value: number;
    detail: string;
    icon: typeof HeartPulse;
    to: string;
    tone: Tone;
    badge: string;
  }[] = [
    {
      title: healthchecks.length === 1 ? "Healthcheck" : "Healthchecks",
      value: healthchecks.length,
      detail: healthchecks.length
        ? `${hc.up} up, ${hc.down} need attention${paused(hc.paused)}`
        : "Nothing checked yet",
      icon: HeartPulse,
      to: "/healthchecks",
      tone: hc.down ? "down" : "up",
      badge: hc.down ? "Review" : healthchecks.length ? "Healthy" : "Empty",
    },
    {
      title: heartbeats.length === 1 ? "Heartbeat" : "Heartbeats",
      value: heartbeats.length,
      detail: heartbeats.length
        ? `${hb.up} on schedule, ${hb.down} need attention${hb.pending ? `, ${hb.pending} waiting` : ""}${paused(hb.paused)}`
        : "No scheduled jobs tracked",
      icon: Activity,
      to: "/heartbeats",
      tone: hb.down ? "paused" : "up",
      badge: hb.down ? "Review" : heartbeats.length ? "On time" : "Empty",
    },
    {
      title: channels === 1 ? "Alert channel" : "Alert channels",
      value: channels,
      detail: channels ? "Alerted when monitors go down and recover" : "Nobody gets alerted yet",
      icon: Bell,
      to: "/notifications",
      tone: channels ? "up" : "paused",
      badge: channels ? "Active" : "Set up",
    },
    {
      title: "On status page",
      value: publicCount,
      detail: pageOn ? "Public at /status" : "The status page is off",
      icon: FileText,
      to: "/status-page",
      tone: pageOn ? "up" : "info",
      badge: pageOn ? "Live" : "Off",
    },
  ];

  return (
    // On the page's four-column grid, not inside a card, so the cards line up
    // with the sections below them.
    <section>
      <h2 className="flex items-center gap-2 text-base font-bold md:text-lg">
        <LayoutDashboard className="size-4 shrink-0" />
        Monitoring summary
      </h2>
      <p className="mt-1 mb-4 text-sm text-muted-foreground">
        High-signal counts for your healthchecks, heartbeats and alerting.
      </p>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {cards.map((c) => {
          const tone = tones[c.tone];
          return (
            <Link
              key={c.to}
              to={c.to}
              className={cn(
                "flex h-full flex-col gap-4 rounded-xl border-[1.5px] bg-card p-4 shadow-xs transition-all hover:bg-nav-hover hover:shadow-md",
                tone.hover,
              )}
            >
              <div className="flex items-start justify-between gap-3">
                <span className={cn("grid size-10 shrink-0 place-items-center rounded-lg bg-nav-hover", tone.icon)}>
                  <c.icon className="size-5" />
                </span>
                <span className={cn("rounded-full px-2 py-0.5 text-xs font-medium", tone.badge)}>{c.badge}</span>
              </div>
              <div>
                <div className="flex flex-wrap items-baseline gap-2">
                  <span className="text-2xl leading-none font-bold tabular-nums">{c.value}</span>
                  <span className="text-sm font-semibold">{c.title}</span>
                </div>
                <p className="mt-1 text-sm text-muted-foreground">{c.detail}</p>
              </div>
            </Link>
          );
        })}
      </div>
    </section>
  );
}
