import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, Plus, Send, Trash2 } from "lucide-react";
import { api, type Notifier, type NotifierInput } from "@/lib/api";
import { useChannelLabel, useNotifierChannels } from "@/lib/types";
import { SchemaFields } from "@/components/SchemaFields";
import { MonitorScope } from "@/components/MonitorPicker";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Field, Input, Select, Switch } from "@/components/ui/input";
import { ErrorNote, PageHeader } from "@/components/Layout";
import { useCanEdit } from "@/components/AuthGate";
import { RetryButton } from "@/components/Deliveries";
import { cn, formatInterval, timeAgo } from "@/lib/utils";

const blank: NotifierInput = {
  name: "",
  type: "slack",
  enabled: true,
  config: {},
  all_monitors: true,
  monitor_ids: [],
  repeat_seconds: 0,
};

/** "Remind while down" choices, in seconds; 0 is off. */
const repeatChoices = [
  { seconds: 0, label: "Don't remind" },
  { seconds: 15 * 60, label: "Every 15 minutes" },
  { seconds: 30 * 60, label: "Every 30 minutes" },
  { seconds: 60 * 60, label: "Every hour" },
  { seconds: 4 * 60 * 60, label: "Every 4 hours" },
  { seconds: 24 * 60 * 60, label: "Once a day" },
];

function repeatLabel(seconds: number) {
  const choice = repeatChoices.find((c) => c.seconds === seconds);
  return choice ? choice.label.toLowerCase() : `every ${formatInterval(seconds)}`;
}

export function Notifications() {
  const notifiers = useQuery({ queryKey: ["notifiers"], queryFn: api.notifiers });
  const channels = useNotifierChannels();
  const [editing, setEditing] = useState<Notifier | "new" | null>(null);
  const canEdit = useCanEdit();

  return (
    <>
      <PageHeader
        title="Notifications"
        icon={Bell}
        description="Where alerts go when a monitor goes down or recovers."
        actions={
          canEdit &&
          editing == null && (
            <Button onClick={() => setEditing("new")}>
              <Plus /> Add channel
            </Button>
          )
        }
      />
      <ErrorNote error={notifiers.error} />
      <div className="flex flex-col gap-4">
        {editing && (
          <NotifierForm
            key={editing === "new" ? "new" : editing.id}
            initial={editing === "new" ? null : editing}
            onDone={() => setEditing(null)}
          />
        )}
        {notifiers.data?.map((n) => (
          <NotifierRow key={n.id} n={n} onEdit={() => setEditing(n)} />
        ))}
        {notifiers.isSuccess && notifiers.data.length === 0 && !editing && (
          <Card className="px-6 py-12 text-center text-sm text-muted-foreground">
            {canEdit
              ? `No channels yet. Add ${listOf(channels.data?.map((c) => c.label) ?? [])} to get alerted.`
              : "No alert channels yet."}
          </Card>
        )}
      </div>
    </>
  );
}

function NotifierRow({ n, onEdit }: { n: Notifier; onEdit: () => void }) {
  const qc = useQueryClient();
  const channelLabel = useChannelLabel();
  const canEdit = useCanEdit();
  const test = useMutation({
    mutationFn: () => api.testNotifier(n.id),
    onSuccess: () => toast.success("Test alert sent", `Check ${n.name} for the message.`),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteNotifier(n.id),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifiers"] });
      toast.success("Channel deleted", n.name);
    },
  });
  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-center gap-3">
        <div className="min-w-48 flex-1">
          <div className="flex items-center gap-2">
            <span className="font-medium">{n.name}</span>
            <Badge>{channelLabel(n.type)}</Badge>
            {!n.enabled && <Badge>Disabled</Badge>}
          </div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            {n.all_monitors
              ? "Every monitor"
              : n.monitor_ids.length === 0
                ? "No monitors yet: edit it to choose some"
                : `${n.monitor_ids.length} monitor${n.monitor_ids.length === 1 ? "" : "s"}`}
            {n.repeat_seconds > 0 && <> · reminds {repeatLabel(n.repeat_seconds)} while down</>}
          </div>
          <ChannelHealthLine n={n} />
        </div>
        {canEdit && (
          <div className="flex items-center gap-2">
            <Button variant="outline" size="sm" onClick={() => test.mutate()} disabled={test.isPending}>
              <Send /> {test.isPending ? "Sending…" : "Send test"}
            </Button>
            <Button variant="outline" size="sm" onClick={onEdit}>
              Edit
            </Button>
            <Button
              variant="ghost"
              size="icon"
              aria-label="Delete channel"
              onClick={() =>
                confirm({
                  title: "Delete channel",
                  message: (
                    <>
                      <strong className="text-foreground">{n.name}</strong> stops getting alerts. You can add it again
                      later.
                    </>
                  ),
                  confirmLabel: "Delete",
                  action: () => remove.mutateAsync(),
                })
              }
            >
              <Trash2 />
            </Button>
          </div>
        )}
      </div>
      {test.error && (
        <div className="mt-3">
          <ErrorNote error={test.error} />
        </div>
      )}
    </Card>
  );
}

function NotifierForm({ initial, onDone }: { initial: Notifier | null; onDone: () => void }) {
  const qc = useQueryClient();
  const [form, setForm] = useState<NotifierInput>(
    initial
      ? {
          name: initial.name,
          type: initial.type,
          enabled: initial.enabled,
          config: initial.config,
          all_monitors: initial.all_monitors,
          monitor_ids: initial.monitor_ids,
          repeat_seconds: initial.repeat_seconds,
        }
      : blank,
  );
  const save = useMutation({
    mutationFn: () => (initial ? api.updateNotifier(initial.id, form) : api.createNotifier(form)),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["notifiers"] });
      onDone();
    },
  });
  const channels = useNotifierChannels();
  const channel = channels.data?.find((c) => c.type === form.type);

  return (
    <Card>
      <CardHeader>
        <CardTitle>{initial ? "Edit channel" : "New channel"}</CardTitle>
        <CardDescription>{channel?.help}</CardDescription>
      </CardHeader>
      <CardContent>
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field label="Name" htmlFor="n-name">
              <Input
                id="n-name"
                required
                value={form.name}
                placeholder="#alerts"
                onChange={(e) => setForm({ ...form, name: e.target.value })}
              />
            </Field>
            <Field label="Type" htmlFor="n-type">
              <Select
                id="n-type"
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value, config: {} })}
              >
                {channels.data?.map((c) => (
                  <option key={c.type} value={c.type}>
                    {c.label}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          {channel && (
            <SchemaFields
              idPrefix="n"
              fields={channel.fields}
              values={form.config}
              onChange={(key, value) => setForm((f) => ({ ...f, config: { ...f.config, [key]: value } }))}
            />
          )}
          <MonitorScope
            all={form.all_monitors}
            ids={form.monitor_ids}
            onChange={(all, ids) => setForm((f) => ({ ...f, all_monitors: all, monitor_ids: ids }))}
            allLabel="Alert for every monitor, including new ones"
            someLabel="Only for the monitors I choose"
          />
          {channel?.reminders && (
            <Field label="Remind while down" htmlFor="n-repeat" hint="Until someone acknowledges it or it recovers.">
              <Select
                id="n-repeat"
                value={form.repeat_seconds}
                onChange={(e) => setForm({ ...form, repeat_seconds: Number(e.target.value) })}
              >
                {[
                  ...repeatChoices,
                  ...(repeatChoices.some((c) => c.seconds === form.repeat_seconds)
                    ? []
                    : [{ seconds: form.repeat_seconds, label: repeatLabel(form.repeat_seconds) }]),
                ].map((c) => (
                  <option key={c.seconds} value={c.seconds}>
                    {c.label}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Switch
            id="n-enabled"
            checked={form.enabled}
            onChange={(v) => setForm({ ...form, enabled: v })}
            label="Enabled"
          />
          <ErrorNote error={save.error} />
          <div className="flex justify-end gap-2">
            <Button variant="ghost" onClick={onDone}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              Save
            </Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

/** How the channel's recent alerts went: the last one, failures and anything waiting. */
function ChannelHealthLine({ n }: { n: Notifier }) {
  const { last, failed, pending } = n.health;
  const lastFailed = last != null && (last.state === "failed" || last.state === "expired");
  const parts: React.ReactNode[] = [];
  if (last?.done_at) {
    parts.push(
      <span key="last" className={cn(lastFailed && "text-down")}>
        {lastFailed ? "Last alert not delivered" : "Last alert sent"} {timeAgo(last.done_at)}
        {lastFailed && last.detail && <>: {last.detail}</>}
        {lastFailed && <RetryButton d={last} />}
      </span>,
    );
  }
  if (failed > (lastFailed ? 1 : 0)) {
    parts.push(
      <span key="failed" className="text-down">
        {failed} not delivered in the last 24 hours
      </span>,
    );
  }
  if (pending > 0) {
    parts.push(
      <span key="pending" className="text-paused">
        {pending} waiting to be sent
      </span>,
    );
  }
  if (parts.length === 0) return null;
  return (
    <div className="mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
      {parts.flatMap((p, i) => (i === 0 ? [p] : [<span key={`sep${i}`}>·</span>, p]))}
    </div>
  );
}

/** ["Slack", "Discord", "Webhook"] → "Slack, Discord or Webhook". */
function listOf(items: string[]) {
  return items.length < 2 ? (items[0] ?? "a channel") : `${items.slice(0, -1).join(", ")} or ${items.at(-1)}`;
}
