import { Fragment, useState } from "react";
import { useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { KeyRound } from "lucide-react";
import { api, type HeartbeatSummary, type Run, type RunStats } from "@/lib/api";
import { cn, formatClock, formatDateTime, formatDuration, formatUptime, pingURL, timeAgo } from "@/lib/utils";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { CopyField } from "@/components/ui/copy-button";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { DailyBars, HeartbeatDot, HeartbeatStateLabel, RunBars, runOutcome, runTime } from "@/components/status";
import { BackLink, EventsCard, MonitorActions, Stat, StatStrip } from "@/components/MonitorPage";
import { AcknowledgedBadge, managedBadge } from "@/components/MonitorEmpty";
import { useCanEdit } from "@/components/AuthGate";

export function HeartbeatDetail() {
  const id = Number(useParams().id);
  const detail = useQuery({ queryKey: ["heartbeat", id], queryFn: () => api.heartbeat(id) });

  if (detail.error) return <ErrorNote error={detail.error} />;
  if (!detail.data) return null;
  const { heartbeat: h, stats, daily, upcoming } = detail.data;

  return (
    <>
      <BackLink m={h} />
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            <HeartbeatDot state={h.tracking.state} className="size-3" />
            {h.name}
          </span>
        }
        description={
          <span className="flex flex-wrap items-center gap-2">
            <Badge>Heartbeat</Badge>
            <span>{h.schedule}</span>
            <span>· {formatDuration(h.heartbeat.grace_seconds * 1000)} grace</span>
            {h.source !== "ui" && <Badge>{managedBadge(h)}</Badge>}
            {h.status === "down" && h.acknowledged && <AcknowledgedBadge ack={h.acknowledged} long />}
          </span>
        }
        actions={<MonitorActions m={h} />}
      />

      <StatStrip>
        <Stat label="State" value={<HeartbeatStateLabel state={h.tracking.state} />} sub={lastRunText(h.last_run)} />
        <NextRunStat h={h} />
        <Stat label="On time 30d" value={formatUptime(stats["30d"].ratio)} sub={runCounts(stats["30d"])} />
        <Stat
          label="Avg duration 30d"
          value={stats["30d"].avg_duration_ms == null ? "—" : formatDuration(stats["30d"].avg_duration_ms)}
          sub={
            stats["30d"].avg_duration_ms == null
              ? "Ping /start when the job begins to measure it"
              : h.last_run?.duration_ms != null
                ? `last run took ${formatDuration(h.last_run.duration_ms)}`
                : undefined
          }
        />
      </StatStrip>

      {/* The same four columns and gap as the stats above, so the cards line up. */}
      <div className="mt-6 grid gap-4 lg:grid-cols-4">
        <div className="min-w-0 lg:col-span-3 [&>*]:h-full">
          {h.source === "kubernetes" ? <CronJobRunsCard h={h} /> : <PingCard h={h} />}
        </div>
        <ScheduleCard h={h} upcoming={upcoming} />
      </div>

      <Card section className="mt-6">
        <CardHeader className="flex-row flex-wrap items-center justify-between gap-2">
          <CardTitle>On-time runs</CardTitle>
          <span className="text-xs text-muted-foreground tabular-nums">
            24h {formatUptime(stats["24h"].ratio)} · 7d {formatUptime(stats["7d"].ratio)} · 30d{" "}
            {formatUptime(stats["30d"].ratio)} · 90d {formatUptime(stats["90d"].ratio)}
          </span>
        </CardHeader>
        <CardContent className="flex flex-col gap-6">
          <div>
            <div className="mb-2 text-xs font-medium text-muted-foreground">Recent runs</div>
            <RunBars runs={h.recent} />
          </div>
          <div>
            <div className="mb-2 text-xs font-medium text-muted-foreground">Last 30 days</div>
            <DailyBars days={daily} measure="on time" />
          </div>
        </CardContent>
      </Card>

      <RunsCard id={h.id} />

      <EventsCard id={h.id} describe={(e) => `${h.name} ${e.message || e.status}`} />
    </>
  );
}

function lastRunText(run: Run | null) {
  if (!run) return "no run yet";
  if (run.outcome === "running" && run.started_at) return `started ${timeAgo(run.started_at)}`;
  return `last run ${timeAgo(runTime(run))}`;
}

function runCounts(s: RunStats) {
  if (!s.runs) return "no runs yet";
  const parts = [`${s.runs} run${s.runs === 1 ? "" : "s"}`];
  if (s.missed) parts.push(`${s.missed} missed`);
  if (s.failed) parts.push(`${s.failed} failed`);
  return parts.join(" · ");
}

function NextRunStat({ h }: { h: HeartbeatSummary }) {
  const { due_at, deadline, running_since } = h.tracking;
  if (!due_at) return <Stat label="Next run" value="—" sub="Paused" />;
  if (running_since) {
    return <Stat label="Next run" value="Running" sub={`started ${timeAgo(running_since)}`} />;
  }
  return (
    <Stat
      label="Next run"
      value={timeAgo(due_at)}
      sub={deadline && `${formatClock(due_at)}, late after ${formatClock(deadline)}`}
    />
  );
}

/**
 * The heartbeat's schedule as a crontab line, for the example. An interval
 * cron can't express exactly falls back to a daily run.
 */
function crontabSchedule(h: HeartbeatSummary["heartbeat"]) {
  if (h.cron) return h.cron;
  const minutes = (h.every_seconds ?? 0) / 60;
  if (!Number.isInteger(minutes)) return "0 3 * * *";
  if (minutes < 60 && 60 % minutes === 0) return minutes === 1 ? "* * * * *" : `*/${minutes} * * * *`;
  const hours = minutes / 60;
  if (Number.isInteger(hours) && hours < 24 && 24 % hours === 0)
    return hours === 1 ? "0 * * * *" : `0 */${hours} * * *`;
  return "0 3 * * *";
}

const examples = {
  cron: {
    label: "Crontab",
    code: (url: string, cron: string) =>
      `${cron} /usr/local/bin/backup.sh && curl -fsS -m 10 --retry 3 ${url} >/dev/null`,
    note: "Pings only when the job succeeds. If it fails or never runs, the run is missed.",
  },
  script: {
    label: "Shell script",
    code: (url: string) =>
      `curl -fsS -m 10 --retry 3 ${url}/start >/dev/null\n/usr/local/bin/backup.sh\ncurl -fsS -m 10 --retry 3 ${url}/$? >/dev/null`,
    note: "Reports the start, the exit code and how long it took. A non-zero exit code alerts right away.",
  },
  log: {
    label: "With output",
    code: (url: string) =>
      `out=$(/usr/local/bin/backup.sh 2>&1); code=$?\ncurl -fsS -m 10 --retry 3 --data-raw "$(printf '%s' "$out" | tail -c 500)" ${url}/$code >/dev/null`,
    note: "POST a body and the agent keeps the first 500 characters with the run, e.g. the end of the log.",
  },
};

/** The URL a job pings, how to call it, and a way to replace it. */
function PingCard({ h }: { h: HeartbeatSummary }) {
  const canEdit = useCanEdit();
  const qc = useQueryClient();
  const [example, setExample] = useState<keyof typeof examples>("cron");
  const rotate = useMutation({
    mutationFn: () => api.rotateToken(h.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["heartbeat", h.id] });
      qc.invalidateQueries({ queryKey: ["heartbeats"], exact: true });
      toast.success("New ping URL", "Update your job to use it. The old URL no longer works.");
    },
  });

  // Viewers don't get the token: anyone with it can report runs.
  if (!h.heartbeat.token) return <ViewerPingNote />;
  const url = pingURL(h.heartbeat.token);
  const ex = examples[example];

  return (
    <Card section>
      <CardHeader className="flex-row items-start justify-between gap-4">
        <div>
          <CardTitle>Ping URL</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            Have your job call this each time it runs. GET and POST both work.
          </p>
        </div>
        {canEdit && h.source === "ui" && (
          <Button
            variant="outline"
            size="sm"
            onClick={() =>
              confirm({
                title: "Replace the ping URL",
                subtitle: "Do this if the URL leaked",
                message:
                  "The current URL stops working right away. Runs that ping it are ignored until you update your job.",
                confirmLabel: "Replace URL",
                icon: KeyRound,
                action: () => rotate.mutateAsync(),
              })
            }
          >
            <KeyRound /> New URL
          </Button>
        )}
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <CopyField text={url} label="Copy ping URL" />

        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
          <dt className="font-mono text-xs leading-5">URL</dt>
          <dd className="text-muted-foreground">The job finished.</dd>
          <dt className="font-mono text-xs leading-5">URL/start</dt>
          <dd className="text-muted-foreground">The job started. Its finish then records how long it took.</dd>
          <dt className="font-mono text-xs leading-5">URL/fail</dt>
          <dd className="text-muted-foreground">The job failed. You're alerted right away.</dd>
          <dt className="font-mono text-xs leading-5">URL/&lt;code&gt;</dt>
          <dd className="text-muted-foreground">The job's exit code: 0 is success, 1 to 255 a failure.</dd>
        </dl>

        <div>
          <div className="mb-2 flex w-fit gap-1 rounded-md bg-muted p-0.5" role="tablist" aria-label="Examples">
            {(Object.keys(examples) as (keyof typeof examples)[]).map((k) => (
              <button
                key={k}
                role="tab"
                aria-selected={example === k}
                onClick={() => setExample(k)}
                className={cn(
                  "rounded px-2.5 py-1 text-xs font-medium",
                  example === k ? "bg-card shadow-xs" : "text-muted-foreground",
                )}
              >
                {examples[k].label}
              </button>
            ))}
          </div>
          <CopyField text={ex.code(url, crontabSchedule(h.heartbeat))} label="Copy example" />
          <p className="mt-2 text-xs text-muted-foreground">{ex.note}</p>
        </div>
      </CardContent>
    </Card>
  );
}

/** A discovered CronJob's runs come from its Jobs, so there's nothing to ping. */
function CronJobRunsCard({ h }: { h: HeartbeatSummary }) {
  const grace = formatDuration(h.heartbeat.grace_seconds * 1000);
  const rows: [string, string][] = [
    ["Job starts", "A run starts, so its duration is measured."],
    ["Job completes", "The run succeeded."],
    ["Job fails", "The run failed, with the reason and the container's exit code (OOMKilled, exit code 1, ...)."],
    ["No Job finishes", `If no run has finished ${grace} after it was due, it's missed.`],
    ["CronJob suspended", "The heartbeat is paused until the CronJob is resumed."],
  ];
  return (
    <Card section>
      <CardHeader>
        <CardTitle>Runs from Kubernetes</CardTitle>
        <p className="mt-1 text-sm text-muted-foreground">
          The agent reads this CronJob&apos;s Jobs every 10 seconds and records each run at the times Kubernetes saw.
          The job doesn&apos;t need to ping anything.
        </p>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
          {rows.map(([when, what]) => (
            <Fragment key={when}>
              <dt className="font-medium whitespace-nowrap">{when}</dt>
              <dd className="text-muted-foreground">{what}</dd>
            </Fragment>
          ))}
        </dl>
        <p className="text-sm text-muted-foreground">
          The schedule and time zone come from the CronJob. Set the grace period with the{" "}
          <code className="font-mono text-xs">upti.my/grace</code> annotation, e.g.{" "}
          <code className="font-mono text-xs">upti.my/grace: 30m</code>.
        </p>
      </CardContent>
    </Card>
  );
}

function ViewerPingNote() {
  return (
    <Card section>
      <CardHeader>
        <CardTitle>Ping URL</CardTitle>
        <p className="mt-1 text-sm text-muted-foreground">
          Only admins can see the ping URL, since anyone with it can report runs.
        </p>
      </CardHeader>
    </Card>
  );
}

function ScheduleCard({ h, upcoming }: { h: HeartbeatSummary; upcoming: string[] }) {
  const s = h.heartbeat;
  return (
    <Card section>
      <CardHeader>
        <CardTitle>Schedule</CardTitle>
      </CardHeader>
      <CardContent>
        <dl className="flex flex-col gap-3 text-sm">
          <Row label="Runs">{s.cron ? <code>{s.cron}</code> : h.schedule}</Row>
          {s.cron && <Row label="Time zone">{s.timezone || "UTC"}</Row>}
          <Row label="Grace period">
            {formatDuration(s.grace_seconds * 1000)}
            <div className="text-xs text-muted-foreground">How late a run can be before it counts as missed.</div>
          </Row>
          <Row label="Next runs">
            {upcoming.length ? (
              <ol className="flex flex-col gap-0.5">
                {upcoming.map((t) => (
                  <li key={t} className="tabular-nums">
                    {formatDateTime(t)} <span className="text-muted-foreground">· {timeAgo(t)}</span>
                  </li>
                ))}
              </ol>
            ) : (
              <span className="text-muted-foreground">Paused</span>
            )}
          </Row>
        </dl>
      </CardContent>
    </Card>
  );
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  );
}

/** The latest runs: when, how it went, how long it took, and what the job said. */
function RunsCard({ id }: { id: number }) {
  const runs = useQuery({ queryKey: ["heartbeat", id, "runs"], queryFn: () => api.runs(id, 50) });
  const [shown, setShown] = useState(10);
  // The API returns them oldest first, for the bars; the table leads with the newest.
  const newest = [...(runs.data ?? [])].reverse();
  return (
    <Card section className="mt-6">
      <CardHeader>
        <CardTitle>Runs</CardTitle>
      </CardHeader>
      <CardContent>
        <ErrorNote error={runs.error} />
        {newest.length ? (
          <div className="-mx-2 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-muted-foreground">
                  <th className="px-2 pb-2 font-medium">Result</th>
                  <th className="px-2 pb-2 font-medium">When</th>
                  <th className="px-2 pb-2 font-medium">Duration</th>
                  <th className="px-2 pb-2 font-medium">Message</th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {newest.slice(0, shown).map((r) => {
                  const { label, color } = runOutcome(r);
                  return (
                    <tr key={r.id}>
                      <td className="px-2 py-2.5 whitespace-nowrap">
                        <span className="inline-flex items-center gap-2 font-medium">
                          <span className={cn("size-2 rounded-full", color)} />
                          {label}
                        </span>
                      </td>
                      <td className="px-2 py-2.5 whitespace-nowrap tabular-nums">{formatDateTime(runTime(r))}</td>
                      <td className="px-2 py-2.5 whitespace-nowrap tabular-nums">
                        {r.duration_ms == null ? "—" : formatDuration(r.duration_ms)}
                      </td>
                      <td className="max-w-md truncate px-2 py-2.5 text-muted-foreground" data-tip={r.message}>
                        {r.message || "—"}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {newest.length > shown && (
              <div className="mt-3 px-2">
                <Button variant="outline" size="sm" onClick={() => setShown((n) => n + 10)}>
                  Show more
                </Button>
              </div>
            )}
          </div>
        ) : (
          runs.isSuccess && <p className="text-sm text-muted-foreground">No runs yet. Ping the URL to record one.</p>
        )}
      </CardContent>
    </Card>
  );
}
