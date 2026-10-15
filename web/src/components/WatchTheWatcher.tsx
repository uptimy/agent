import { useState } from "react";
import { Link } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ExternalLink, Send, Unplug, X } from "lucide-react";
import { api, type UptimyCheckIn } from "@/lib/api";
import { cn, formatInterval, timeAgo } from "@/lib/utils";
import { confirm } from "@/components/ui/confirm";
import { toast } from "@/components/ui/toast";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { UptimyMark } from "@/components/brand";
import { ErrorNote } from "@/components/Layout";
import { useCanEdit } from "@/components/AuthGate";
import { UptimyAlertingSettings, useUptimyAlerting } from "@/components/UptimyAlerting";

const CREATE_URL =
  "https://app.upti.my/workspaces/heartbeats/?utm_source=uptimy-agent&utm_medium=app&utm_campaign=watch_the_watcher";

function useUptimyCheckIn() {
  return useQuery({ queryKey: ["uptimy"], queryFn: api.uptimyCheckIn, refetchInterval: 30_000 });
}

/** Full connect / status card, shown on the Settings page. */
export function WatchTheWatcherCard() {
  const hb = useUptimyCheckIn();
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
        <ErrorNote error={hb.error} />
        {warning && (
          <p role="alert" className="mb-4 rounded-md border border-paused/40 bg-paused/10 px-3 py-2 text-sm">
            {warning}
          </p>
        )}
        {hb.data &&
          (hb.data.enabled || hb.data.account ? (
            <Connected status={hb.data} canEdit={canEdit} onWarning={setWarning} />
          ) : canEdit ? (
            <ConnectForm status={hb.data} />
          ) : (
            <p className="text-sm text-muted-foreground">Not connected. An admin can set it up here.</p>
          ))}
      </CardContent>
    </Card>
  );
}

function ConnectForm({ status }: { status: UptimyCheckIn }) {
  const [manual, setManual] = useState(false);
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

      <div>
        <button
          type="button"
          className="text-sm text-muted-foreground underline-offset-4 hover:text-foreground hover:underline"
          aria-expanded={manual}
          onClick={() => setManual((m) => !m)}
        >
          {manual ? "Hide" : "Or paste a heartbeat URL instead"}
        </button>
        {manual && (
          <div className="mt-4">
            <PasteHeartbeat status={status} />
          </div>
        )}
      </div>
    </div>
  );
}

/** The manual path: create a heartbeat in Uptimy yourself and paste its URL. */
function PasteHeartbeat({ status }: { status: UptimyCheckIn }) {
  const qc = useQueryClient();
  const [value, setValue] = useState("");
  const connect = useMutation({
    mutationFn: () => api.connectUptimyURL(value),
    onSuccess: (s) => {
      qc.setQueryData(["uptimy"], s);
      qc.invalidateQueries({ queryKey: ["info"] });
    },
  });

  return (
    <ol className="flex flex-col gap-5">
      <Step n={1} title="Create a heartbeat in Uptimy">
        <p className="text-sm text-muted-foreground">
          Set it to expect a check-in every <b>{formatInterval(status.interval_seconds)}</b>. Heartbeats are included in
          Uptimy's free plan.
        </p>
        <a
          href={CREATE_URL}
          target="_blank"
          rel="noreferrer"
          className={buttonVariants({ variant: "outline", className: "mt-3 w-fit" })}
        >
          Open Uptimy <ExternalLink />
        </a>
      </Step>
      <Step n={2} title="Paste its URL here">
        <form
          className="mt-1 flex flex-col gap-2 sm:flex-row"
          onSubmit={(e) => {
            e.preventDefault();
            connect.mutate();
          }}
        >
          <Input
            aria-label="Heartbeat URL or token"
            placeholder="https://heartbeats.upti.my/v1/monitors/…"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            required
          />
          <Button type="submit" variant="outline" disabled={connect.isPending} className="shrink-0">
            {connect.isPending ? "Connecting…" : "Connect"}
          </Button>
        </form>
        <p className="mt-2 text-xs text-muted-foreground">
          The token on its own works too. We send a check-in straight away to confirm.
        </p>
        <div className="mt-2">
          <ErrorNote error={connect.error} />
        </div>
      </Step>
    </ol>
  );
}

function Step({ n, title, children }: { n: number; title: string; children: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="grid size-6 shrink-0 place-items-center rounded-full bg-muted text-xs font-semibold">{n}</span>
      <div className="min-w-0 flex-1">
        <div className="mb-1 text-sm font-medium">{title}</div>
        {children}
      </div>
    </li>
  );
}

function Connected({
  status,
  canEdit,
  onWarning,
}: {
  status: UptimyCheckIn;
  canEdit: boolean;
  onWarning: (warning?: string) => void;
}) {
  const qc = useQueryClient();
  const update = (s: UptimyCheckIn) => {
    qc.setQueryData(["uptimy"], s);
    qc.invalidateQueries({ queryKey: ["info"] });
  };
  const test = useMutation({
    mutationFn: api.testUptimyCheckIn,
    onSuccess: (s) => {
      update(s);
      if (s.sync) toast.success("Uploading now");
      else if (s.last_ok) toast.success("Check-in sent", "Uptimy received it.");
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
  const account = status.account;
  const sync = account ? status.sync : undefined;
  const managed = !!account && !status.managed_by_env;
  const settings = useUptimyAlerting(managed);
  const inWindow = !!settings.data?.maintenance_until;
  // With the Cloud sync, an upload is the check-in.
  const lastCheckIn = sync ? sync.last_upload_at : status.last_ping_at;
  const lastError = sync ? sync.last_error : status.last_error;
  const waiting = !lastCheckIn && !status.paused && !lastError;
  const healthy = !waiting && (sync ? !sync.last_error : status.last_ok);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-start gap-3 rounded-lg border p-4">
        <span
          className={cn(
            "mt-1.5 size-2.5 shrink-0 rounded-full",
            status.paused || (healthy && inWindow)
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
                  ? sync
                    ? "Setting up the sync…"
                    : "Waiting for the first check-in"
                  : !healthy
                    ? sync
                      ? "Uploads are failing"
                      : "Check-ins are failing"
                    : account
                      ? `Connected to ${account.workspace_name}`
                      : "Connected to Uptimy"}
          </div>
          <div className="mt-0.5 text-muted-foreground">
            {status.paused && !sync && "The agent doesn't check in while alerts are paused."}
            {status.paused && sync && "Uptimy doesn't alert when the agent goes silent while alerts are paused."}
            {!status.paused &&
              healthy &&
              !sync &&
              `Checking in every ${formatInterval(status.interval_seconds)} · last check-in ${timeAgo(lastCheckIn!)}`}
            {healthy &&
              sync &&
              `Uploading every ${formatInterval(sync.upload_interval_seconds ?? 300)} · last upload ${timeAgo(lastCheckIn!)}`}
            {!healthy && !waiting && lastError}
          </div>
          {sync?.active && (
            <div className="mt-1 text-muted-foreground">
              {sync.healthchecks} healthcheck{sync.healthchecks === 1 ? "" : "s"} and {sync.heartbeats} heartbeat
              {sync.heartbeats === 1 ? "" : "s"} in Uptimy, read-only there. Change them here.
            </div>
          )}
          {account ? (
            <div className="mt-2 text-xs text-muted-foreground">
              Connected by {account.connected_by} {timeAgo(account.connected_at)}. The agent&apos;s key and its check-in
              are managed for you.
            </div>
          ) : (
            status.url && (
              <code className="mt-2 block truncate text-xs text-muted-foreground" title={status.url}>
                {status.url}
              </code>
            )
          )}
        </div>
      </div>
      {managed && <UptimyAlertingSettings canEdit={canEdit} />}
      {!managed && !status.managed_by_env && (
        <p className="text-xs text-muted-foreground">
          To change when Uptimy alerts, edit this heartbeat in Uptimy, or connect with &quot;Connect to Uptimy
          Cloud&quot; to manage it here.
        </p>
      )}
      {status.managed_by_env ? (
        <p className="text-xs text-muted-foreground">
          Set by the <code className="rounded bg-muted px-1">UPTIMY_HEARTBEAT_URL</code> environment variable.
        </p>
      ) : null}
      {canEdit && (
        <div className="flex flex-wrap gap-2">
          <Button variant="outline" size="sm" onClick={() => test.mutate()} disabled={test.isPending || status.paused}>
            <Send /> {test.isPending ? "Sending…" : sync ? "Upload now" : "Send test check-in"}
          </Button>
          {!status.managed_by_env && (
            <Button
              variant="ghost"
              size="sm"
              onClick={() =>
                confirm({
                  title: "Disconnect from Uptimy",
                  subtitle: "Nobody is alerted if this agent goes down",
                  icon: Unplug,
                  message: account ? (
                    <>
                      This removes the agent, its monitors and its check-in from{" "}
                      <strong className="text-foreground">{account.workspace_name}</strong> and revokes its key. Reports
                      already made keep their numbers, and the agent keeps working here. You can connect again at any
                      time.
                    </>
                  ) : (
                    "The agent stops sending check-ins to Uptimy. You can connect again at any time."
                  ),
                  confirmLabel: "Disconnect",
                  action: () => disconnect.mutateAsync(),
                })
              }
              disabled={disconnect.isPending}
            >
              Disconnect
            </Button>
          )}
        </div>
      )}
      <ErrorNote error={test.error} />
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

/** Small dashboard nudge shown until the heartbeat is connected or dismissed. */
export function WatchTheWatcherNudge() {
  const hb = useUptimyCheckIn();
  const [dismissed, setDismissed] = useState(readDismissed);
  if (!hb.data || hb.data.enabled || hb.data.account || dismissed) return null;

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
