import type * as React from "react";
import { cn } from "@/lib/utils";

/** A keyboard key, like Chakra's Kbd in the Uptimy app. */
export function Kbd({ className, ...props }: React.HTMLAttributes<HTMLElement>) {
  return (
    <kbd
      className={cn(
        "inline-flex h-5 min-w-5 items-center justify-center rounded border border-b-2 bg-muted px-1.5 font-sans text-xs font-medium text-muted-foreground",
        className,
      )}
      {...props}
    />
  );
}
