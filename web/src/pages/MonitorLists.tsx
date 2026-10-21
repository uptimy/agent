import { useState } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Activity, HeartPulse, Plus, Search } from "lucide-react";
import { api, type Status } from "@/lib/api";
import { byAttention, countByStatus, kinds, type MonitorKind } from "@/lib/monitors";
import { buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { FilterChips } from "@/components/ui/filter-chips";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { MonitorEmpty } from "@/components/MonitorEmpty";
import { HealthcheckList } from "@/components/HealthcheckList";
import { HeartbeatList } from "@/components/HeartbeatList";
import { useCanEdit } from "@/components/AuthGate";
import { MonitorsFileNotice } from "@/components/MonitorsFileNotice";

/** The Healthchecks page. */
export function HealthchecksPage() {
  const list = useQuery({ queryKey: ["healthchecks"], queryFn: api.healthchecks });
  return (
    <ListPage
      kind="healthcheck"
      icon={HeartPulse}
      description="Check websites, APIs, databases, ports, DNS, TLS certificates and Kubernetes workloads from inside your network."
      query={list}
      searchText={(h) => `${h.name} ${h.target}`}
      render={(shown) => <HealthcheckList healthchecks={shown} />}
    />
  );
}

/** The Heartbeats page. */
export function HeartbeatsPage() {
  const list = useQuery({ queryKey: ["heartbeats"], queryFn: api.heartbeats });
  return (
    <ListPage
      kind="heartbeat"
      icon={Activity}
      description="Cron jobs, backups and workers ping a URL each time they run. You're alerted when a run is missed or fails."
      query={list}
      searchText={(h) => `${h.name} ${h.schedule}`}
      render={(shown) => <HeartbeatList heartbeats={shown} />}
    />
  );
}

function ListPage<T extends { name: string; status: Status }>({
  kind,
  icon,
  description,
  query,
  searchText,
  render,
}: {
  kind: MonitorKind;
  icon: typeof HeartPulse;
  description: string;
  query: { data?: T[]; error: Error | null; isSuccess: boolean };
  searchText: (item: T) => string;
  render: (shown: T[]) => React.ReactNode;
}) {
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState<Status | "all">("all");
  const k = kinds[kind];
  const all = (query.data ?? []).slice().sort(byAttention);
  const q = search.trim().toLowerCase();
  const searched = q ? all.filter((m) => searchText(m).toLowerCase().includes(q)) : all;
  const shown = status === "all" ? searched : searched.filter((m) => m.status === status);
  const counts = countByStatus(all);
  // The chips count what the search leaves, so they always add up to what's shown.
  const chipCounts = countByStatus(searched);

  return (
    <>
      <PageHeader
        title={k.title}
        icon={icon}
        description={
          <>
            <p>{description}</p>
            {all.length > 0 && (
              <p className="mt-1 text-sm opacity-80">
                {counts.up} healthy · {counts.down} down
                {counts.pending ? ` · ${counts.pending} waiting` : ""}
                {counts.paused ? ` · ${counts.paused} paused` : ""}
              </p>
            )}
          </>
        }
        actions={all.length > 0 && <NewButton kind={kind} />}
      />
      <ErrorNote error={query.error} />
      <MonitorsFileNotice />

      {query.isSuccess &&
        (all.length === 0 ? (
          <MonitorEmpty kind={kind} />
        ) : (
          <div className="flex flex-col gap-4">
            {/* One toolbar line: status chips, then search. */}
            <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
              <FilterChips
                label="Status"
                value={status}
                onChange={setStatus}
                options={[
                  { value: "all", label: "All", count: searched.length },
                  { value: "up", label: "Healthy", count: chipCounts.up },
                  { value: "down", label: "Down", count: chipCounts.down },
                  ...(chipCounts.pending
                    ? [{ value: "pending" as const, label: "Waiting", count: chipCounts.pending }]
                    : []),
                  { value: "paused", label: "Paused", count: chipCounts.paused },
                ]}
              />
              <div className="relative w-full sm:max-w-xs">
                <Search className="pointer-events-none absolute top-1/2 left-3 size-4 -translate-y-1/2 text-muted-foreground" />
                <Input
                  aria-label={`Search ${k.title.toLowerCase()}`}
                  placeholder={`Search ${k.title.toLowerCase()}`}
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  className="pl-9"
                />
              </div>
            </div>
            {shown.length ? (
              <div>{render(shown)}</div>
            ) : (
              <p className="py-8 text-center text-sm text-muted-foreground">
                {q ? `Nothing matches “${search}”.` : "Nothing with this status."}
              </p>
            )}
          </div>
        ))}
    </>
  );
}

function NewButton({ kind }: { kind: MonitorKind }) {
  const canEdit = useCanEdit();
  const k = kinds[kind];
  if (!canEdit) return null;
  return (
    <Link to={`${k.path}/new`} className={buttonVariants()}>
      <Plus /> New {k.noun}
    </Link>
  );
}
