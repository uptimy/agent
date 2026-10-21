import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Pencil, Plus, Siren, Trash2 } from "lucide-react";
import { api, type Incident, type IncidentSeverity, type IncidentStatus, type NewIncident } from "@/lib/api";
import { cn, formatDateTime } from "@/lib/utils";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Field, Input, Textarea } from "@/components/ui/input";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { MonitorScope, useAllMonitors } from "@/components/MonitorPicker";
import { useCanEdit } from "@/components/AuthGate";
import {
  SEVERITIES,
  STATUSES,
  SeverityChip,
  StatusChip,
  Timeline,
  incidentDuration,
  severityTheme,
  statusTheme,
} from "@/components/status-page/incidents";

// Incidents are written by people, for the status page. Posting one doesn't
// alert anyone; monitors do that. Announcements live with the status page.

/** The status the next update most likely has; reopening starts over. */
function suggestedStatus(i: Incident): IncidentStatus {
  const next: Record<IncidentStatus, IncidentStatus> = {
    investigating: "identified",
    identified: "monitoring",
    monitoring: "resolved",
    resolved: "investigating",
  };
  return next[i.status];
}

const resolvedMessage = "This incident has been resolved.";

export function IncidentsPage() {
  const incidents = useQuery({ queryKey: ["incidents"], queryFn: api.incidents });
  const [posting, setPosting] = useState(false);
  const canEdit = useCanEdit();

  const list = incidents.data ?? [];
  const groups = [
    { title: "Open", items: list.filter((i) => !i.resolved_at) },
    { title: "Resolved in the last 14 days", items: list.filter((i) => i.resolved_at) },
  ].filter((g) => g.items.length > 0);

  return (
    <>
      <PageHeader
        title="Incidents"
        icon={Siren}
        description="Tell visitors what's going on. Incidents appear on the status page; posting one doesn't alert anyone."
        actions={
          canEdit &&
          !posting && (
            <Button onClick={() => setPosting(true)}>
              <Plus /> Report an incident
            </Button>
          )
        }
      />
      <ErrorNote error={incidents.error} />
      <div className="flex flex-col gap-6">
        {posting && <NewIncidentForm onDone={() => setPosting(false)} />}
        {groups.map((g) => (
          <section key={g.title} className="flex flex-col gap-3">
            <h2 className="text-sm font-medium text-muted-foreground">{g.title}</h2>
            {g.items.map((i) => (
              <IncidentRow key={i.id} incident={i} />
            ))}
          </section>
        ))}
        {incidents.isSuccess && list.length === 0 && !posting && (
          <Card className="px-6 py-12 text-center text-sm text-muted-foreground">
            No incidents. Report one when something&apos;s wrong and visitors should know what you&apos;re doing about
            it. For news like a migration or a new region, post an announcement on the status page.
          </Card>
        )}
      </div>
    </>
  );
}

/** A row of choices, like the heartbeat form's schedule switch. */
function Choice<T extends string>({
  label,
  value,
  options,
  onChange,
  render,
}: {
  label: string;
  value: T;
  options: readonly T[];
  onChange: (v: T) => void;
  render: (v: T) => React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-sm font-medium">{label}</span>
      <div className="flex w-fit flex-wrap gap-1 rounded-md bg-muted p-0.5" role="radiogroup" aria-label={label}>
        {options.map((o) => (
          <button
            key={o}
            type="button"
            role="radio"
            aria-checked={value === o}
            onClick={() => onChange(o)}
            className={cn(
              "inline-flex items-center gap-1.5 rounded px-3 py-1.5 text-sm font-medium",
              value === o ? "bg-card shadow-xs" : "text-muted-foreground",
            )}
          >
            {render(o)}
          </button>
        ))}
      </div>
    </div>
  );
}

const severityOption = (s: IncidentSeverity) => (
  <>
    <span className={cn("size-2 rounded-full", severityTheme[s].dot)} />
    {severityTheme[s].label}
  </>
);
const statusOption = (s: IncidentStatus) => statusTheme[s].label;

function AffectedMonitors({ ids, onChange }: { ids: number[]; onChange: (ids: number[]) => void }) {
  // Kept as "some" once the choice is made, even before a monitor is ticked.
  const [some, setSome] = useState(ids.length > 0);
  return (
    <div className="flex flex-col gap-1.5">
      <span className="text-sm font-medium">Affected monitors</span>
      <MonitorScope
        all={!some}
        ids={ids}
        onChange={(all, next) => {
          setSome(!all);
          onChange(all ? [] : next);
        }}
        allLabel="None in particular"
        someLabel="These monitors"
      />
      <p className="text-xs text-muted-foreground">Only monitors on the status page are named there.</p>
    </div>
  );
}

function NewIncidentForm({ onDone }: { onDone: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<NewIncident>({
    title: "",
    severity: "medium",
    status: "investigating",
    message: "",
    monitor_ids: [],
  });
  const set = <K extends keyof NewIncident>(k: K, v: NewIncident[K]) => setForm((f) => ({ ...f, [k]: v }));
  const save = useMutation({
    mutationFn: () => api.createIncident(form),
    onSuccess: (i) => {
      qc.invalidateQueries({ queryKey: ["incidents"] });
      toast.success("Incident reported", i.title);
      onDone();
    },
  });

  return (
    <Card section>
      <CardHeader>
        <CardTitle>Report an incident</CardTitle>
        <CardDescription>
          Shown on the status page right away. Post updates as you learn more, and resolve it when it&apos;s over.
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
          <Field label="Title" htmlFor="i-title">
            <Input
              id="i-title"
              required
              maxLength={150}
              value={form.title}
              placeholder="Checkout is failing for some customers"
              onChange={(e) => set("title", e.target.value)}
            />
          </Field>
          <div className="flex flex-wrap gap-x-8 gap-y-5">
            <Choice
              label="Severity"
              value={form.severity}
              options={SEVERITIES}
              onChange={(v) => set("severity", v)}
              render={severityOption}
            />
            <Choice
              label="Status"
              value={form.status}
              options={STATUSES.filter((s) => s !== "resolved")}
              onChange={(v) => set("status", v)}
              render={statusOption}
            />
          </div>
          <Field
            label="Message"
            htmlFor="i-message"
            hint="What's happening and what you're doing about it. Visitors see it as written."
          >
            <Textarea
              id="i-message"
              required
              rows={4}
              maxLength={5000}
              value={form.message}
              placeholder="We're seeing failed payments and are looking into it."
              onChange={(e) => set("message", e.target.value)}
            />
          </Field>
          <AffectedMonitors ids={form.monitor_ids} onChange={(ids) => set("monitor_ids", ids)} />
          <ErrorNote error={save.error} />
          <div className="flex justify-end gap-2">
            <Button type="button" variant="ghost" onClick={onDone}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              Report incident
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

function IncidentRow({ incident: inc }: { incident: Incident }) {
  const qc = useQueryClient();
  const canEdit = useCanEdit();
  const { monitors } = useAllMonitors();
  const [editing, setEditing] = useState(false);
  const open = !inc.resolved_at;
  const saved = (i: Incident) =>
    qc.setQueryData<Incident[]>(["incidents"], (l) => l?.map((x) => (x.id === i.id ? i : x)));

  const remove = useMutation({
    mutationFn: () => api.deleteIncident(inc.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["incidents"] });
      toast.success("Incident deleted", inc.title);
    },
  });
  const removeUpdate = useMutation({
    mutationFn: (updateID: number) => api.deleteIncidentUpdate(inc.id, updateID),
    onSuccess: saved,
    onError: (err) => toast.error("Couldn't delete the update", err.message),
  });
  const [editingUpdate, setEditingUpdate] = useState<number | null>(null);

  const names = inc.monitor_ids.map((id) => monitors.find((m) => m.id === id)?.name).filter(Boolean);
  const status = inc.status;

  return (
    <Card className="relative overflow-hidden">
      <span className={cn("absolute inset-y-0 left-0 w-[3px]", statusTheme[status].rail)} />
      <div className="p-5">
        <div className="flex flex-wrap items-start gap-3">
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <StatusChip status={status} />
              <SeverityChip severity={inc.severity} />
            </div>
            <h3 className="mt-2 font-medium">{inc.title}</h3>
            <p className="mt-0.5 text-sm text-muted-foreground">
              {formatDateTime(inc.created_at)}
              {` · ${incidentDuration(inc.created_at, inc.resolved_at)}${open ? " so far" : ""}`}
              {names.length > 0 && ` · ${names.join(", ")}`}
            </p>
          </div>
          {canEdit && !editing && (
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={() => setEditing(true)}>
                Edit
              </Button>
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  confirm({
                    title: "Delete incident",
                    message: (
                      <>
                        <strong className="text-foreground">{inc.title}</strong> and its updates will be removed from
                        the status page. To show it's over instead, resolve it.
                      </>
                    ),
                    confirmLabel: "Delete",
                    action: () => remove.mutateAsync(),
                  })
                }
              >
                Delete
              </Button>
            </div>
          )}
        </div>

        {editing && <EditIncidentForm incident={inc} onDone={() => setEditing(false)} onSaved={saved} />}

        <div className="mt-4 border-t pt-4">
          <Timeline
            updates={inc.updates.map((u) => ({
              status: u.status,
              message: u.message,
              time: u.created_at,
            }))}
            actions={
              canEdit
                ? (i) => {
                    const u = inc.updates[i];
                    return (
                      <span className="flex gap-0.5">
                        <IconButton label="Edit update" onClick={() => setEditingUpdate(u.id)}>
                          <Pencil />
                        </IconButton>
                        {inc.updates.length > 1 && (
                          <IconButton
                            label="Delete update"
                            onClick={() =>
                              confirm({
                                title: "Delete update",
                                message:
                                  i === 0 && u.status === "resolved"
                                    ? "This reopens the incident: the update before it becomes the latest."
                                    : "It's removed from the timeline on the status page.",
                                confirmLabel: "Delete",
                                action: () => removeUpdate.mutateAsync(u.id),
                              })
                            }
                          >
                            <Trash2 />
                          </IconButton>
                        )}
                      </span>
                    );
                  }
                : undefined
            }
          />
          {editingUpdate != null && (
            <EditUpdateForm
              key={editingUpdate}
              incident={inc}
              updateID={editingUpdate}
              onDone={() => setEditingUpdate(null)}
              onSaved={saved}
            />
          )}
        </div>

        {canEdit && open && <AddUpdateForm key={inc.status} incident={inc} onSaved={saved} />}
        {canEdit && !open && <ReopenSection incident={inc} onSaved={saved} />}
      </div>
    </Card>
  );
}

/** A resolved incident can be reopened with a new update, behind a button. */
function ReopenSection({ incident, onSaved }: { incident: Incident; onSaved: (i: Incident) => void }) {
  const [open, setOpen] = useState(false);
  if (!open) {
    return (
      <div className="mt-4 flex justify-end">
        <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
          Reopen
        </Button>
      </div>
    );
  }
  return <AddUpdateForm incident={incident} onSaved={onSaved} />;
}

function IconButton({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={onClick}
      className="rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground [&_svg]:size-3.5"
    >
      {children}
    </button>
  );
}

/** The next update to an incident; on a resolved one, it reopens it. */
function AddUpdateForm({ incident: inc, onSaved }: { incident: Incident; onSaved: (i: Incident) => void }) {
  const reopening = inc.resolved_at != null;
  const initial = suggestedStatus(inc) as IncidentStatus;
  const [status, setStatus] = useState<IncidentStatus>(initial);
  const [message, setMessage] = useState(initial === "resolved" ? resolvedMessage : "");
  const add = useMutation({
    mutationFn: () => api.addIncidentUpdate(inc.id, { status, message }),
    onSuccess: (i) => {
      onSaved(i);
      toast.success(
        status === "resolved" ? "Incident resolved" : reopening ? "Incident reopened" : "Update posted",
        i.title,
      );
    },
  });

  return (
    <form
      className="mt-5 flex flex-col gap-4 rounded-lg border bg-muted/30 p-4"
      onSubmit={(e) => {
        e.preventDefault();
        add.mutate();
      }}
    >
      <Choice
        label={reopening ? "Reopen as" : "New status"}
        value={status}
        options={reopening ? STATUSES.filter((s) => s !== "resolved") : STATUSES}
        onChange={(v) => {
          setStatus(v);
          if (v === "resolved" && !message) setMessage(resolvedMessage);
          if (v !== "resolved" && message === resolvedMessage) setMessage("");
        }}
        render={statusOption}
      />
      <Field label="Update" htmlFor={`u-${inc.id}`}>
        <Textarea
          id={`u-${inc.id}`}
          required
          rows={3}
          maxLength={5000}
          value={message}
          placeholder="We found the cause and are rolling out a fix."
          onChange={(e) => setMessage(e.target.value)}
        />
      </Field>
      <ErrorNote error={add.error} />
      <div className="flex justify-end">
        <Button type="submit" size="sm" disabled={add.isPending}>
          {status === "resolved" ? "Resolve incident" : reopening ? "Reopen" : "Post update"}
        </Button>
      </div>
    </form>
  );
}

function EditUpdateForm({
  incident: inc,
  updateID,
  onDone,
  onSaved,
}: {
  incident: Incident;
  updateID: number;
  onDone: () => void;
  onSaved: (i: Incident) => void;
}) {
  const [message, setMessage] = useState(inc.updates.find((u) => u.id === updateID)?.message ?? "");
  const save = useMutation({
    mutationFn: () => api.editIncidentUpdate(inc.id, updateID, message),
    onSuccess: (i) => {
      onSaved(i);
      onDone();
    },
  });
  return (
    <form
      className="mt-4 flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <Field label="Edit update" htmlFor={`eu-${updateID}`} hint="Fix a typo or add detail. Its time and status stay.">
        <Textarea
          id={`eu-${updateID}`}
          required
          rows={3}
          maxLength={5000}
          value={message}
          onChange={(e) => setMessage(e.target.value)}
        />
      </Field>
      <ErrorNote error={save.error} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={save.isPending}>
          Save
        </Button>
      </div>
    </form>
  );
}

function EditIncidentForm({
  incident: inc,
  onDone,
  onSaved,
}: {
  incident: Incident;
  onDone: () => void;
  onSaved: (i: Incident) => void;
}) {
  const [title, setTitle] = useState(inc.title);
  const [severity, setSeverity] = useState(inc.severity);
  const [ids, setIds] = useState(inc.monitor_ids);
  const save = useMutation({
    mutationFn: () => api.updateIncident(inc.id, { title, severity, monitor_ids: ids }),
    onSuccess: (i) => {
      onSaved(i);
      onDone();
    },
  });
  return (
    <form
      className="mt-4 flex flex-col gap-5 rounded-lg border p-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate();
      }}
    >
      <Field label="Title" htmlFor={`t-${inc.id}`}>
        <Input id={`t-${inc.id}`} required maxLength={150} value={title} onChange={(e) => setTitle(e.target.value)} />
      </Field>
      <Choice label="Severity" value={severity} options={SEVERITIES} onChange={setSeverity} render={severityOption} />
      <AffectedMonitors ids={ids} onChange={setIds} />
      <ErrorNote error={save.error} />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={save.isPending}>
          Save
        </Button>
      </div>
    </form>
  );
}
