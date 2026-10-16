import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { HeartPulse } from "lucide-react";
import { api, type Check, type CheckTypeInfo, type HealthcheckInput } from "@/lib/api";
import { cn } from "@/lib/utils";
import { monitorPath } from "@/lib/monitors";
import { useCheckTypes } from "@/lib/types";
import { Field, Input } from "@/components/ui/input";
import { ErrorNote } from "@/components/Layout";
import { SchemaFields } from "@/components/SchemaFields";
import { FormShell } from "@/components/MonitorPage";
import { ChannelPicker } from "@/components/MonitorPicker";
import { useCanEdit } from "@/components/AuthGate";

// The form is built from the check types the agent registers
// (internal/monitor/check_*.go): their target, fields and defaults come from
// /api/check-types, so a new check type needs no changes here.

const empty: HealthcheckInput = {
  name: "",
  paused: false,
  check: {
    type: "http",
    target: "",
    interval_seconds: 60,
    timeout_seconds: 10,
    failure_threshold: 2,
    recovery_threshold: 1,
    config: {},
  },
  notifier_ids: [],
};

export function HealthcheckForm() {
  const params = useParams();
  const editing = params.id ? Number(params.id) : null;
  const canEdit = useCanEdit();
  const existing = useQuery({
    queryKey: ["healthcheck", editing],
    queryFn: () => api.healthcheck(editing!),
    enabled: editing != null,
  });

  if (!canEdit) return <Navigate to="/healthchecks" replace />;
  if (editing == null) return <HealthcheckFormBody editing={null} initial={empty} />;
  if (existing.error) return <ErrorNote error={existing.error} />;
  if (!existing.data) return null;
  const h = existing.data.healthcheck;
  if (h.source !== "ui") return <Navigate to={monitorPath(h)} replace />;
  return (
    <HealthcheckFormBody
      key={h.id}
      editing={h.id}
      initial={{
        name: h.name,
        paused: h.paused,
        check: h.check,
        notifier_ids: existing.data.notifier_ids,
      }}
    />
  );
}

function HealthcheckFormBody({ editing, initial }: { editing: number | null; initial: HealthcheckInput }) {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const types = useCheckTypes();
  const [form, setForm] = useState(initial);

  const save = useMutation({
    mutationFn: () => (editing ? api.updateHealthcheck(editing, form) : api.createHealthcheck(form)),
    onSuccess: (h) => {
      qc.invalidateQueries({ queryKey: ["healthchecks"], exact: true });
      qc.invalidateQueries({ queryKey: ["healthcheck", h.id] });
      navigate(monitorPath(h));
    },
  });

  const setCheck = <K extends keyof Check>(key: K, value: Check[K]) =>
    setForm((f) => ({ ...f, check: { ...f.check, [key]: value } }));
  const check = form.check;
  const type = types.data?.find((t) => t.type === check.type);

  return (
    <FormShell
      kind="healthcheck"
      editing={editing}
      icon={HeartPulse}
      error={save.error}
      submitting={save.isPending}
      canSubmit={!!type}
      onSubmit={() => save.mutate()}
    >
      <ErrorNote error={types.error} />
      {types.data && <TypePicker types={types.data} value={check.type} onChange={(t) => setCheck("type", t)} />}

      <Field label="Name" htmlFor="name">
        <Input
          id="name"
          required
          maxLength={100}
          value={form.name}
          onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
          placeholder="Checkout API"
        />
      </Field>

      {type && (
        <>
          <Field label={type.target.label} htmlFor="target" hint={type.target.hint}>
            <Input
              id="target"
              required={!type.target.optional}
              value={check.target}
              onChange={(e) => setCheck("target", e.target.value)}
              placeholder={type.target.placeholder}
              autoComplete="off"
              spellCheck={false}
            />
          </Field>
          <SchemaFields
            idPrefix="config"
            fields={type.fields ?? []}
            values={check.config}
            onChange={(key, value) => setCheck("config", { ...check.config, [key]: value })}
          />
        </>
      )}

      <div className="grid gap-4 sm:grid-cols-2">
        <Field label="Check every (s)" htmlFor="interval">
          <Input
            id="interval"
            type="number"
            min={10}
            value={check.interval_seconds}
            onChange={(e) => setCheck("interval_seconds", Number(e.target.value))}
          />
        </Field>
        <Field label="Timeout (s)" htmlFor="timeout">
          <Input
            id="timeout"
            type="number"
            min={1}
            value={check.timeout_seconds}
            onChange={(e) => setCheck("timeout_seconds", Number(e.target.value))}
          />
        </Field>
        <Field label="Alert after failures" htmlFor="threshold" hint="Consecutive failed checks">
          <Input
            id="threshold"
            type="number"
            min={1}
            max={10}
            value={check.failure_threshold}
            onChange={(e) => setCheck("failure_threshold", Number(e.target.value))}
          />
        </Field>
        <Field label="Recover after successes" htmlFor="recovery" hint="Consecutive good checks once down">
          <Input
            id="recovery"
            type="number"
            min={1}
            max={10}
            value={check.recovery_threshold}
            onChange={(e) => setCheck("recovery_threshold", Number(e.target.value))}
          />
        </Field>
      </div>

      <ChannelPicker ids={form.notifier_ids ?? []} onChange={(ids) => setForm((f) => ({ ...f, notifier_ids: ids }))} />
    </FormShell>
  );
}

function TypePicker({
  types,
  value,
  onChange,
}: {
  types: CheckTypeInfo[];
  value: string;
  onChange: (type: string) => void;
}) {
  const info = useQuery({ queryKey: ["info"], queryFn: api.info });
  const selected = types.find((t) => t.type === value);
  return (
    <fieldset className="flex flex-col gap-2">
      <legend className="mb-2 text-sm font-medium">Type</legend>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {types.map((t) => (
          <button
            key={t.type}
            type="button"
            aria-pressed={value === t.type}
            onClick={() => onChange(t.type)}
            className={cn(
              "rounded-lg border p-3 text-left transition-colors",
              value === t.type ? "border-primary bg-primary/5 ring-1 ring-primary" : "hover:bg-muted",
            )}
          >
            <div className="text-sm font-medium">{t.label}</div>
            <div className="mt-0.5 text-xs text-muted-foreground">{t.summary}</div>
          </button>
        ))}
      </div>
      {selected?.requires === "kubernetes" && info.data && !info.data.kubernetes && (
        <p className="text-xs text-paused">
          This agent isn&apos;t running inside Kubernetes, so {selected.label} checks will fail. Deploy it with the Helm
          chart to use them.
        </p>
      )}
    </fieldset>
  );
}
