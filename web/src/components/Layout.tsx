import { useEffect, useState } from "react";
import { NavLink, Outlet, useLocation } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Bell,
  Siren,
  Wrench,
  FileText,
  HeartPulse,
  LayoutDashboard,
  Menu,
  PanelLeftClose,
  PanelLeftOpen,
  Search,
  Settings,
  Users,
  X,
  type LucideIcon,
} from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { PALETTE_KEYS, useKeyboardShortcuts } from "@/lib/shortcuts";
import { toggleSidebarCollapsed, useSidebarCollapsed } from "@/lib/sidebar";
import { UptimyLogo, UptimyMark } from "@/components/brand";
import { useCanEdit, useMe } from "@/components/AuthGate";
import { UserMenu } from "@/components/UserMenu";
import { CommandPalette } from "@/components/CommandPalette";
import { Kbd } from "@/components/ui/kbd";
import { SideTooltip } from "@/components/ui/side-tooltip";
import { ErrorBoundary } from "@/components/ErrorBoundary";

export function Logo({ className }: { className?: string }) {
  return (
    <span className={cn("inline-flex shrink-0 items-center", className)}>
      <UptimyLogo />
    </span>
  );
}

// Same sections, icons and order as the Uptimy app's sidebar, so moving
// between the agent and the platform feels familiar.
const nav: { to: string; label: string; icon: LucideIcon; end?: boolean; adminOnly?: boolean }[] = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard, end: true },
  { to: "/healthchecks", label: "Healthchecks", icon: HeartPulse },
  { to: "/heartbeats", label: "Heartbeats", icon: Activity },
  { to: "/status-page", label: "Status page", icon: FileText },
  { to: "/incidents", label: "Incidents", icon: Siren },
  { to: "/maintenance", label: "Maintenance", icon: Wrench },
  { to: "/notifications", label: "Notifications", icon: Bell },
  { to: "/users", label: "Users", icon: Users, adminOnly: true },
  { to: "/settings", label: "Settings", icon: Settings },
];

// As in the app (components/common/Sidebar.tsx): every row keeps the same
// height and padding in both states, so icons stay put and only the width
// animates. 12px container + 1px border + 10px = icon centred in the 64px rail.
const fade = "transition-opacity duration-[220ms] ease-[cubic-bezier(0.4,0,0.2,1)]";
const row = "flex w-full items-center gap-3 overflow-hidden rounded-xl border px-[10px] whitespace-nowrap";

export function Layout() {
  const location = useLocation();
  // The drawer is open for the page it was opened on, so navigating closes it.
  const [openOn, setOpenOn] = useState<string | null>(null);
  const open = openOn === location.pathname;
  const setOpen = (o: boolean) => setOpenOn(o ? location.pathname : null);
  const collapsed = useSidebarCollapsed();
  const canEdit = useCanEdit();
  const { paletteOpen, openPalette, closePalette } = useKeyboardShortcuts({ canEdit });

  // Escape closes the mobile drawer.
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpenOn(null);
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open]);

  return (
    <div className="min-h-dvh">
      <aside className="fixed inset-y-0 left-0 z-30 hidden w-(--sidebar-w,15rem) border-r-[1.5px] bg-card shadow-lg transition-[width] duration-[220ms] ease-[cubic-bezier(0.4,0,0.2,1)] md:block">
        <SidebarContent collapsed={collapsed} onOpenPalette={openPalette} />
      </aside>

      {/* Mobile: a top bar with a drawer. */}
      <div className="sticky top-0 z-20 flex h-14 items-center gap-3 border-b bg-card px-4 md:hidden">
        <button
          type="button"
          aria-label="Open navigation"
          onClick={() => setOpen(true)}
          className="grid size-9 place-items-center rounded-md hover:bg-muted"
        >
          <Menu className="size-5" />
        </button>
        <NavLink to="/">
          <Logo />
        </NavLink>
      </div>
      {open && (
        <div className="fixed inset-0 z-40 md:hidden">
          <div className="absolute inset-0 bg-black/50" onClick={() => setOpen(false)} />
          <aside className="absolute inset-y-0 left-0 w-[75vw] max-w-[280px] bg-card shadow-xl">
            <SidebarContent onClose={() => setOpen(false)} onOpenPalette={openPalette} />
          </aside>
        </div>
      )}

      <div className="transition-[padding] duration-[220ms] ease-[cubic-bezier(0.4,0,0.2,1)] md:pl-(--sidebar-w,15rem)">
        <main className="mx-auto max-w-7xl px-4 py-6 sm:px-6 md:px-8 md:py-10">
          <ErrorBoundary key={location.pathname}>
            <Outlet />
          </ErrorBoundary>
        </main>
      </div>

      <CommandPalette open={paletteOpen} onClose={closePalette} />
    </div>
  );
}

function SidebarContent({
  collapsed = false,
  onClose,
  onOpenPalette,
}: {
  /** Icons only; desktop only, the mobile drawer is always expanded. */
  collapsed?: boolean;
  onClose?: () => void;
  onOpenPalette: () => void;
}) {
  const me = useMe();
  const info = useQuery({ queryKey: ["info"], queryFn: api.info });

  return (
    <div className="flex h-dvh flex-col">
      {/* Both logos stay mounted and crossfade so the header never reflows. */}
      <div className="flex h-16 shrink-0 items-center overflow-hidden border-b bg-sunken px-4 md:px-0">
        {onClose && (
          <button
            type="button"
            aria-label="Close navigation"
            onClick={onClose}
            className="grid size-8 place-items-center rounded-md hover:bg-muted"
          >
            <X className="size-4" />
          </button>
        )}
        <NavLink to="/" aria-label="Uptimy home" className="relative h-full flex-1">
          <span
            className={cn(
              "absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2",
              fade,
              collapsed && "opacity-0",
            )}
          >
            <UptimyLogo className="h-9 max-w-none" />
          </span>
          <span
            aria-hidden
            className={cn(
              "absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2",
              fade,
              !collapsed && "opacity-0",
            )}
          >
            <UptimyMark className="h-[18px] max-w-none" />
          </span>
        </NavLink>
      </div>

      {/* Search: opens the command palette, and shows people that ⌘K exists. */}
      <div className="shrink-0 px-3 pt-3 pb-1">
        <SideTooltip
          disabled={!collapsed}
          content={
            <span className="flex items-center gap-2">
              Search <Kbd className="border-white/20 bg-white/10 text-white">{PALETTE_KEYS}</Kbd>
            </span>
          }
        >
          <button
            type="button"
            aria-label="Search"
            onClick={() => {
              onClose?.();
              onOpenPalette();
            }}
            className={cn(
              row,
              "h-10 cursor-pointer text-muted-foreground transition-colors hover:border-brand hover:bg-nav-hover",
            )}
          >
            <Search className="size-[18px] shrink-0" />
            <span className={cn("min-w-0 flex-1 text-left text-sm", fade, collapsed && "opacity-0")}>Search…</span>
            <Kbd className={cn("shrink-0", fade, collapsed && "opacity-0")}>{PALETTE_KEYS}</Kbd>
          </button>
        </SideTooltip>
      </div>

      <nav className="flex flex-1 flex-col gap-1 overflow-x-hidden overflow-y-auto px-3 py-2">
        {nav
          .filter((n) => !n.adminOnly || me.role === "admin")
          .map(({ to, label, icon: Icon, end }) => (
            <SideTooltip key={to} content={label} disabled={!collapsed}>
              <NavLink
                to={to}
                end={end}
                onClick={onClose}
                aria-label={collapsed ? label : undefined}
                className={({ isActive }) =>
                  cn(
                    row,
                    "h-11 text-sm transition-[background-color,border-color] duration-200 focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
                    isActive
                      ? "border-brand bg-brand font-semibold text-white"
                      : "border-transparent font-medium hover:border-brand hover:bg-nav-hover",
                  )
                }
              >
                {({ isActive }) => (
                  <>
                    <Icon className="size-[18px] shrink-0" />
                    <span className={cn("min-w-0 flex-1", fade, collapsed && "opacity-0")}>{label}</span>
                    {isActive && (
                      <span
                        className={cn(
                          "size-2 shrink-0 rounded-full bg-white",
                          fade,
                          collapsed ? "opacity-0" : "opacity-80",
                        )}
                      />
                    )}
                  </>
                )}
              </NavLink>
            </SideTooltip>
          ))}
      </nav>

      {/* Collapse toggle (desktop only) */}
      <div className="hidden shrink-0 px-3 pb-2 md:block">
        <SideTooltip
          content={
            <span className="flex items-center gap-2">
              {collapsed ? "Expand sidebar" : "Collapse sidebar"}{" "}
              <Kbd className="border-white/20 bg-white/10 text-white">[</Kbd>
            </span>
          }
        >
          <button
            type="button"
            onClick={toggleSidebarCollapsed}
            aria-label={collapsed ? "Expand sidebar" : "Collapse sidebar"}
            className={cn(
              row,
              "h-10 cursor-pointer border-transparent text-sm font-medium text-muted-foreground transition-colors hover:bg-nav-hover hover:text-foreground",
            )}
          >
            {collapsed ? (
              <PanelLeftOpen className="size-[18px] shrink-0" />
            ) : (
              <PanelLeftClose className="size-[18px] shrink-0" />
            )}
            <span className={cn("flex-1 text-left", fade, collapsed && "opacity-0")}>Collapse</span>
          </button>
        </SideTooltip>
      </div>

      <div className="shrink-0 border-t bg-sunken px-3 py-3">
        <UserMenu collapsed={collapsed} />
        {/* Fixed-height slot so the strip never changes height between
            states: the line crossfades to a GitHub mark in the rail. */}
        <div className="relative mt-2 h-4 text-xs leading-4 text-muted-foreground">
          <div
            aria-hidden={collapsed || undefined}
            className={cn(
              "overflow-hidden text-center whitespace-nowrap",
              fade,
              collapsed && "pointer-events-none opacity-0",
            )}
          >
            Uptimy Agent {info.data?.version} ·{" "}
            <a
              className="hover:text-foreground"
              href="https://github.com/uptimy/agent"
              target="_blank"
              rel="noreferrer"
              tabIndex={collapsed ? -1 : undefined}
            >
              GitHub
            </a>
          </div>
          <a
            href="https://github.com/uptimy/agent"
            target="_blank"
            rel="noreferrer"
            aria-label="Uptimy Agent on GitHub"
            aria-hidden={!collapsed || undefined}
            tabIndex={collapsed ? undefined : -1}
            className={cn(
              "absolute inset-0 grid place-items-center hover:text-foreground",
              fade,
              !collapsed && "pointer-events-none opacity-0",
            )}
          >
            <GitHubMark className="size-3.5" />
          </a>
        </div>
      </div>
    </div>
  );
}

export function PageHeader({
  title,
  icon: Icon,
  description,
  actions,
}: {
  title: React.ReactNode;
  icon?: LucideIcon;
  description?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    // Side by side from sm up, whatever the description's length: it wraps
    // within its column instead of pushing the actions to a row of their own.
    <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end sm:justify-between">
      <div className="min-w-0 flex-1">
        <h1 className="flex items-center gap-3 text-2xl font-bold tracking-tight md:text-3xl">
          {Icon && <Icon className="size-6 shrink-0 md:size-7" />}
          {title}
        </h1>
        {description && <div className="mt-2 max-w-2xl text-sm text-muted-foreground md:text-base">{description}</div>}
      </div>
      {actions && <div className="flex shrink-0 items-center gap-2">{actions}</div>}
    </div>
  );
}

export function ErrorNote({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <p role="alert" className="rounded-md border border-down/30 bg-down/10 px-3 py-2 text-sm text-down">
      {error instanceof Error ? error.message : String(error)}
    </p>
  );
}

/** Lucide's former GitHub icon; lucide-react 1.x dropped brand icons. */
function GitHubMark({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
      className={className}
    >
      <path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36-.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4" />
      <path d="M9 18c-4.51 2-5-2-7-2" />
    </svg>
  );
}
