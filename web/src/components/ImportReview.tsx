import { Link } from "react-router";
import type { ImportSkipped, MonitorKind } from "@/lib/api";
import { monitorPath } from "@/lib/monitors";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";

// The review and result steps the importers share (Uptime Kuma, Railway).

type ImportedMonitor = { id: number; name: string; kind: MonitorKind };

export function ItemList({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div>
      <h3 className="mb-2 text-sm font-medium">{title}</h3>
      <ul className="max-h-80 divide-y overflow-y-auto rounded-lg border">{children}</ul>
    </div>
  );
}

export function Item({
  checked,
  onToggle,
  name,
  badge,
  notes,
  extra,
  children,
}: {
  checked: boolean;
  onToggle: () => void;
  name: string;
  badge: string;
  notes: string[] | null;
  extra?: string;
  /** Settings for the item, below it (outside its label, so clicks don't toggle it). */
  children?: React.ReactNode;
}) {
  return (
    <li>
      <label className="flex cursor-pointer items-start gap-3 px-3 py-2.5 text-sm hover:bg-nav-hover">
        <input type="checkbox" checked={checked} onChange={onToggle} className="mt-1 accent-primary" />
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="font-medium">{name}</span>
            <Badge>{badge}</Badge>
            {extra && <span className="text-xs text-muted-foreground">{extra}</span>}
          </div>
          {notes?.map((n) => (
            <p key={n} className="text-xs text-muted-foreground">
              {n.charAt(0).toUpperCase() + n.slice(1)}
            </p>
          ))}
        </div>
      </label>
      {children && <div className="px-3 pb-2.5 pl-9">{children}</div>}
    </li>
  );
}

export function SkippedList({ items, title }: { items: ImportSkipped[]; title: string }) {
  return (
    <details className="rounded-lg border px-3 py-2 text-sm">
      <summary className="cursor-pointer font-medium">
        {title} ({items.length})
      </summary>
      <ul className="mt-2 flex flex-col gap-1.5 pb-1">
        {items.map((s, i) => (
          <li key={`${s.what}-${s.name}-${i}`}>
            <span className="font-medium">{s.name}</span>
            <span className="text-muted-foreground">
              {" "}
              · {s.what}: {s.reason}
            </span>
          </li>
        ))}
      </ul>
    </details>
  );
}

export function ImportResult({
  result,
  onDone,
  heartbeatsTitle,
  heartbeatsHint,
}: {
  result: { monitors: ImportedMonitor[]; notifiers?: { id: number }[]; skipped: ImportSkipped[] };
  onDone: () => void;
  heartbeatsTitle: string;
  heartbeatsHint: React.ReactNode;
}) {
  const heartbeats = result.monitors.filter((m) => m.kind === "heartbeat");
  return (
    <div className="flex flex-col gap-4 text-sm">
      <p>
        Imported {result.monitors.length} monitor{result.monitors.length === 1 ? "" : "s"}
        {result.notifiers &&
          ` and ${result.notifiers.length} notification channel${result.notifiers.length === 1 ? "" : "s"}`}
        .
      </p>
      {heartbeats.length > 0 && (
        <div className="rounded-lg border border-paused/40 bg-paused/5 p-3">
          <p className="font-medium">{heartbeatsTitle}</p>
          <p className="mt-1 text-muted-foreground">{heartbeatsHint}</p>
          <ul className="mt-2 flex flex-col gap-1">
            {heartbeats.map((h) => (
              <li key={h.id}>
                <Link to={monitorPath(h)} className="font-medium underline">
                  {h.name}
                </Link>
              </li>
            ))}
          </ul>
        </div>
      )}
      {result.skipped.length > 0 && <SkippedList items={result.skipped} title="Not imported" />}
      <div>
        <Button variant="outline" onClick={onDone}>
          Done
        </Button>
      </div>
    </div>
  );
}
