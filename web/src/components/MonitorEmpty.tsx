import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Eye, FileCode2, Plus, ShipWheel, Wrench } from "lucide-react";
import { cn, timeAgo } from "@/lib/utils";
import { kinds, type MonitorKind } from "@/lib/monitors";
import { api, type Acknowledgement, type MonitorSource } from "@/lib/api";
import { describeRef, MONITOR_LABEL } from "@/lib/kubernetes";
import { CopyField } from "@/components/ui/copy-button";
import { buttonVariants } from "@/components/ui/button";
import { useCanEdit } from "@/components/AuthGate";

const emptyCopy: Record<MonitorKind, { title: string; body: string }> = {
  healthcheck: {
    title: "No healthchecks yet",
    body: "Check a website, API, database, port, DNS record, TLS certificate or Kubernetes workload.",
  },
  heartbeat: {
    title: "No heartbeats yet",
    body: "Cron jobs, backups and workers ping a URL each time they run. You're alerted when a run is missed or fails.",
  },
};

// Inside a cluster, the empty state says monitors can come from labels.
const discoveryCopy: Record<MonitorKind, { what: string; example: string }> = {
  healthcheck: { what: "a Service, Ingress or Deployment", example: "service checkout" },
  heartbeat: { what: "a CronJob", example: "cronjob backup" },
};

/** The empty state of a healthcheck or heartbeat list. */
export function MonitorEmpty({ kind, compact }: { kind: MonitorKind; compact?: boolean }) {
  const canEdit = useCanEdit();
  const copy = emptyCopy[kind];
  const info = useQuery({ queryKey: ["info"], queryFn: api.info });
  const discovery = info.data?.discovery ?? false;
  return (
    <div
      className={cn(
        "flex flex-col items-center gap-2 rounded-lg border border-dashed px-6 text-center",
        compact ? "py-8" : "py-16",
      )}
    >
      <h3 className="font-semibold">{copy.title}</h3>
      <p className="max-w-md text-sm text-muted-foreground">{copy.body}</p>
      {discovery && !compact && (
        <div className="mt-2 flex w-full max-w-lg flex-col gap-2 text-left">
          <p className="text-sm">
            <ShipWheel className="mr-1.5 inline size-4 align-[-3px]" />
            This agent runs in Kubernetes: label {discoveryCopy[kind].what} and it shows up here within 30 seconds.
          </p>
          <CopyField text={`kubectl -n <namespace> label ${discoveryCopy[kind].example} ${MONITOR_LABEL}=true`} />
          <Link to="/settings/kubernetes" className="text-sm underline underline-offset-2">
            See what's in the cluster
          </Link>
        </div>
      )}
      {discovery && compact && (
        <p className="max-w-md text-sm text-muted-foreground">
          Or label {discoveryCopy[kind].what} <code className="rounded bg-muted px-1">{MONITOR_LABEL}=true</code>.{" "}
          <Link to="/settings/kubernetes" className="underline underline-offset-2">
            Cluster resources
          </Link>
        </p>
      )}
      {!compact && (
        <p className="max-w-md text-sm text-muted-foreground">
          You can also define them in YAML with <code className="rounded bg-muted px-1">MONITORS_FILE</code>.
        </p>
      )}
      {canEdit && (
        <Link
          to={`${kinds[kind].path}/new`}
          className={cn(buttonVariants({ size: compact ? "sm" : "default" }), "mt-2")}
        >
          <Plus /> Add {kinds[kind].noun}
        </Link>
      )}
    </div>
  );
}

/** "Discovered in Kubernetes · service shop/checkout", for a monitor's page. */
export function managedBadge(m: { source: MonitorSource; source_ref?: string }) {
  if (m.source === "ui") return "";
  const label = managedLabel[m.source];
  return m.source_ref ? `${label} · ${describeRef(m.source_ref)}` : label;
}

/** How a monitor defined outside the UI is labeled; edit it at its source. */
export const managedLabel: Record<Exclude<MonitorSource, "ui">, string> = {
  file: "Managed by monitors file",
  kubernetes: "Discovered in Kubernetes",
};

/** Marks a monitor defined outside the UI, in lists. */
export function ManagedIcon({ source }: { source: MonitorSource }) {
  if (source === "ui") return null;
  const Icon = source === "kubernetes" ? ShipWheel : FileCode2;
  return (
    <span data-tip={managedLabel[source]} className="text-muted-foreground">
      <Icon className="size-3.5" />
    </span>
  );
}

/** Marks a monitor in a maintenance window: it won't alert. */
/** Someone is on this outage: shown on down monitors that were acknowledged. */
export function AcknowledgedBadge({ ack, long = false }: { ack: Acknowledgement; long?: boolean }) {
  return (
    <span
      data-tip={`Acknowledged by ${ack.by} ${timeAgo(ack.time)}: no more reminders for this outage`}
      className="inline-flex items-center gap-1 rounded-full bg-paused/15 px-1.5 py-0.5 text-xs font-medium whitespace-nowrap text-paused"
    >
      <Eye className="size-3" /> {long ? `${ack.by} is on it · ${timeAgo(ack.time)}` : ack.by}
    </span>
  );
}

export function MaintenanceBadge() {
  return (
    <span
      data-tip="In maintenance: it won't alert"
      className="inline-flex items-center gap-1 rounded-full bg-sky-500/15 px-1.5 py-0.5 text-xs font-medium text-sky-600 dark:text-sky-400"
    >
      <Wrench className="size-3" /> Maintenance
    </span>
  );
}
