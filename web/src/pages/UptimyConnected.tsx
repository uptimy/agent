import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { CheckCircle2, XCircle } from "lucide-react";
import { api, type UptimyCheckIn } from "@/lib/api";
import { buttonVariants } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { UptimyMark } from "@/components/brand";

/** Must match AGENT_CALLBACK_PATH in upti.my-app's consent page. */
const CONNECT_CALLBACK_PATH = "/uptimy/connected";

type Handoff = { state: string; code: string; error: string };

/**
 * Uptimy's consent page sends the browser here with a one-time code and the
 * state (OAuth). The agent's server exchanges the code for the agent key with
 * a PKCE verifier only it holds, so the key never reaches the browser. Read
 * the parameters once at page load and remove them from the address bar and
 * history straight away. Module-level so it survives a sign-in screen shown
 * first, and React StrictMode's double-rendering in development.
 */
const handoff: Handoff | null = (() => {
  if (window.location.pathname !== CONNECT_CALLBACK_PATH || !window.location.search) return null;
  const p = new URLSearchParams(window.location.search);
  window.history.replaceState(window.history.state, "", window.location.pathname);
  return { state: p.get("state") ?? "", code: p.get("code") ?? "", error: p.get("error") ?? "" };
})();

// The handoff can only be used once (the agent consumes the state), so share
// one request between StrictMode's duplicate effects.
let finishing: Promise<UptimyCheckIn> | null = null;

type Outcome =
  | { kind: "working" }
  | { kind: "connected"; workspace: string }
  | { kind: "cancelled" }
  | { kind: "failed"; message: string };

export function UptimyConnected() {
  const qc = useQueryClient();
  const [outcome, setOutcome] = useState<Outcome>(() => {
    if (!handoff) return { kind: "failed", message: "Nothing to finish here. Start again from Settings." };
    if (handoff.error === "access_denied") return { kind: "cancelled" };
    if (handoff.error || !handoff.code) return { kind: "failed", message: "Uptimy didn't complete the connection." };
    return { kind: "working" };
  });

  useEffect(() => {
    if (outcome.kind !== "working" || !handoff) return;
    finishing ??= api.finishUptimyConnect(handoff.state, handoff.code);
    finishing
      .then((status) => {
        qc.setQueryData(["uptimy"], status);
        qc.invalidateQueries({ queryKey: ["info"] });
        setOutcome({ kind: "connected", workspace: status.account?.workspace_name ?? "Uptimy" });
      })
      .catch((err: unknown) =>
        setOutcome({ kind: "failed", message: err instanceof Error ? err.message : String(err) }),
      );
  }, [outcome.kind, qc]);

  return (
    <div className="mx-auto max-w-md py-10">
      <Card className="flex flex-col items-center gap-4 px-6 py-10 text-center">
        {outcome.kind === "working" && (
          <>
            <UptimyMark className="h-5 animate-pulse" />
            <p className="text-sm text-muted-foreground">Setting up the heartbeat in Uptimy…</p>
          </>
        )}
        {outcome.kind === "connected" && (
          <>
            <CheckCircle2 className="size-10 text-up" />
            <div>
              <h1 className="text-lg font-semibold">Connected to {outcome.workspace}</h1>
              <p className="mt-1 text-sm text-muted-foreground">
                This agent now checks in with Uptimy every minute. If it stops, Uptimy alerts you.
              </p>
            </div>
            <Link to="/settings" className={buttonVariants()}>
              Back to Settings
            </Link>
          </>
        )}
        {outcome.kind === "cancelled" && (
          <>
            <XCircle className="size-10 text-muted-foreground" />
            <div>
              <h1 className="text-lg font-semibold">Connection cancelled</h1>
              <p className="mt-1 text-sm text-muted-foreground">Nothing was created in Uptimy.</p>
            </div>
            <Link to="/settings" className={buttonVariants({ variant: "outline" })}>
              Back to Settings
            </Link>
          </>
        )}
        {outcome.kind === "failed" && (
          <>
            <XCircle className="size-10 text-down" />
            <div>
              <h1 className="text-lg font-semibold">Couldn't connect to Uptimy</h1>
              <p className="mt-1 text-sm text-muted-foreground">{outcome.message}</p>
            </div>
            <Link to="/settings" className={buttonVariants({ variant: "outline" })}>
              Back to Settings
            </Link>
          </>
        )}
      </Card>
    </div>
  );
}
