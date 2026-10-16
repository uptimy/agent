import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Ban, Check, Clock, LoaderCircle, PauseCircle, RotateCw, X, type LucideIcon } from "lucide-react";
import { api, type Delivery } from "@/lib/api";
import { cn, formatDateTime, timeAgo } from "@/lib/utils";
import { toast } from "@/components/ui/toast";
import { useCanEdit } from "@/components/AuthGate";

const looks: Record<Delivery["state"], { icon: LucideIcon; className: string }> = {
  pending: { icon: Clock, className: "text-paused" },
  held: { icon: PauseCircle, className: "text-muted-foreground" },
  sent: { icon: Check, className: "text-muted-foreground" },
  failed: { icon: X, className: "text-down" },
  expired: { icon: X, className: "text-down" },
  suppressed: { icon: Ban, className: "text-muted-foreground" },
};

/** What happened to an alert on a channel, in a few words. */
export function deliverySummary(d: Delivery): string {
  switch (d.state) {
    case "pending":
      if (d.attempts === 0) return "Sending…";
      return `Retrying${d.next_attempt_at ? ` ${timeAgo(d.next_attempt_at)}` : ""} (${d.attempts} ${d.attempts === 1 ? "attempt" : "attempts"} so far)`;
    case "held":
      return "Held until maintenance ends";
    case "sent":
      return d.attempts > 1 ? `Sent after ${d.attempts} attempts` : "Sent";
    case "failed":
      return "Refused by the channel";
    case "expired":
      return `Gave up after ${d.attempts} ${d.attempts === 1 ? "attempt" : "attempts"}`;
    case "suppressed":
      return "Not sent";
  }
}

/**
 * Under an event, per channel: how its alert went, then any reminders (as
 * one line: how many, and the last one's fate) and the acknowledgement.
 */
export function Deliveries({ deliveries }: { deliveries: Delivery[] }) {
  const byChannel = new Map<number, Delivery[]>();
  for (const d of deliveries) byChannel.set(d.notifier_id, [...(byChannel.get(d.notifier_id) ?? []), d]);
  return (
    <ul className="mt-1.5 flex flex-col gap-1">
      {[...byChannel.values()].flatMap((ds) => {
        const lines = [];
        const change = ds.find((d) => !d.alert_type);
        if (change) lines.push(<DeliveryLine key={`${change.notifier_id}`} d={change} />);
        const reminders = ds.filter((d) => d.alert_type === "reminder");
        if (reminders.length) {
          const last = reminders[reminders.length - 1];
          const sent = reminders.filter((d) => d.state === "sent").length;
          lines.push(
            <DeliveryLine
              key={`${last.notifier_id}-reminders`}
              d={last}
              // "Reminded 3 times · 13:00", or how the latest one is doing.
              label={
                last.state === "sent"
                  ? `Reminded ${sent} ${sent === 1 ? "time" : "times"}`
                  : `Reminder${sent ? ` (${sent} sent before)` : ""}: ${lower(deliverySummary(last))}`
              }
            />,
          );
        }
        for (const ack of ds.filter((d) => d.alert_type === "acknowledged")) {
          lines.push(
            <DeliveryLine
              key={`${ack.alert_id}-${ack.notifier_id}`}
              d={ack}
              label={ack.state === "sent" ? "Acknowledgement sent" : `Acknowledgement: ${lower(deliverySummary(ack))}`}
            />,
          );
        }
        return lines;
      })}
    </ul>
  );
}

const lower = (s: string) => s.charAt(0).toLowerCase() + s.slice(1);

/** One channel's line; label replaces the usual summary ("Sent", "Retrying …"). */
function DeliveryLine({ d, label }: { d: Delivery; label?: string }) {
  const { icon: Icon, className } = looks[d.state];
  const retrying = d.state === "pending" && d.attempts > 0;
  const explain = d.detail && d.state !== "sent" ? d.detail : "";
  return (
    <li className="flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground">
      <Icon className={cn("size-3.5 shrink-0", retrying ? "text-paused" : className)} aria-hidden />
      <span className="font-medium text-foreground">{d.notifier_name}</span>
      <span className={cn((d.state === "failed" || d.state === "expired") && "text-down")}>
        {label ?? deliverySummary(d)}
        {explain && <>: {explain}</>}
      </span>
      {d.done_at && d.state === "sent" && <time dateTime={d.done_at}>· {formatDateTime(d.done_at)}</time>}
      {(d.state === "failed" || d.state === "expired" || retrying) && <RetryButton d={d} />}
    </li>
  );
}

/** Sends the alert to its channel again, or now instead of at the next retry. */
export function RetryButton({ d }: { d: Delivery }) {
  const qc = useQueryClient();
  const canEdit = useCanEdit();
  const retry = useMutation({
    mutationFn: () => api.retryDelivery(d),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["events"] });
      qc.invalidateQueries({ queryKey: ["notifiers"] });
    },
    onError: (e) => toast.error("Couldn't retry", e instanceof Error ? e.message : String(e)),
  });
  if (!canEdit) return null;
  return (
    <button
      type="button"
      onClick={() => retry.mutate()}
      disabled={retry.isPending}
      className="inline-flex items-center gap-1 rounded px-1 font-medium text-foreground underline-offset-2 hover:underline disabled:opacity-60"
    >
      {retry.isPending ? <LoaderCircle className="size-3 animate-spin" /> : <RotateCw className="size-3" />}
      {d.state === "pending" ? "Retry now" : "Retry"}
    </button>
  );
}
