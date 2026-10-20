import { Settings as SettingsIcon, ShipWheel } from "lucide-react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { cn, timeAgo } from "@/lib/utils";
import { buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { WatchTheWatcherCard } from "@/components/WatchTheWatcher";
import { KumaImportCard } from "@/components/KumaImport";
import { RailwayImportCard } from "@/components/RailwayImport";
import { useCanEdit } from "@/components/AuthGate";
import { MonitorsFileNotice } from "@/components/MonitorsFileNotice";

export function Settings() {
  const info = useQuery({ queryKey: ["info"], queryFn: api.info });
  const i = info.data;
  const canEdit = useCanEdit();

  return (
    <>
      <PageHeader title="Settings" icon={SettingsIcon} description="Settings for this agent." />
      <ErrorNote error={info.error} />
      <MonitorsFileNotice />
      {i && (
        <div className="flex flex-col gap-6">
          <WatchTheWatcherCard />
          {i.discovery && <DiscoveryCard />}
          {canEdit && <KumaImportCard />}
          {canEdit && <RailwayImportCard />}

          <Card>
            <CardHeader>
              <CardTitle>Agent</CardTitle>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-x-8 gap-y-3 text-sm sm:grid-cols-[max-content_1fr]">
                <Row label="Version" value={i.version} />
                <Row
                  label="Kubernetes checks"
                  value={i.kubernetes ? "Available (running in cluster)" : "Unavailable (not in a cluster)"}
                />
                <Row
                  label="Monitors file"
                  value={
                    !i.monitors_file
                      ? "Not configured"
                      : i.monitors_file_error
                        ? i.monitors_file_error.applied
                          ? "Loaded, some monitors skipped (see the notice above)"
                          : "Not applied: it has an error (see the notice above)"
                        : "Loaded"
                  }
                />
                <Row
                  label="Data retention"
                  value={`Every check ${i.retention_days} days · daily history ${i.history_days} days`}
                />
                <Row
                  label="Status page"
                  value={
                    i.status_page_enabled ? (
                      <Link to="/status-page" className="underline">
                        {i.status_page_title}
                      </Link>
                    ) : (
                      "Disabled"
                    )
                  }
                />
              </dl>
            </CardContent>
          </Card>
        </div>
      )}
    </>
  );
}

/** A summary of Kubernetes discovery, linking to its page. */
function DiscoveryCard() {
  const status = useQuery({ queryKey: ["discovery"], queryFn: api.discoveryStatus, refetchInterval: 10_000 });
  const s = status.data;
  const problems = s ? s.warnings.length + (s.error ? 1 : 0) : 0;
  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-4">
        <div>
          <CardTitle className="flex items-center gap-2">
            <ShipWheel className="size-4" /> Kubernetes discovery
          </CardTitle>
          <p className="mt-1 text-sm text-muted-foreground">
            Resources labeled <code className="font-mono text-xs">upti.my/monitor: &quot;true&quot;</code> are monitored
            automatically.
          </p>
        </div>
        <Link to="/settings/kubernetes" className={buttonVariants({ variant: "outline", size: "sm" })}>
          Resources and status
        </Link>
      </CardHeader>
      <CardContent>
        <ErrorNote error={status.error} />
        {s && (
          <p className={cn("text-sm", problems ? "text-paused" : "text-muted-foreground")}>
            {s.monitors} monitor{s.monitors === 1 ? "" : "s"} from{" "}
            {s.scope === "cluster" ? "the whole cluster" : s.scope}
            {s.last_scan && `, last scan ${timeAgo(s.last_scan)}`}
            {problems ? ` · ${problems} problem${problems === 1 ? "" : "s"}` : ""}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd>{value}</dd>
    </>
  );
}
