import { cn } from "@/lib/utils";

/**
 * A filter as chips, like the Uptimy app's FilterChips: a label, then one chip
 * per option with an optional count; the selected one is filled. Chips are
 * buttons, so they're reachable by keyboard and announce their state.
 */
export function FilterChips<V extends string>({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { value: V; label: string; count?: number }[];
  value: V;
  onChange: (value: V) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2" role="group" aria-label={label}>
      <span className="text-sm text-muted-foreground">{label}:</span>
      {options.map((o) => {
        const selected = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            aria-pressed={selected}
            onClick={() => onChange(o.value)}
            className={cn(
              "inline-flex h-7 items-center gap-1 rounded-full border px-3 text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
              selected
                ? "border-primary bg-primary text-primary-foreground"
                : "text-muted-foreground hover:bg-nav-hover hover:text-foreground",
            )}
          >
            {o.label}
            {o.count != null && <span className="tabular-nums">({o.count})</span>}
          </button>
        );
      })}
    </div>
  );
}
