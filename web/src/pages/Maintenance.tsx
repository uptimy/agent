import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, Wrench } from "lucide-react";
import { api, type MaintenanceInput, type MaintenanceWindow } from "@/lib/api";
import { cn, formatDateTime, fromLocalInput, timeAgo, toLocalInput } from "@/lib/utils";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Field, Input, Switch, Textarea } from "@/components/ui/input";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { MonitorScope } from "@/components/MonitorPicker";
import { useCanEdit } from "@/components/AuthGate";

// Planned work: monitors in a window don't page anyone while it runs. If one
// is still down when the window ends, the alert goes out then.

const durations = [
  { label: "30 min", minutes: 30 },
  { label: "1 hour", minutes: 60 },
  { label: "2 hours", minutes: 120 },
  { label: "4 hours", minutes: 240 },
  { label: "1 day", minutes: 1440 },
];

function blank(): MaintenanceInput {
  const start = new Date();
  start.setSeconds(0, 0);
  return {
    title: "",
    description: "",
    starts_at: start.toISOString(),
    ends_at: new Date(start.getTime() + 60 * 60_000).toISOString(),
    all_monitors: true,
    monitor_ids: [],
    public: true,
  };
}

export function MaintenancePage() {
  const windows = useQuery({ queryKey: ["maintenance"], queryFn: api.maintenance });
  const [editing, setEditing] = useState<MaintenanceWindow | "new" | null>(null);
  const canEdit = useCanEdit();

  const list = windows.data ?? [];
  const groups = [
    { title: "In progress", items: list.filter((w) => w.state === "active") },
    { title: "Scheduled", items: list.filter((w) => w.state === "scheduled") },
    { title: "Recently ended", items: list.filter((w) => w.state === "ended").reverse() },
  ].filter((g) => g.items.length > 0);

  return (
    <>
      <PageHeader
        title="Maintenance"
        icon={Wrench}
        description="Planned work doesn't page anyone. If a monitor is still down when the window ends, you're alerted then."
        actions={
          canEdit &&
          editing == null && (
            <Button onClick={() => setEditing("new")}>
              <Plus /> Schedule maintenance
            </Button>
          )
        }
      />
      <ErrorNote error={windows.error} />
      <div className="flex flex-col gap-6">
        {editing && (
          <MaintenanceForm
            key={editing === "new" ? "new" : editing.id}
            initial={editing === "new" ? null : editing}
            onDone={() => setEditing(null)}
          />
        )}
        {groups.map((g) => (
          <section key={g.title} className="flex flex-col gap-3">
            <h2 className="text-sm font-medium text-muted-foreground">{g.title}</h2>
            {g.items.map((w) => (
              <MaintenanceRow key={w.id} w={w} onEdit={() => setEditing(w)} />
            ))}
          </section>
        ))}
        {windows.isSuccess && list.length === 0 && !editing && (
          <Card className="px-6 py-12 text-center text-sm text-muted-foreground">
            No maintenance planned. Schedule a window before deploys, upgrades or migrations so they don&apos;t wake
            anyone up.
          </Card>
        )}
      </div>
    </>
  );
}

const stateBadge: Record<MaintenanceWindow["state"], { label: string; className: string }> = {
  active: { label: "In progress", className: "bg-sky-500/15 text-sky-600 dark:text-sky-400" },
  scheduled: { label: "Scheduled", className: "" },
  ended: { label: "Ended", className: "" },
};

function MaintenanceRow({ w, onEdit }: { w: MaintenanceWindow; onEdit: () => void }) {
  const qc = useQueryClient();
  const canEdit = useCanEdit();
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["maintenance"] });
    qc.invalidateQueries({ queryKey: ["healthchecks"] });
    qc.invalidateQueries({ queryKey: ["heartbeats"] });
  };
  const end = useMutation({
    mutationFn: () => api.endMaintenance(w.id),
    onSuccess: () => {
      refresh();
      toast.success("Maintenance ended", "Monitors that are still down will alert now.");
    },
    onError: (err) => toast.error("Couldn't end the maintenance", err.message),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteMaintenance(w.id),
    onSuccess: () => {
      refresh();
      toast.success(w.state === "scheduled" ? "Maintenance canceled" : "Maintenance deleted", w.title);
    },
  });
  const badge = stateBadge[w.state];
  const covers = w.all_monitors
    ? "All monitors"
    : `${w.monitor_ids.length} monitor${w.monitor_ids.length === 1 ? "" : "s"}`;

  return (
    <Card className={cn("p-5", w.state === "active" && "border-sky-500/50")}>
      <div className="flex flex-wrap items-start gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{w.title}</span>
            <Badge className={badge.className}>{badge.label}</Badge>
            {w.public && <Badge>On status page</Badge>}
          </div>
          <div className="mt-1 text-sm text-muted-foreground">
            {formatDateTime(w.starts_at)} – {formatDateTime(w.ends_at)} · {covers}
            {w.state === "active" && ` · ends ${timeAgo(w.ends_at)}`}
            {w.state === "scheduled" && ` · starts ${timeAgo(w.starts_at)}`}
          </div>
          {w.description && <p className="mt-2 text-sm whitespace-pre-line">{w.description}</p>}
        </div>
        {canEdit && (
          <div className="flex gap-2">
            {w.state === "active" && (
              <Button variant="outline" size="sm" onClick={() => end.mutate()} disabled={end.isPending}>
                End now
              </Button>
            )}
            {w.state !== "ended" && (
              <Button variant="outline" size="sm" onClick={onEdit}>
                Edit
              </Button>
            )}
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                confirm({
                  title: w.state === "scheduled" ? "Cancel maintenance" : "Delete maintenance",
                  message:
                    w.state === "active" ? (
                      <>
                        <strong className="text-foreground">{w.title}</strong> is in progress. Deleting it ends it now,
                        and monitors that are still down alert right away.
                      </>
                    ) : (
                      <>
                        <strong className="text-foreground">{w.title}</strong> will be removed
                        {w.public && " from the status page"}.
                      </>
                    ),
                  confirmLabel: w.state === "scheduled" ? "Cancel maintenance" : "Delete",
                  action: () => remove.mutateAsync(),
                })
              }
            >
              {w.state === "scheduled" ? "Cancel" : "Delete"}
            </Button>
          </div>
        )}
      </div>
    </Card>
  );
}

function MaintenanceForm({ initial, onDone }: { initial: MaintenanceWindow | null; onDone: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<MaintenanceInput>(() => (initial ? { ...initial } : blank()));
  const set = <K extends keyof MaintenanceInput>(k: K, v: MaintenanceInput[K]) => setForm((f) => ({ ...f, [k]: v }));
  const save = useMutation({
    mutationFn: () => (initial ? api.updateMaintenance(initial.id, form) : api.createMaintenance(form)),
    onSuccess: (w) => {
      qc.invalidateQueries({ queryKey: ["maintenance"] });
      qc.invalidateQueries({ queryKey: ["healthchecks"] });
      qc.invalidateQueries({ queryKey: ["heartbeats"] });
      toast.success(initial ? "Maintenance saved" : "Maintenance scheduled", w.title);
      onDone();
    },
  });
  const lengthMinutes = (new Date(form.ends_at).getTime() - new Date(form.starts_at).getTime()) / 60_000;
  const started = initial?.state === "active";

  return (
    <Card section>
      <CardHeader>
        <CardTitle>{initial ? "Edit maintenance" : "Schedule maintenance"}</CardTitle>
        <CardDescription>
          Monitors it covers keep being checked and their history is kept; they just don&apos;t alert.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-5"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <Field label="Title" htmlFor="m-title">
            <Input
              id="m-title"
              required
              maxLength={100}
              value={form.title}
              placeholder="Database upgrade"
              onChange={(e) => set("title", e.target.value)}
            />
          </Field>
          <Field label="Description" htmlFor="m-description" hint="Optional. Shown on the status page if it's public.">
            <Textarea
              id="m-description"
              maxLength={1000}
              value={form.description}
              placeholder="We're upgrading to Postgres 17. The API may be unavailable for a few minutes."
              onChange={(e) => set("description", e.target.value)}
            />
          </Field>

          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Starts" htmlFor="m-start">
              <Input
                id="m-start"
                type="datetime-local"
                required
                disabled={started}
                value={toLocalInput(form.starts_at)}
                onChange={(e) => {
                  if (!e.target.value) return;
                  const start = fromLocalInput(e.target.value);
                  // Keep the length when the start moves.
                  const end = new Date(new Date(start).getTime() + Math.max(lengthMinutes, 1) * 60_000).toISOString();
                  setForm((f) => ({ ...f, starts_at: start, ends_at: end }));
                }}
              />
            </Field>
            <Field label="Ends" htmlFor="m-end">
              <Input
                id="m-end"
                type="datetime-local"
                required
                value={toLocalInput(form.ends_at)}
                onChange={(e) => e.target.value && set("ends_at", fromLocalInput(e.target.value))}
              />
            </Field>
          </div>
          <div className="-mt-2 flex flex-wrap gap-1.5">
            {durations.map((d) => (
              <button
                key={d.minutes}
                type="button"
                onClick={() =>
                  set("ends_at", new Date(new Date(form.starts_at).getTime() + d.minutes * 60_000).toISOString())
                }
                className={cn(
                  "rounded-full border px-2.5 py-1 text-xs transition-colors",
                  Math.round(lengthMinutes) === d.minutes ? "border-primary bg-primary/5" : "hover:bg-muted",
                )}
              >
                {d.label}
              </button>
            ))}
          </div>

          <MonitorScope
            all={form.all_monitors}
            ids={form.monitor_ids}
            onChange={(all, ids) => setForm((f) => ({ ...f, all_monitors: all, monitor_ids: ids }))}
            allLabel="All monitors"
            someLabel="Only the monitors I choose"
          />
          <Switch
            id="m-public"
            checked={form.public}
            onChange={(v) => set("public", v)}
            label="Announce it on the status page"
          />
          <ErrorNote error={save.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onDone}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {initial ? "Save" : "Schedule"}
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}
