import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Send, Unplug, X } from "lucide-react";
import { api, type UptimyStatus } from "@/lib/api";
import { cn, formatInterval, timeAgo } from "@/lib/utils";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { UptimyMark } from "@/components/brand";
import { ErrorNote } from "@/components/Layout";
import { useCanEdit } from "@/components/AuthGate";
import { UptimyAlertingSettings, useUptimyAlerting } from "@/components/UptimyAlerting";

function useUptimyStatus() {
  return useQuery({ queryKey: ["uptimy"], queryFn: api.uptimyStatus, refetchInterval: 30_000 });
}

/** Full connect / status card, shown on the Settings page. */
export function WatchTheWatcherCard() {
  const status = useUptimyStatus();
  const canEdit = useCanEdit();
  // Shown after disconnecting if Uptimy couldn't be cleaned up; lives here
  // because the connected view it comes from is gone by then.
  const [warning, setWarning] = useState<string>();
  return (
    <Card id="watch-the-watcher">
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <UptimyMark className="h-3" /> Uptimy Cloud
        </CardTitle>
        <CardDescription>
          Connect this agent to your Uptimy workspace. Uptimy alerts you from outside when the agent, its server or the
          cluster goes down, and its monitors show up there with their uptime, reports, status pages and incidents. The
          agent keeps working on its own either way.
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ErrorNote error={status.error} />
        {warning && (
          <p role="alert" className="mb-4 rounded-md border border-paused/40 bg-paused/10 px-3 py-2 text-sm">
            {warning}
          </p>
        )}
        {status.data &&
          (status.data.connected ? (
            <Connected status={status.data} canEdit={canEdit} onWarning={setWarning} />
          ) : canEdit ? (
            <ConnectButton />
          ) : (
            <p className="text-sm text-muted-foreground">Not connected. An admin can set it up here.</p>
          ))}
      </CardContent>
    </Card>
  );
}

function ConnectButton() {
  const start = useMutation({
    mutationFn: () => api.startUptimyConnect(window.location.origin),
    // Leave the agent for Uptimy's consent page; it sends the browser back to
    // /uptimy/connected when done.
    onSuccess: ({ authorize_url }) => window.location.assign(authorize_url),
  });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-col gap-3 rounded-lg border p-4 sm:flex-row sm:items-center">
        <div className="min-w-0 flex-1 text-sm">
          <div className="font-medium">Connect with your Uptimy account</div>
          <p className="mt-0.5 text-muted-foreground">
            Sign in and pick a workspace: the agent adds itself there with its monitors, read-only. Free, and takes a
            few seconds.
          </p>
        </div>
        <Button onClick={() => start.mutate()} disabled={start.isPending} className="shrink-0">
          <UptimyMark className="h-3 brightness-0 invert dark:brightness-100 dark:invert-0" />
          {start.isPending ? "Opening Uptimy…" : "Connect to Uptimy Cloud"}
        </Button>
      </div>
      <ErrorNote error={start.error} />
    </div>
  );
}

function Connected({
  status,
  canEdit,
  onWarning,
}: {
  status: UptimyStatus;
  canEdit: boolean;
  onWarning: (warning?: string) => void;
}) {
  const qc = useQueryClient();
  const update = (s: UptimyStatus) => {
    qc.setQueryData(["uptimy"], s);
    qc.invalidateQueries({ queryKey: ["info"] });
  };
  const upload = useMutation({
    mutationFn: api.uploadToUptimy,
    onSuccess: (s) => {
      update(s);
      toast.success("Uploading now");
    },
  });
  const disconnect = useMutation({
    mutationFn: api.disconnectUptimy,
    onSuccess: (s) => {
      onWarning(s.warning);
      update(s);
      if (!s.warning) toast.success("Disconnected from Uptimy");
    },
  });
  const account = status.account!;
  const sync = status.sync;
  const settings = useUptimyAlerting(true);
  const inWindow = !!settings.data?.maintenance_until;
  const waiting = !sync?.last_upload_at && !sync?.last_error;
  const healthy = !waiting && !sync?.last_error;

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start gap-3 rounded-lg border p-4">
        <span
          className={cn(
            "mt-1.5 size-2.5 shrink-0 rounded-full",
            sync?.over_plan || status.paused || (healthy && inWindow)
              ? "bg-paused"
              : waiting
                ? "bg-pending"
                : healthy
                  ? "bg-up"
                  : "bg-down",
          )}
        />
        <div className="min-w-0 flex-1 text-sm">
          <div className="font-medium">
            {sync?.over_plan
              ? "Not syncing: over your plan's connected agents"
              : status.paused
                ? "Alerts are paused"
                : waiting
                  ? "Setting up the sync…"
                  : !healthy
                    ? "Uploads are failing"
                    : `Connected to ${account.workspace_name}`}
          </div>
          <div className="mt-0.5 text-muted-foreground">
            {status.paused && "Uptimy doesn't alert when the agent goes silent while alerts are paused. "}
            {healthy &&
              sync?.last_upload_at &&
              `Uploading every ${formatInterval(sync.upload_interval_seconds ?? 300)} · last upload ${timeAgo(sync.last_upload_at)}`}
            {!healthy && !waiting && sync?.last_error}
          </div>
          {sync?.active && (
            <div className="mt-1 text-muted-foreground">
              {sync.healthchecks} healthcheck{sync.healthchecks === 1 ? "" : "s"} and {sync.heartbeats} heartbeat
              {sync.heartbeats === 1 ? "" : "s"} in Uptimy, read-only there. Change them here.
            </div>
          )}
          {!!sync?.not_synced?.length && (
            <div className="mt-1 text-down">
              {sync.not_synced.length} monitor{sync.not_synced.length === 1 ? " isn't" : "s aren't"} synced to Uptimy:{" "}
              {sync.not_synced.map((m) => `${m.name} (${m.reason})`).join(", ")}
            </div>
          )}
          {!!sync?.rejected_too_old && (
            <div className="mt-1 text-down">
              Uptimy rejected {sync.rejected_too_old} result{sync.rejected_too_old === 1 ? "" : "s"} that{" "}
              {sync.rejected_too_old === 1 ? "was" : "were"} more than a day old — check the agent&apos;s clock
            </div>
          )}
          {!!sync?.rejected_clock_ahead && (
            <div className="mt-1 text-down">
              Uptimy rejected {sync.rejected_clock_ahead} result{sync.rejected_clock_ahead === 1 ? "" : "s"} timestamped
              in the future — check the agent&apos;s clock
            </div>
          )}
          <div className="mt-2 text-xs text-muted-foreground">
            Connected by {account.connected_by} {timeAgo(account.connected_at)}. The agent&apos;s key and its check-in
            are managed for you.
          </div>
        </div>
      </div>
      <UptimyAlertingSettings canEdit={canEdit} />
      {canEdit && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => upload.mutate()} disabled={upload.isPending}>
            <Send /> {upload.isPending ? "Uploading…" : "Upload now"}
          </Button>
          <Button
            variant="ghost"
            size="sm"
            onClick={() =>
              confirm({
                title: "Disconnect from Uptimy",
                subtitle: "Nobody is alerted if this agent goes down",
                icon: Unplug,
                message: (
                  <>
                    This removes the agent, its monitors and its check-in from{" "}
                    <strong className="text-foreground">{account.workspace_name}</strong> and revokes its key. Reports
                    already made keep their numbers, and the agent keeps working here. You can connect again at any
                    time.
                  </>
                ),
                confirmLabel: "Disconnect",
                action: () => disconnect.mutateAsync(),
              })
            }
            disabled={disconnect.isPending}
          >
            Disconnect
          </Button>
        </div>
      )}
      <ErrorNote error={upload.error} />
    </div>
  );
}

const DISMISS_KEY = "uptimy-agent:hide-watchdog-nudge";

function readDismissed() {
  try {
    return localStorage.getItem(DISMISS_KEY) === "1";
  } catch {
    return false;
  }
}

/** Small dashboard nudge shown until the agent is connected or it's dismissed. */
export function WatchTheWatcherNudge() {
  const status = useUptimyStatus();
  const [dismissed, setDismissed] = useState(readDismissed);
  if (!status.data || status.data.connected || dismissed) return null;

  return (
    <Card className="relative p-5">
      <button
        className="absolute top-3 right-3 rounded p-1 text-muted-foreground hover:bg-muted hover:text-foreground"
        aria-label="Dismiss"
        onClick={() => {
          try {
            localStorage.setItem(DISMISS_KEY, "1");
          } catch {
            // private mode: dismiss for this session only
          }
          setDismissed(true);
        }}
      >
        <X className="size-4" />
      </button>
      <div className="mb-2 flex items-center gap-2 text-sm font-semibold">
        <UptimyMark className="h-3" /> Who watches this agent?
      </div>
      <p className="mb-4 text-sm text-muted-foreground">
        Get alerted from outside if this agent or its server goes down. Takes a minute, free.
      </p>
      <Link to="/settings#watch-the-watcher" className={buttonVariants({ size: "sm" })}>
        Set it up
      </Link>
    </Card>
  );
}
