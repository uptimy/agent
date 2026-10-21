import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { BellOff, Clock, Play, Wrench } from "lucide-react";
import { api, type UptimyAlerting } from "@/lib/api";
import { cn, formatClock, formatMinutes } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { ErrorNote } from "@/components/Layout";

// When Uptimy alerts about this agent, and how to quiet it for planned work.
// Shown in the Watch the watcher card for a "Connect to Uptimy" connection.
//
// A maintenance window doesn't pause the heartbeat: it lets the agent be
// silent for longer, so if the agent never comes back Uptimy still alerts
// once the window has passed. Pausing is there too, but warns that nothing
// alerts until it's resumed.

export function useUptimyAlerting(enabled: boolean) {
  return useQuery({ queryKey: ["uptimy-alerting"], queryFn: api.uptimyAlerting, enabled });
}

export function UptimyAlertingSettings({ canEdit }: { canEdit: boolean }) {
  const qc = useQueryClient();
  const settings = useUptimyAlerting(true);

  const apply = (s: UptimyAlerting) => {
    qc.setQueryData(["uptimy-alerting"], s);
    qc.invalidateQueries({ queryKey: ["uptimy"] }); // paused shows in the status
  };
  const setAlertAfter = useMutation({
    mutationFn: (minutes: number) => api.updateUptimyAlerting({ alert_after_seconds: minutes * 60 }),
    onSuccess: (s, minutes) => {
      apply(s);
      toast.success(`Alerts after ${formatMinutes(minutes)} of silence`);
    },
  });
  const setPaused = useMutation({
    mutationFn: (paused: boolean) => api.updateUptimyAlerting({ paused }),
    onSuccess: (s) => {
      apply(s);
      toast.success(
        s.paused ? "Alerts paused" : "Alerts resumed",
        s.paused ? undefined : "The agent checked in again.",
      );
    },
  });
  const startWindow = useMutation({
    mutationFn: api.startUptimyMaintenance,
    onSuccess: (s) => {
      apply(s);
      toast.success(
        "Maintenance window started",
        `Normal alerting comes back at ${formatClock(s.maintenance_until!)}.`,
      );
    },
  });
  const endWindow = useMutation({
    mutationFn: api.endUptimyMaintenance,
    onSuccess: (s) => {
      apply(s);
      toast.success("Normal alerting restored");
    },
  });

  if (settings.isPending) {
    return <div className="h-24 animate-pulse rounded-lg border bg-nav-hover" aria-label="Loading alert settings" />;
  }
  if (settings.error) {
    return <ErrorNote error={new Error(`Couldn't load alert settings from Uptimy: ${settings.error.message}`)} />;
  }
  const s = settings.data;
  if (!s?.available) return null;

  const busy = setAlertAfter.isPending || setPaused.isPending || startWindow.isPending || endWindow.isPending;
  const error = setAlertAfter.error ?? setPaused.error ?? startWindow.error ?? endWindow.error;
  const minutes = Math.round((s.alert_after_seconds ?? 0) / 60);
  const custom = !s.alert_after_choices.includes(minutes);
  const inWindow = !!s.maintenance_until;

  return (
    <div className="flex flex-col gap-4 border-t pt-4">
      {/* When Uptimy alerts */}
      <div className="flex flex-col gap-2">
        <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
          <h3 className="text-sm font-medium">Alert me when the agent is silent for</h3>
          {custom && !inWindow && (
            <span className="text-xs text-muted-foreground">{formatMinutes(minutes)}, set in Uptimy</span>
          )}
        </div>
        <div role="radiogroup" aria-label="Alert after" className="flex flex-wrap gap-1.5">
          {s.alert_after_choices.map((m) => {
            const selected = !inWindow && m === minutes;
            return (
              <button
                key={m}
                type="button"
                role="radio"
                aria-checked={selected}
                disabled={!canEdit || busy || inWindow || s.paused}
                onClick={() => !selected && setAlertAfter.mutate(m)}
                className={cn(
                  "h-8 rounded-lg border px-3 text-sm transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none disabled:cursor-not-allowed",
                  selected
                    ? "border-brand bg-brand font-semibold text-white"
                    : "hover:border-brand hover:bg-nav-hover disabled:opacity-50 disabled:hover:border-border disabled:hover:bg-transparent",
                )}
              >
                {formatMinutes(m)}
              </button>
            );
          })}
        </div>
        <p className="text-xs text-muted-foreground">
          {inWindow
            ? "You can change this once the maintenance window ends."
            : s.paused
              ? "You can change this once alerts are resumed."
              : "Longer rides out restarts and redeploys."}
        </p>
      </div>

      <div className="h-px bg-border" />

      {/* Planned work */}
      {s.paused ? (
        <Banner icon={BellOff} tone="warn" title="Alerts are paused">
          Uptimy won&apos;t alert even if this agent goes down, until alerts are resumed.
          {canEdit && (
            <div className="mt-3">
              <Button size="sm" onClick={() => setPaused.mutate(false)} disabled={busy}>
                <Play /> Resume alerts
              </Button>
            </div>
          )}
        </Banner>
      ) : inWindow ? (
        <Banner icon={Wrench} tone="warn" title={`Maintenance window until ${formatClock(s.maintenance_until!)}`}>
          The heartbeat stays active in Uptimy; its alert deadline is pushed back instead, so Uptimy only alerts if the
          agent is silent for longer than the window. After that, alerts come back to{" "}
          {formatMinutes(Math.round((s.normal_alert_after_seconds ?? 0) / 60))} of silence.
          {canEdit && (
            <div className="mt-3 flex flex-wrap items-center gap-2">
              <Button size="sm" variant="outline" onClick={() => endWindow.mutate()} disabled={busy}>
                End now
              </Button>
              <span className="text-xs text-muted-foreground">Or make it end in</span>
              {s.maintenance_choices.map((m) => (
                <Button key={m} size="sm" variant="ghost" onClick={() => startWindow.mutate(m)} disabled={busy}>
                  {formatMinutes(m)}
                </Button>
              ))}
            </div>
          )}
        </Banner>
      ) : (
        <div className="flex flex-col gap-2">
          <h3 className="flex items-center gap-2 text-sm font-medium">
            <Clock className="size-4 text-muted-foreground" /> Planned work on this agent or its server?
          </h3>
          {canEdit ? (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="mr-1 text-sm text-muted-foreground">Allow downtime for</span>
              {s.maintenance_choices.map((m) => (
                <Button key={m} size="sm" variant="outline" onClick={() => startWindow.mutate(m)} disabled={busy}>
                  {formatMinutes(m)}
                </Button>
              ))}
              <Button
                size="sm"
                variant="ghost"
                className="text-muted-foreground"
                disabled={busy}
                onClick={() =>
                  confirm({
                    title: "Pause alerts",
                    subtitle: "Nobody is alerted until you resume",
                    icon: BellOff,
                    message: (
                      <>
                        Uptimy won&apos;t alert even if this agent never comes back. For planned work, a maintenance
                        window is safer: it still alerts if the agent isn&apos;t back when the window ends.
                      </>
                    ),
                    confirmLabel: "Pause alerts",
                    action: () => setPaused.mutateAsync(true),
                  })
                }
              >
                Pause until I resume
              </Button>
            </div>
          ) : (
            <p className="text-sm text-muted-foreground">An admin can open a maintenance window here.</p>
          )}
          <p className="text-xs text-muted-foreground">
            You&apos;re still alerted if the agent isn&apos;t back when it ends.
          </p>
        </div>
      )}

      <ErrorNote error={error} />
    </div>
  );
}

function Banner({
  icon: Icon,
  title,
  tone,
  children,
}: {
  icon: typeof Wrench;
  title: string;
  tone: "warn";
  children: React.ReactNode;
}) {
  return (
    <div className={cn("flex gap-3 rounded-lg border p-3 text-sm", tone === "warn" && "border-paused/40 bg-paused/10")}>
      <Icon className="mt-0.5 size-4 shrink-0 text-paused" />
      <div className="min-w-0 flex-1">
        <div className="font-medium">{title}</div>
        <div className="mt-0.5 text-muted-foreground">{children}</div>
      </div>
    </div>
  );
}
