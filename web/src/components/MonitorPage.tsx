import { Link, useNavigate } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Eye, Pause, Pencil, Play, Trash2, type LucideIcon } from "lucide-react";
import { api, type Acknowledgement, type Monitor, type MonitorEvent, type Status } from "@/lib/api";
import { formatDateTime } from "@/lib/utils";
import { kinds, monitorPath } from "@/lib/monitors";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { StatusDot } from "@/components/status";
import { useCanEdit } from "@/components/AuthGate";
import { Deliveries } from "@/components/Deliveries";

// What a healthcheck's page and a heartbeat's page have in common.

/** "← Healthchecks" above a monitor's page. */
export function BackLink({ m }: { m: Pick<Monitor, "kind"> }) {
  const k = kinds[m.kind];
  return (
    <Link
      to={k.path}
      className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
    >
      <ArrowLeft className="size-4" /> {k.title}
    </Link>
  );
}

/** Pause or resume, edit and delete. Editing and deleting are left to the file for monitors defined there. */
export function MonitorActions({
  m,
  children,
}: {
  m: Monitor & { status: Status; acknowledged: Acknowledgement | null };
  children?: React.ReactNode;
}) {
  const canEdit = useCanEdit();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const k = kinds[m.kind];

  const pause = useMutation({
    mutationFn: (paused: boolean) => api.pauseMonitor(m.kind, m.id, paused),
    onSuccess: (_, paused) => {
      qc.invalidateQueries();
      toast.success(paused ? `${capitalize(k.noun)} paused` : `${capitalize(k.noun)} resumed`, m.name);
    },
    onError: (err) => toast.error(`Couldn't ${m.paused ? "resume" : "pause"} ${m.name}`, err.message),
  });
  const acknowledge = useMutation({
    mutationFn: () => api.acknowledge(m.kind, m.id),
    onSuccess: () => {
      qc.invalidateQueries();
      toast.success("Acknowledged", `No more reminders while ${m.name} is down. Your channels hear you're on it.`);
    },
    onError: (err) => toast.error(`Couldn't acknowledge ${m.name}`, err.message),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteMonitor(m.kind, m.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: [`${m.kind}s`] });
      qc.invalidateQueries({ queryKey: ["activity"] });
      toast.success(`${capitalize(k.noun)} deleted`, m.name);
      navigate(k.path);
    },
  });

  if (!canEdit) return null;
  const managed = m.source !== "ui";
  const history = m.kind === "heartbeat" ? "run history" : "check history";
  return (
    <>
      {m.status === "down" && !m.acknowledged && (
        <Button onClick={() => acknowledge.mutate()} disabled={acknowledge.isPending}>
          <Eye /> Acknowledge
        </Button>
      )}
      {children}
      <Button variant="outline" onClick={() => pause.mutate(!m.paused)} disabled={pause.isPending}>
        {m.paused ? <Play /> : <Pause />} {m.paused ? "Resume" : "Pause"}
      </Button>
      {!managed && (
        <>
          <Link to={`${monitorPath(m)}/edit`} className={buttonVariants({ variant: "outline" })}>
            <Pencil /> Edit
          </Link>
          <Button
            variant="outline"
            size="icon"
            aria-label={`Delete ${k.noun}`}
            title={`Delete ${k.noun}`}
            onClick={() =>
              confirm({
                title: `Delete ${k.noun}`,
                message: (
                  <>
                    This permanently deletes <strong className="text-foreground">{m.name}</strong> and all of its{" "}
                    {history}.{m.kind === "heartbeat" && " Its ping URL stops working."}
                    {m.public && " It also disappears from the status page."}
                  </>
                ),
                confirmLabel: "Delete forever",
                typeToConfirm: m.name,
                action: () => remove.mutateAsync(),
              })
            }
          >
            <Trash2 />
          </Button>
        </>
      )}
    </>
  );
}

/** One figure in the row of stats at the top of a monitor's page. */
export function Stat({ label, value, sub }: { label: string; value: React.ReactNode; sub?: string }) {
  return (
    <Card className="p-5">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className="mt-2 text-xl font-semibold tabular-nums">{value}</div>
      {sub && (
        <div className="mt-1 truncate text-xs text-muted-foreground" title={sub}>
          {sub}
        </div>
      )}
    </Card>
  );
}

/** A monitor's status changes, newest first. */
export function EventsCard({ id, describe }: { id: number; describe: (e: MonitorEvent) => string }) {
  const events = useQuery({ queryKey: ["events", id], queryFn: () => api.events(id) });
  return (
    <Card className="mt-6">
      <CardHeader>
        <CardTitle>Events</CardTitle>
      </CardHeader>
      <CardContent>
        <ErrorNote error={events.error} />
        {events.data?.length ? (
          <ol className="divide-y">
            {events.data.map((e) => (
              <li key={e.id} className="flex items-start gap-3 py-3 text-sm">
                <StatusDot status={e.status} className="mt-1.5" />
                <div className="min-w-0 flex-1 break-words">
                  {describe(e)}
                  {e.deliveries && e.deliveries.length > 0 && <Deliveries deliveries={e.deliveries} />}
                </div>
                <time className="shrink-0 text-xs text-muted-foreground" dateTime={e.time}>
                  {formatDateTime(e.time)}
                </time>
              </li>
            ))}
          </ol>
        ) : (
          events.isSuccess && <p className="text-sm text-muted-foreground">No status changes yet.</p>
        )}
      </CardContent>
    </Card>
  );
}

const capitalize = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);

/** The frame of the healthcheck and heartbeat forms: a back link, the title and a card holding the fields. */
export function FormShell({
  kind,
  editing,
  icon,
  description,
  error,
  submitting,
  canSubmit = true,
  onSubmit,
  children,
}: {
  kind: Monitor["kind"];
  editing: number | null;
  icon: LucideIcon;
  description?: React.ReactNode;
  error: unknown;
  submitting: boolean;
  canSubmit?: boolean;
  onSubmit: () => void;
  children: React.ReactNode;
}) {
  const k = kinds[kind];
  return (
    <div className="mx-auto max-w-2xl">
      <Link
        to={editing ? `${k.path}/${editing}` : k.path}
        className="mb-4 inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground"
      >
        <ArrowLeft className="size-4" /> Back
      </Link>
      <PageHeader title={editing ? `Edit ${k.noun}` : `New ${k.noun}`} icon={icon} description={description} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onSubmit();
        }}
      >
        <Card>
          <CardContent className="flex flex-col gap-6 pt-5">
            {children}
            <ErrorNote error={error} />
            <div className="flex justify-end gap-2">
              <Button type="submit" disabled={submitting || !canSubmit}>
                {editing ? "Save changes" : `Create ${k.noun}`}
              </Button>
            </div>
          </CardContent>
        </Card>
      </form>
    </div>
  );
}
