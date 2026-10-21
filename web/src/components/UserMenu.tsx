import { useEffect, useRef, useState } from "react";
import { Link } from "react-router";
import { BookOpen, Bug, ChevronsUpDown, Link2, LogOut, Monitor, Moon, Sun, UserRound } from "lucide-react";
import { cn } from "@/lib/utils";
import { useMe, useSignOut } from "@/components/AuthGate";
import { useTheme, type ThemePreference } from "@/lib/theme";

export function Avatar({ name, className }: { name: string; className?: string }) {
  return (
    <span
      aria-hidden
      className={cn(
        "grid size-7 shrink-0 place-items-center rounded-full bg-brand text-xs font-semibold text-white uppercase",
        className,
      )}
    >
      {name.slice(0, 1)}
    </span>
  );
}

const fade = "transition-opacity duration-[220ms] ease-[cubic-bezier(0.4,0,0.2,1)]";

/**
 * The signed-in user at the bottom of the sidebar, like the Uptimy app's:
 * opens a menu with the account page, theme, help links and sign out. In the
 * collapsed rail only the avatar shows and the menu opens to the right.
 */
export function UserMenu({ collapsed = false }: { collapsed?: boolean }) {
  const me = useMe();
  const signOut = useSignOut();
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onClick);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onClick);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const item =
    "flex h-10 w-full items-center gap-3 rounded-md px-3 text-left text-sm font-medium transition-colors hover:bg-nav-hover focus-visible:bg-nav-hover focus-visible:outline-none [&_svg]:size-[18px] [&_svg]:shrink-0";

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Account menu"
        onClick={() => setOpen((o) => !o)}
        // Same hover as the nav items so it reads as clickable. The strip
        // behind it is bg-sunken, which equals the nav hover tint in dark
        // mode, so the row lifts to the card surface instead. Lit while open.
        className={cn(
          "flex h-12 w-full cursor-pointer items-center gap-3 overflow-hidden rounded-xl border border-transparent px-1 text-left whitespace-nowrap transition-[background-color,border-color] duration-200 hover:border-brand hover:bg-card focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
          open && "border-brand bg-card",
        )}
      >
        <Avatar name={me.username} className="size-8" />
        <div className={cn("min-w-0 flex-1", fade, collapsed && "opacity-0")}>
          <div className="truncate text-sm font-semibold">{me.username}</div>
          <div className="truncate text-xs text-muted-foreground">{me.role === "admin" ? "Admin" : "Viewer"}</div>
        </div>
        <ChevronsUpDown
          className={cn("mr-2 size-3.5 shrink-0 text-muted-foreground", fade, collapsed && "opacity-0")}
        />
      </button>

      {open && (
        <div
          role="menu"
          className={cn(
            "absolute z-40 w-60 max-w-[calc(100vw-32px)] rounded-xl border bg-card shadow-xl",
            collapsed ? "bottom-0 left-full ml-3" : "bottom-full left-0 mb-2",
          )}
          onClick={() => setOpen(false)}
        >
          <div className="flex flex-col gap-1 p-4">
            <Link role="menuitem" to="/account" className={item}>
              <UserRound /> My Profile
            </Link>
            <ThemeSwitch />
          </div>
          <div className="h-px bg-border" />
          <div className="flex flex-col gap-1 p-4">
            <a
              role="menuitem"
              className={item}
              href="https://github.com/uptimy/agent#readme"
              target="_blank"
              rel="noopener"
            >
              <BookOpen /> Documentation
            </a>
            <a
              role="menuitem"
              className={item}
              href="https://www.upti.my/?utm_source=uptimy-agent&utm_medium=user_menu"
              target="_blank"
              rel="noopener"
            >
              <Link2 /> Uptimy Cloud
            </a>
            <a
              role="menuitem"
              className={item}
              href="https://github.com/uptimy/agent/issues/new"
              target="_blank"
              rel="noopener"
            >
              <Bug /> Report a Bug
            </a>
          </div>
          <div className="h-px bg-border" />
          <div className="p-4">
            <button
              role="menuitem"
              type="button"
              className={cn(item, "text-down hover:bg-down/10 focus-visible:bg-down/10")}
              onClick={() => signOut.mutate()}
            >
              <LogOut /> Sign Out
            </button>
          </div>
        </div>
      )}
    </div>
  );
}

const themeOptions: { value: ThemePreference; label: string; icon: typeof Sun }[] = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
];

function resolvedIcon(preference: ThemePreference) {
  const Icon = themeOptions.find((o) => o.value === preference)!.icon;
  return <Icon className="size-[18px]" />;
}

/** Light / Dark / System, remembered per browser. */
function ThemeSwitch() {
  const { preference, setTheme } = useTheme();
  return (
    <div className="flex h-10 items-center justify-between gap-2 px-3">
      <span className="flex items-center gap-3 text-sm font-medium">
        {resolvedIcon(preference)}
        Theme
      </span>
      <div role="radiogroup" aria-label="Theme" className="flex rounded-md border p-0.5">
        {themeOptions.map(({ value, label, icon: Icon }) => (
          <button
            key={value}
            type="button"
            role="radio"
            aria-checked={preference === value}
            data-tip={label}
            onClick={(e) => {
              e.stopPropagation(); // keep the menu open to see the change
              setTheme(value);
            }}
            className={cn(
              "grid size-7 place-items-center rounded transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
              preference === value ? "bg-muted text-foreground" : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="size-4" />
            <span className="sr-only">{label}</span>
          </button>
        ))}
      </div>
    </div>
  );
}
