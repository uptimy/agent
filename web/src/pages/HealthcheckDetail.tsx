import { useState } from "react";
import { useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { RefreshCw } from "lucide-react";
import { api, type Daily, type Result, type Status } from "@/lib/api";
import { cn, formatInterval, formatLatency, formatUptime, timeAgo } from "@/lib/utils";
import { useCheckTypeLabel } from "@/lib/types";
import { toast } from "@/components/ui/toast";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { ResultBars, StatusDot, StatusLabel } from "@/components/status";
import { BackLink, EventsCard, MonitorActions, Stat } from "@/components/MonitorPage";
import { AcknowledgedBadge, managedBadge } from "@/components/MonitorEmpty";

// Up to 7 days every check is shown; longer ranges are one point a day, from
// the daily history (kept longer than every check).
const ranges = [
  { hours: 1, label: "1h" },
  { hours: 24, label: "24h" },
  { hours: 24 * 7, label: "7d" },
  { hours: 24 * 30, label: "30d" },
  { hours: 24 * 90, label: "90d" },
];
const dailyFrom = 24 * 30;

const statusWords: Record<Status, string> = { up: "Up", down: "Down", pending: "Pending", paused: "Paused" };

export function HealthcheckDetail() {
  const id = Number(useParams().id);
  const [hours, setHours] = useState(24);
  const typeLabel = useCheckTypeLabel();
  const qc = useQueryClient();

  const detail = useQuery({ queryKey: ["healthcheck", id], queryFn: () => api.healthcheck(id) });
  const daily = hours >= dailyFrom;
  const results = useQuery({
    queryKey: ["healthcheck", id, "results", hours],
    queryFn: () => api.results(id, hours),
    enabled: !daily,
  });
  const days = useQuery({
    queryKey: ["healthcheck", id, "daily", hours / 24],
    queryFn: () => api.daily(id, hours / 24),
    enabled: daily,
  });
  const checkNow = useMutation({
    mutationFn: () => api.checkNow(id),
    onSuccess: () => {
      // The result arrives over the live connection, which refreshes this page.
      qc.invalidateQueries({ queryKey: ["healthcheck", id] });
      toast.info("Checking now", detail.data?.healthcheck.name);
    },
    onError: (err) => toast.error("Couldn't run the check", err.message),
  });

  if (detail.error) return <ErrorNote error={detail.error} />;
  if (!detail.data) return null;
  const { healthcheck: h, uptime } = detail.data;

  return (
    <>
      <BackLink m={h} />
      <PageHeader
        title={
          <span className="flex items-center gap-3">
            <StatusDot status={h.status} className="size-3" />
            {h.name}
          </span>
        }
        description={
          <span className="flex flex-wrap items-center gap-2">
            <Badge>{typeLabel(h.check.type)}</Badge>
            {h.target && <span className="break-all">{h.target}</span>}
            <span>· every {formatInterval(h.check.interval_seconds)}</span>
            {h.source !== "ui" && <Badge>{managedBadge(h)}</Badge>}
            {h.status === "down" && h.acknowledged && <AcknowledgedBadge ack={h.acknowledged} long />}
          </span>
        }
        actions={
          <MonitorActions m={h}>
            {!h.paused && (
              <Button variant="outline" onClick={() => checkNow.mutate()} disabled={checkNow.isPending}>
                <RefreshCw className={cn(checkNow.isPending && "animate-spin")} /> Check now
              </Button>
            )}
          </MonitorActions>
        }
      />

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat
          label="Status"
          value={<StatusLabel status={h.status} />}
          sub={h.last_result ? `checked ${timeAgo(h.last_result.time)}` : "no checks yet"}
        />
        <Stat label="Uptime 24h" value={formatUptime(uptime["24h"].ratio)} sub={`${uptime["24h"].checks} checks`} />
        <Stat
          label="Uptime 7d"
          value={formatUptime(uptime["7d"].ratio)}
          sub={`30d: ${formatUptime(uptime["30d"].ratio)} · 90d: ${formatUptime(uptime["90d"].ratio)}`}
        />
        <Stat
          label="Avg response 24h"
          value={formatLatency(uptime["24h"].avg_latency_ms)}
          sub={h.last_result?.ok === false ? h.last_result.message : undefined}
        />
      </div>

      <Card className="mt-6">
        <CardHeader className="flex-row items-center justify-between">
          <CardTitle>Response time</CardTitle>
          <div className="flex gap-1 rounded-md bg-muted p-0.5">
            {ranges.map((r) => (
              <button
                key={r.hours}
                onClick={() => setHours(r.hours)}
                aria-pressed={hours === r.hours}
                className={cn(
                  "rounded px-2.5 py-1 text-xs font-medium",
                  hours === r.hours ? "bg-card shadow-xs" : "text-muted-foreground",
                )}
              >
                {r.label}
              </button>
            ))}
          </div>
        </CardHeader>
        <CardContent>
          <ResultBars results={h.recent} slots={40} />
          {daily ? <DailyLatencyChart days={days.data ?? []} /> : <LatencyChart data={results.data ?? []} />}
        </CardContent>
      </Card>

      <EventsCard
        id={h.id}
        describe={(e) => (e.message ? `${statusWords[e.status]} · ${e.message}` : statusWords[e.status])}
      />
    </>
  );
}

function LatencyChart({ data }: { data: Result[] }) {
  const points = data.map((r) => ({ t: new Date(r.time).getTime(), ms: r.ok ? r.latency_ms : null }));
  if (points.length < 2) {
    return <p className="mt-6 text-sm text-muted-foreground">Not enough data yet.</p>;
  }
  return (
    <div className="mt-6 h-56">
      <ResponsiveContainer width="100%" height="100%">
        <AreaChart data={points} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
          <defs>
            <linearGradient id="latency" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor="var(--up)" stopOpacity={0.25} />
              <stop offset="100%" stopColor="var(--up)" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke="var(--border)" vertical={false} />
          <XAxis
            dataKey="t"
            type="number"
            scale="time"
            domain={["dataMin", "dataMax"]}
            tickFormatter={(t) => new Date(t).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}
            stroke="var(--muted-foreground)"
            fontSize={11}
            tickLine={false}
            axisLine={false}
            minTickGap={40}
          />
          <YAxis
            stroke="var(--muted-foreground)"
            fontSize={11}
            tickLine={false}
            axisLine={false}
            width={48}
            tickFormatter={(v) => formatLatency(v)}
          />
          <Tooltip
            contentStyle={{
              background: "var(--card)",
              border: "1px solid var(--border)",
              borderRadius: 8,
              fontSize: 12,
            }}
            labelFormatter={(t) => new Date(t as number).toLocaleString()}
            formatter={(v) => [formatLatency(v as number), "Response"]}
          />
          <Area
            type="monotone"
            dataKey="ms"
            stroke="var(--up)"
            strokeWidth={1.5}
            fill="url(#latency)"
            connectNulls={false}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}

/** One point a day: the median response time, in a band up to the 95th percentile. */
function DailyLatencyChart({ days }: { days: Daily[] }) {
  const points = days.map((d) => ({
    t: Date.parse(`${d.date}T00:00:00Z`),
    p50: d.p50_ms ?? null,
    band: d.p50_ms != null && d.p95_ms != null ? [d.p50_ms, d.p95_ms] : null,
    p95: d.p95_ms ?? null,
  }));
  if (points.filter((p) => p.p50 != null).length < 2) {
    return <p className="mt-6 text-sm text-muted-foreground">Not enough data yet.</p>;
  }
  return (
    <div className="mt-6 h-56">
      <ResponsiveContainer width="100%" height="100%">
        <ComposedChart data={points} margin={{ left: 0, right: 8, top: 4, bottom: 0 }}>
          <CartesianGrid stroke="var(--border)" vertical={false} />
          <XAxis
            dataKey="t"
            type="number"
            scale="time"
            domain={["dataMin", "dataMax"]}
            tickFormatter={(t) =>
              new Date(t).toLocaleDateString(undefined, { month: "short", day: "numeric", timeZone: "UTC" })
            }
            stroke="var(--muted-foreground)"
            fontSize={11}
            tickLine={false}
            axisLine={false}
            minTickGap={40}
          />
          <YAxis
            stroke="var(--muted-foreground)"
            fontSize={11}
            tickLine={false}
            axisLine={false}
            width={48}
            tickFormatter={(v) => formatLatency(v)}
          />
          <Tooltip
            contentStyle={{
              background: "var(--card)",
              border: "1px solid var(--border)",
              borderRadius: 8,
              fontSize: 12,
            }}
            labelFormatter={(t) =>
              new Date(t as number).toLocaleDateString(undefined, { dateStyle: "medium", timeZone: "UTC" }) + " (UTC)"
            }
            formatter={(v, name) => [formatLatency(v as number), name === "p50" ? "Median" : "95th percentile"]}
          />
          <Area
            type="monotone"
            dataKey="band"
            tooltipType="none"
            stroke="none"
            fill="var(--up)"
            fillOpacity={0.15}
            connectNulls={false}
            isAnimationActive={false}
            activeDot={false}
          />
          <Line
            type="monotone"
            dataKey="p50"
            stroke="var(--up)"
            strokeWidth={1.5}
            dot={false}
            connectNulls={false}
            isAnimationActive={false}
          />
          <Line
            type="monotone"
            dataKey="p95"
            stroke="var(--up)"
            strokeOpacity={0.5}
            strokeDasharray="3 3"
            strokeWidth={1}
            dot={false}
            connectNulls={false}
            isAnimationActive={false}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
