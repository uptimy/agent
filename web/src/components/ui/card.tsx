import * as React from "react";
import { cn } from "@/lib/utils";

// A section card is a heading on the page with only its content framed, the
// Uptimy app's PageSection: one box level, never a box around a titled block.
const SectionContext = React.createContext(false);

/**
 * A framed box. With `section`, the header sits on the page and only
 * CardContent is framed, which is what a titled block should be.
 */
export function Card({
  className,
  section = false,
  ...props
}: React.HTMLAttributes<HTMLDivElement> & { section?: boolean }) {
  return (
    <SectionContext.Provider value={section}>
      <div className={cn(section ? "flex flex-col" : "rounded-xl border bg-card", className)} {...props} />
    </SectionContext.Provider>
  );
}

export function CardHeader({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  const section = React.useContext(SectionContext);
  return <div className={cn("flex flex-col gap-1", section ? "pb-3" : "p-5 pb-3", className)} {...props} />;
}

export function CardTitle({ className, ...props }: React.HTMLAttributes<HTMLHeadingElement>) {
  return <h3 className={cn("font-semibold leading-none", className)} {...props} />;
}

export function CardDescription({ className, ...props }: React.HTMLAttributes<HTMLParagraphElement>) {
  return <p className={cn("text-sm text-muted-foreground", className)} {...props} />;
}

export function CardContent({ className, ...props }: React.HTMLAttributes<HTMLDivElement>) {
  const section = React.useContext(SectionContext);
  return <div className={cn(section ? "rounded-xl border bg-card p-5" : "p-5 pt-0", className)} {...props} />;
}
