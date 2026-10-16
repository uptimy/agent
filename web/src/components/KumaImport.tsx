import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ArrowRightLeft, Upload } from "lucide-react";
import { api, type KumaImportResult, type KumaPlan } from "@/lib/api";
import { useChannelLabel, useCheckTypeLabel } from "@/lib/types";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { ErrorNote } from "@/components/Layout";
import { ImportResult, Item, ItemList, SkippedList } from "@/components/ImportReview";

/**
 * Import from Uptime Kuma: upload kuma.db, review what it becomes, choose
 * what to import. Nothing is created before the last step.
 */
export function KumaImportCard() {
  const [plan, setPlan] = useState<KumaPlan | null>(null);
  const [result, setResult] = useState<KumaImportResult | null>(null);
  const reset = () => {
    setPlan(null);
    setResult(null);
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <ArrowRightLeft className="size-4" /> Import from Uptime Kuma
        </CardTitle>
        <CardDescription>
          Bring over your monitors and notifications, including which monitors alert where. You review everything before
          it&apos;s imported.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {result ? (
          <ImportResult
            result={result}
            onDone={reset}
            heartbeatsTitle="Point your jobs at the new ping URLs"
            heartbeatsHint="Kuma's push URLs don't work here. Each heartbeat has its own:"
          />
        ) : plan ? (
          <PlanReview plan={plan} onCancel={reset} onImported={setResult} />
        ) : (
          <ChooseFile onPlan={setPlan} />
        )}
      </CardContent>
    </Card>
  );
}

function ChooseFile({ onPlan }: { onPlan: (p: KumaPlan) => void }) {
  const input = useRef<HTMLInputElement>(null);
  const read = useMutation({ mutationFn: (f: File) => api.planKumaImport(f), onSuccess: onPlan });
  return (
    <div className="flex flex-col gap-3 text-sm">
      <p className="text-muted-foreground">
        Upload Kuma&apos;s database, <code>kuma.db</code>. It&apos;s in Kuma&apos;s data folder (<code>/app/data</code>{" "}
        in Docker). Stop Kuma first so the copy is complete, e.g.{" "}
        <code>docker stop uptime-kuma && docker cp uptime-kuma:/app/data/kuma.db .</code>
      </p>
      <input
        ref={input}
        type="file"
        accept=".db,.sqlite,application/octet-stream"
        className="hidden"
        onChange={(e) => {
          const f = e.target.files?.[0];
          if (f) read.mutate(f);
          e.target.value = "";
        }}
      />
      <div>
        <Button variant="outline" onClick={() => input.current?.click()} disabled={read.isPending}>
          <Upload /> {read.isPending ? "Reading kuma.db…" : "Choose kuma.db"}
        </Button>
      </div>
      <ErrorNote error={read.error} />
    </div>
  );
}

function PlanReview({
  plan,
  onCancel,
  onImported,
}: {
  plan: KumaPlan;
  onCancel: () => void;
  onImported: (r: KumaImportResult) => void;
}) {
  const qc = useQueryClient();
  const typeLabel = useCheckTypeLabel();
  const channelLabel = useChannelLabel();
  const [monitors, setMonitors] = useState(() => new Set(plan.monitors.map((m) => m.kuma_id)));
  const [notifiers, setNotifiers] = useState(() => new Set(plan.notifiers.map((n) => n.kuma_id)));
  const toggle = (set: Set<number>, update: (s: Set<number>) => void, id: number) => {
    const next = new Set(set);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    update(next);
  };
  const apply = useMutation({
    mutationFn: () =>
      api.applyKumaImport({
        sections: plan.sections ?? [],
        monitors: plan.monitors.filter((m) => monitors.has(m.kuma_id)),
        notifiers: plan.notifiers.filter((n) => notifiers.has(n.kuma_id)),
      }),
    onSuccess: (r) => {
      qc.invalidateQueries();
      onImported(r);
    },
  });
  const count = monitors.size + notifiers.size;

  return (
    <div className="flex flex-col gap-5">
      {plan.monitors.length > 0 && (
        <ItemList title={`Monitors (${plan.monitors.length})`}>
          {plan.monitors.map((m) => (
            <Item
              key={m.kuma_id}
              checked={monitors.has(m.kuma_id)}
              onToggle={() => toggle(monitors, setMonitors, m.kuma_id)}
              name={m.monitor.name}
              badge={m.monitor.kind === "heartbeat" ? "Heartbeat" : typeLabel(m.monitor.check?.type ?? "")}
              notes={m.notes}
              extra={m.monitor.paused ? "paused" : undefined}
            />
          ))}
        </ItemList>
      )}
      {plan.notifiers.length > 0 && (
        <ItemList title={`Notifications (${plan.notifiers.length})`}>
          {plan.notifiers.map((n) => (
            <Item
              key={n.kuma_id}
              checked={notifiers.has(n.kuma_id)}
              onToggle={() => toggle(notifiers, setNotifiers, n.kuma_id)}
              name={n.notifier.name}
              badge={channelLabel(n.notifier.type)}
              notes={n.notes}
              extra={[
                n.notifier.all_monitors
                  ? "every monitor"
                  : `${n.kuma_monitor_ids?.length ?? 0} monitor${n.kuma_monitor_ids?.length === 1 ? "" : "s"}`,
                !n.notifier.enabled && "disabled",
              ]
                .filter(Boolean)
                .join(" · ")}
            />
          ))}
        </ItemList>
      )}
      {plan.skipped.length > 0 && <SkippedList items={plan.skipped} title="Can't be imported" />}
      {plan.monitors.length + plan.notifiers.length === 0 && (
        <p className="text-sm text-muted-foreground">Nothing in this database can be imported.</p>
      )}

      <ErrorNote error={apply.error} />
      <div className="flex justify-end gap-2">
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button onClick={() => apply.mutate()} disabled={count === 0 || apply.isPending}>
          {apply.isPending ? "Importing…" : `Import ${count} item${count === 1 ? "" : "s"}`}
        </Button>
      </div>
    </div>
  );
}
