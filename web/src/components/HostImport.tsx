import { useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Server, TrainFront, type LucideIcon } from "lucide-react";
import { api, type HostImportResult, type HostPlan, type HostService, type ImportHost } from "@/lib/api";
import { useCheckTypeLabel } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input, PasswordInput } from "@/components/ui/input";
import { ErrorNote } from "@/components/Layout";
import { ImportResult, Item, ItemList, SkippedList } from "@/components/ImportReview";

const HOSTS: Record<
  ImportHost,
  {
    label: string;
    icon: LucideIcon;
    description: string;
    placeholder: string;
    inputLabel: string;
    help: string;
    group: string;
  }
> = {
  railway: {
    label: "Railway",
    icon: TrainFront,
    description: "Monitor every service in a Railway project, privately when the agent runs in it.",
    placeholder: "Project token",
    inputLabel: "Railway project token",
    help: "Railway → Project → Settings → Tokens",
    group: "project",
  },
  render: {
    label: "Render",
    icon: Server,
    description:
      "Monitor every Render service the key can see, privately when the agent runs in the same workspace and region.",
    placeholder: "API key",
    inputLabel: "Render API key",
    help: "Render → Account Settings → API Keys",
    group: "workspace",
  },
};

/**
 * Import from Railway or Render: paste a project token or API key, review a
 * monitor per service, choose what to import. The token is only sent to
 * plan, and never kept.
 */
export function HostImportCard({ host }: { host: ImportHost }) {
  const copy = HOSTS[host];
  const [plan, setPlan] = useState<HostPlan | null>(null);
  const [result, setResult] = useState<HostImportResult | null>(null);
  const reset = () => {
    setPlan(null);
    setResult(null);
  };

  return (
    <Card section>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <copy.icon className="size-4" /> Import from {copy.label}
        </CardTitle>
        <CardDescription>{copy.description}</CardDescription>
      </CardHeader>
      <CardContent>
        {result ? (
          <ImportResult
            result={result}
            onDone={reset}
            heartbeatsTitle="Ping the agent from your cron jobs"
            heartbeatsHint={
              <>
                Add <code>curl -fsS &lt;ping URL&gt;</code> at the end of each job. The URLs are on their pages:
              </>
            }
          />
        ) : plan ? (
          <PlanReview host={host} plan={plan} onCancel={reset} onImported={setResult} />
        ) : (
          <EnterToken host={host} onPlan={setPlan} />
        )}
      </CardContent>
    </Card>
  );
}

function EnterToken({ host, onPlan }: { host: ImportHost; onPlan: (p: HostPlan) => void }) {
  const copy = HOSTS[host];
  const [token, setToken] = useState("");
  const read = useMutation({ mutationFn: (t: string) => api.planHostImport(host, t), onSuccess: onPlan });
  return (
    <form
      className="flex flex-col gap-3 text-sm"
      onSubmit={(e) => {
        e.preventDefault();
        read.mutate(token.trim());
      }}
    >
      <div className="flex flex-col gap-2 sm:flex-row">
        <PasswordInput
          autoComplete="off"
          placeholder={copy.placeholder}
          aria-label={copy.inputLabel}
          value={token}
          onChange={(e) => setToken(e.target.value)}
          className="sm:max-w-sm"
        />
        <Button type="submit" variant="outline" disabled={!token.trim() || read.isPending}>
          {read.isPending ? "Reading services…" : "Preview services"}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">{copy.help}</p>
      <ErrorNote error={read.error} />
    </form>
  );
}

/** What the review changes on an HTTP check. */
type Edit = { address?: "private" | "public"; port: string; path: string };

function validPort(port: string) {
  const n = Number(port);
  return Number.isInteger(n) && n >= 1 && n <= 65535;
}

/** The HTTP check's URL for the chosen address, or "" while a private one has no port. */
function target(s: HostService, e: Edit) {
  const path = e.path.startsWith("/") ? e.path : `/${e.path}`;
  if (e.address === "public") return `https://${s.public_host}${path}`;
  return validPort(e.port) ? `http://${s.private_host}:${e.port}${path}` : "";
}

function PlanReview({
  host,
  plan,
  onCancel,
  onImported,
}: {
  host: ImportHost;
  plan: HostPlan;
  onCancel: () => void;
  onImported: (r: HostImportResult) => void;
}) {
  const copy = HOSTS[host];
  const qc = useQueryClient();
  const typeLabel = useCheckTypeLabel();
  const [selected, setSelected] = useState(() => new Set(plan.services.map((s) => s.service_id)));
  const [edits, setEdits] = useState<Record<string, Edit>>(() =>
    Object.fromEntries(
      plan.services.map((s) => [
        s.service_id,
        { address: s.address, port: s.port ? String(s.port) : "", path: s.path ?? "/" },
      ]),
    ),
  );
  const edit = (id: string, patch: Partial<Edit>) => setEdits((all) => ({ ...all, [id]: { ...all[id], ...patch } }));
  const toggle = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  };

  const chosen = plan.services
    .filter((s) => selected.has(s.service_id))
    .map((s): HostService => {
      const e = edits[s.service_id];
      if (!e.address || !s.monitor.check) return s;
      return { ...s, monitor: { ...s.monitor, check: { ...s.monitor.check, target: target(s, e) } } };
    });
  const missingPort = chosen.some((s) => s.monitor.check && !s.monitor.check.target);
  const apply = useMutation({
    mutationFn: () => api.applyHostImport(host, chosen),
    onSuccess: (r) => {
      qc.invalidateQueries();
      onImported(r);
    },
  });

  return (
    <div className="flex flex-col gap-5">
      <p className="text-sm text-muted-foreground">
        {plan.project_name}
        {plan.environment_name && ` · ${plan.environment_name}`}
        {!plan.same_project &&
          ` · the agent isn't in this ${copy.group}, so services are checked at their public addresses`}
      </p>
      {plan.services.length > 0 && (
        <ItemList title={`Services (${plan.services.length})`}>
          {plan.services.map((s) => {
            const e = edits[s.service_id];
            const m = s.monitor;
            const shown = e.address ? target(s, e) : m.check ? m.check.target : `${m.heartbeat?.cron} (UTC)`;
            return (
              <Item
                key={s.service_id}
                checked={selected.has(s.service_id)}
                onToggle={() => toggle(s.service_id)}
                name={m.name}
                badge={m.kind === "heartbeat" ? "Heartbeat" : typeLabel(m.check?.type ?? "")}
                notes={s.notes}
                extra={shown || undefined}
              >
                {e.address && (
                  <div className="flex flex-wrap items-center gap-2">
                    <AddressToggle
                      value={e.address}
                      onChange={(address) => edit(s.service_id, { address })}
                      privateDisabled={
                        (!plan.same_project && `The agent isn't in this ${copy.group}`) ||
                        (!s.private_host && "Not on the agent's private network")
                      }
                      publicDisabled={!s.public_host && "No public domain"}
                    />
                    {e.address === "private" && (
                      <Input
                        inputMode="numeric"
                        placeholder="Port"
                        aria-label={`${m.name} port`}
                        value={e.port}
                        onChange={(ev) => edit(s.service_id, { port: ev.target.value.trim() })}
                        aria-invalid={e.port !== "" && !validPort(e.port)}
                        className={cn("h-8 w-24", e.port !== "" && !validPort(e.port) && "border-down")}
                      />
                    )}
                    <Input
                      placeholder="/"
                      aria-label={`${m.name} path`}
                      value={e.path}
                      onChange={(ev) => edit(s.service_id, { path: ev.target.value.trim() })}
                      className="h-8 w-40"
                    />
                  </div>
                )}
              </Item>
            );
          })}
        </ItemList>
      )}
      {plan.skipped.length > 0 && <SkippedList items={plan.skipped} title="Won't be imported" />}
      {plan.services.length === 0 && (
        <p className="text-sm text-muted-foreground">Nothing in this {copy.group} to import.</p>
      )}

      <ErrorNote error={apply.error} />
      <div className="flex items-center justify-end gap-2">
        {missingPort && <span className="text-sm text-muted-foreground">Enter the missing ports</span>}
        <Button variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button onClick={() => apply.mutate()} disabled={chosen.length === 0 || missingPort || apply.isPending}>
          {apply.isPending ? "Importing…" : `Import ${chosen.length} monitor${chosen.length === 1 ? "" : "s"}`}
        </Button>
      </div>
    </div>
  );
}

function AddressToggle({
  value,
  onChange,
  privateDisabled,
  publicDisabled,
}: {
  value: "private" | "public";
  onChange: (v: "private" | "public") => void;
  privateDisabled: string | false;
  publicDisabled: string | false;
}) {
  const options = [
    ["private", "Private", privateDisabled],
    ["public", "Public", publicDisabled],
  ] as const;
  return (
    <div className="flex w-fit gap-1 rounded-md bg-muted p-0.5" role="radiogroup" aria-label="Address">
      {options.map(([v, label, disabled]) => (
        <button
          key={v}
          type="button"
          role="radio"
          aria-checked={value === v}
          disabled={!!disabled}
          data-tip={disabled || undefined}
          onClick={() => onChange(v)}
          className={cn(
            "rounded px-2.5 py-1 text-xs font-medium disabled:cursor-not-allowed disabled:opacity-50",
            value === v
              ? "bg-primary text-primary-foreground shadow-xs"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {label}
        </button>
      ))}
    </div>
  );
}
