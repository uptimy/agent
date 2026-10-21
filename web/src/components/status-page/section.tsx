// The status page's sections, as on the hosted Uptimy status pages
// (upti.my-status app/components/status-section.tsx): a heading on the page
// with an optional legend, count or link, and one frame of rows separated by
// hairlines. One box level, never a box in a box, no tinted header bars.
import type { CSSProperties, ReactNode } from "react";
import { cn } from "@/lib/utils";

export function StatusSection({
  title,
  icon,
  aside,
  titleStyle,
  className,
  children,
}: {
  title: ReactNode;
  icon?: ReactNode;
  /** Right side of the heading: the colour legend, a count, a link. */
  aside?: ReactNode;
  titleStyle?: CSSProperties;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section className={className}>
      {/* Phones: title, then the aside on its own line, always. Wider: one row. */}
      <div className="mb-3 flex flex-col items-start gap-1.5 sm:flex-row sm:items-center sm:justify-between sm:gap-2">
        <h2 className="flex items-center gap-2 text-base font-semibold" style={titleStyle}>
          {icon}
          {title}
        </h2>
        {aside}
      </div>
      {children}
    </section>
  );
}

/** The frame holding a section's rows. */
export function StatusList({ children }: { children: ReactNode }) {
  return <div className="divide-y overflow-hidden rounded-xl border bg-card">{children}</div>;
}

/** The one line an empty section shows. */
export function StatusEmpty({ icon, children }: { icon?: ReactNode; children: ReactNode }) {
  return (
    <div className={cn("flex items-center gap-2 px-4 py-4 text-sm text-muted-foreground sm:px-5")}>
      {icon}
      {children}
    </div>
  );
}

/** The bar colours, shown once per section. */
export function UptimeLegend() {
  const items = [
    { className: "bg-up", label: "99.5%+" },
    { className: "bg-minor", label: "99–99.5%" },
    { className: "bg-degraded", label: "95–99%" },
    { className: "bg-down", label: "<95%" },
    { className: "bg-no-data", label: "No data" },
  ];
  return (
    <div className="flex items-center gap-3 text-xs text-muted-foreground">
      {items.map((i) => (
        <span key={i.label} className="flex items-center gap-1">
          <span className={cn("size-2 rounded-sm", i.className)} />
          {i.label}
        </span>
      ))}
    </div>
  );
}
