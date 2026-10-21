import { useMemo, useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { AlertTriangle, ArrowLeft, RefreshCw, ShipWheel } from "lucide-react";
import { api, type KubeResource } from "@/lib/api";
import { cn, timeAgo } from "@/lib/utils";
import { monitorPath } from "@/lib/monitors";
import { isSystemResource, labelCommands, MONITOR_LABEL, SYSTEM_NAMESPACES } from "@/lib/kubernetes";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton, CopyField } from "@/components/ui/copy-button";
import { Input, Select, Switch } from "@/components/ui/input";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { Stat, StatStrip } from "@/components/MonitorPage";

/**
 * Kubernetes discovery: how its scans are going, and every resource it
 * could monitor, with the command to label it. The agent itself only
 * reads the cluster, so labeling is up to you (or your manifests).
 */
export function KubernetesPage() {
  const status = useQuery({ queryKey: ["discovery"], queryFn: api.discoveryStatus, refetchInterval: 10_000 });
  const s = status.data;
  return (
    <>
      <Link
        to="/settings"
        className="mb-4 inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="size-4" /> Settings
      </Link>
      <PageHeader
        title="Kubernetes"
        icon={ShipWheel}
        description={
          <>
            Resources labeled <code className="font-mono text-sm">{MONITOR_LABEL}: &quot;true&quot;</code> are monitored
            within 30 seconds, and stop being monitored when the label goes.
          </>
        }
      />
      <ErrorNote error={status.error} />
      {s && (
        <div className="flex flex-col gap-6">
          <StatStrip>
            <Stat label="Last scan" value={s.last_scan ? timeAgo(s.last_scan) : "Not yet"} sub="every 30 seconds" />
            <Stat
              label="Scope"
              value={s.scope === "cluster" ? "Whole cluster" : s.scope.replace("namespace ", "")}
              sub={s.scope === "cluster" ? "all namespaces" : "only this namespace (RBAC)"}
            />
            <Stat label="Monitors" value={String(s.monitors)} sub="from labeled resources" />
            <Stat
              label="Problems"
              value={String(s.warnings.length + (s.error ? 1 : 0))}
              sub={s.warnings.length || s.error ? "see below" : "none"}
            />
          </StatStrip>

          {(s.error || s.warnings.length > 0) && (
            <Card section>
              <CardHeader>
                <CardTitle>Problems</CardTitle>
                <p className="mt-1 text-sm text-muted-foreground">
                  What the last scan couldn&apos;t use. A resource with a problem keeps the monitor it had.
                </p>
              </CardHeader>
              <CardContent>
                <ul className="flex flex-col gap-2 text-sm">
                  {s.error && (
                    <li className="flex gap-2 text-down">
                      <AlertTriangle className="mt-0.5 size-4 shrink-0" />
                      The last scan failed, so the monitors it found before were kept: {s.error}
                    </li>
                  )}
                  {s.warnings.map((w) => (
                    <li key={w} className="flex gap-2">
                      <AlertTriangle className="mt-0.5 size-4 shrink-0 text-paused" />
                      <span className="min-w-0 break-words">{w}</span>
                    </li>
                  ))}
                </ul>
              </CardContent>
            </Card>
          )}

          <GettingStarted />
          <ResourcesCard />
        </div>
      )}
    </>
  );
}

function GettingStarted() {
  return (
    <Card section>
      <CardHeader>
        <CardTitle>Label what you want monitored</CardTitle>
        <p className="mt-1 text-sm text-muted-foreground">
          Put the label in your manifests or Helm chart, so every environment and every new app is monitored when
          it&apos;s deployed. A Service gets an HTTP check on its readinessProbe path, an Ingress or HTTPRoute one per
          hostname, a Deployment, StatefulSet or DaemonSet a readiness check, and a CronJob a heartbeat read from its
          Jobs.
        </p>
      </CardHeader>
      <CardContent className="grid gap-4 lg:grid-cols-2">
        <CopyField text={`metadata:\n  labels:\n    ${MONITOR_LABEL}: "true"`} label="Copy YAML" />
        <CopyField text={`kubectl -n shop label service checkout ${MONITOR_LABEL}=true`} label="Copy command" />
        <p className="text-sm text-muted-foreground lg:col-span-2">
          Annotations like <code className="font-mono text-xs">upti.my/name</code>,{" "}
          <code className="font-mono text-xs">upti.my/path</code> and{" "}
          <code className="font-mono text-xs">upti.my/interval</code> adjust the check.{" "}
          <a
            href="https://github.com/uptimy/agent/blob/main/docs/kubernetes.md#auto-discovery"
            target="_blank"
            rel="noopener"
            className="underline"
          >
            All annotations
          </a>
        </p>
      </CardContent>
    </Card>
  );
}

type Show = "all" | "monitored" | "unmonitored";

function ResourcesCard() {
  const resources = useQuery({ queryKey: ["kube-resources"], queryFn: api.kubeResources });
  const [query, setQuery] = useState("");
  const [kind, setKind] = useState("");
  const [namespace, setNamespace] = useState("");
  const [show, setShow] = useState<Show>("all");
  const [system, setSystem] = useState(false);
  const [limit, setLimit] = useState(50);
  const [showCommands, setShowCommands] = useState(false);

  const items = useMemo(() => resources.data?.items ?? [], [resources.data]);
  const kinds = useMemo(() => [...new Set(items.map((r) => r.kind))], [items]);
  const namespaces = useMemo(
    () => [...new Set(items.map((r) => r.namespace))].filter((n) => system || !SYSTEM_NAMESPACES.has(n)).sort(),
    [items, system],
  );
  const shown = items.filter(
    (r) =>
      (system || !isSystemResource(r)) &&
      (!kind || r.kind === kind) &&
      (!namespace || r.namespace === namespace) &&
      (show === "all" || (show === "monitored") === r.monitors.length > 0) &&
      `${r.namespace}/${r.name}`.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const unmonitored = shown.filter((r) => r.monitors.length === 0 && r.label_state !== "out");

  return (
    <Card section>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-4">
        <div>
          <CardTitle>Resources</CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            Everything discovery can monitor in this cluster. Copy the command to label one, or label many at once.
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={() => resources.refetch()} disabled={resources.isFetching}>
          <RefreshCw className={cn(resources.isFetching && "animate-spin")} /> Refresh
        </Button>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <ErrorNote error={resources.error} />
        <div className="flex flex-wrap items-center gap-2">
          <Input
            placeholder="Search by name"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            className="w-56"
            aria-label="Search resources"
          />
          <Select value={kind} onChange={(e) => setKind(e.target.value)} className="w-auto" aria-label="Kind">
            <option value="">All kinds</option>
            {kinds.map((k) => (
              <option key={k} value={k}>
                {k}
              </option>
            ))}
          </Select>
          <Select
            value={namespace}
            onChange={(e) => setNamespace(e.target.value)}
            className="w-auto"
            aria-label="Namespace"
          >
            <option value="">All namespaces</option>
            {namespaces.map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </Select>
          <Select value={show} onChange={(e) => setShow(e.target.value as Show)} className="w-auto" aria-label="Show">
            <option value="all">Monitored or not</option>
            <option value="monitored">Monitored</option>
            <option value="unmonitored">Not monitored</option>
          </Select>
          <Switch id="system-namespaces" checked={system} onChange={setSystem} label="System namespaces" />
        </div>

        {unmonitored.length > 0 && (
          <div className="flex flex-col gap-2">
            <Button variant="outline" size="sm" className="w-fit" onClick={() => setShowCommands((v) => !v)}>
              {showCommands ? "Hide commands" : `Commands to monitor the ${unmonitored.length} shown`}
            </Button>
            {showCommands && <CopyField text={labelCommands(unmonitored).join("\n")} label="Copy commands" />}
          </div>
        )}

        {resources.data && resources.data.truncated.length > 0 && (
          <p className="text-sm text-muted-foreground">
            Only the first 500 of each are listed for: {resources.data.truncated.join(", ")}. Pick a namespace to narrow
            it down.
          </p>
        )}

        {shown.length ? (
          <div className="-mx-2 overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="text-left text-xs text-muted-foreground">
                  <th className="hidden px-2 pb-2 font-medium sm:table-cell">Kind</th>
                  <th className="px-2 pb-2 font-medium">Resource</th>
                  <th className="hidden px-2 pb-2 font-medium sm:table-cell">Monitoring</th>
                  <th className="px-2 pb-2" />
                </tr>
              </thead>
              <tbody className="divide-y">
                {shown.slice(0, limit).map((r) => (
                  <ResourceRow key={`${r.kind}/${r.namespace}/${r.name}`} r={r} />
                ))}
              </tbody>
            </table>
            {shown.length > limit && (
              <div className="mt-3 px-2">
                <Button variant="outline" size="sm" onClick={() => setLimit((n) => n + 50)}>
                  Show more ({shown.length - limit} left)
                </Button>
              </div>
            )}
          </div>
        ) : (
          resources.isSuccess && <p className="text-sm text-muted-foreground">Nothing matches.</p>
        )}
      </CardContent>
    </Card>
  );
}

function ResourceRow({ r }: { r: KubeResource }) {
  const monitored = r.monitors.length > 0;
  // Monitored: the command that stops it. Otherwise the one that starts it
  // (--overwrite fixes an opt-out or a mistyped value).
  const [command] = labelCommands([r], monitored);
  return (
    <tr>
      <td className="hidden px-2 py-2.5 whitespace-nowrap sm:table-cell">
        <Badge>{r.kind}</Badge>
      </td>
      <td className="px-2 py-2.5">
        <span className="text-muted-foreground">{r.namespace}/</span>
        <span className="font-medium break-all">{r.name}</span>
        {/* On phones the kind and status sit under the name. */}
        <div className="mt-1 flex flex-wrap items-center gap-2 sm:hidden">
          <Badge>{r.kind}</Badge>
          <ResourceStatus r={r} />
        </div>
      </td>
      <td className="hidden px-2 py-2.5 sm:table-cell">
        <ResourceStatus r={r} />
      </td>
      <td className="w-10 px-2 py-1 text-right align-top sm:align-middle">
        <CopyButton
          text={command}
          label={monitored ? "Copy the command to stop monitoring it" : "Copy the command to monitor it"}
        />
      </td>
    </tr>
  );
}

function ResourceStatus({ r }: { r: KubeResource }) {
  if (r.monitors.length > 0)
    return (
      <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
        <span className="size-2 rounded-full bg-up" />
        {r.monitors.map((m) => (
          <Link key={m.id} to={monitorPath(m)} className="underline-offset-2 hover:underline">
            {m.name}
          </Link>
        ))}
      </span>
    );
  switch (r.label_state) {
    case "out":
      return (
        <span className="text-muted-foreground">
          Opted out ({MONITOR_LABEL}: {r.label})
        </span>
      );
    case "invalid":
      return (
        <span className="text-paused">
          {MONITOR_LABEL} is &quot;{r.label}&quot;, not &quot;true&quot;
        </span>
      );
    case "in":
      return <span className="text-muted-foreground">Labeled; appears within 30 seconds, or see Problems</span>;
  }
  return <span className="text-muted-foreground">Not monitored</span>;
}
