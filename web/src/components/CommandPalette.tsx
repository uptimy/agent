import { Fragment, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  Activity,
  Bell,
  Wrench,
  BookOpen,
  Bug,
  FileText,
  HeartPulse,
  LayoutDashboard,
  LogOut,
  MessagesSquare,
  Moon,
  PanelLeftClose,
  Plus,
  Search,
  Settings,
  Sun,
  UserRound,
  Users,
  type LucideIcon,
} from "lucide-react";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";
import { monitorPath } from "@/lib/monitors";
import { shortcutFor } from "@/lib/shortcuts";
import { toggleSidebarCollapsed } from "@/lib/sidebar";
import { useTheme } from "@/lib/theme";
import { useMe, useSignOut } from "@/components/AuthGate";
import { HeartbeatDot, StatusDot } from "@/components/status";
import { Kbd } from "@/components/ui/kbd";

// Mirrors upti.my-app components/common/CommandPalette.tsx: grouped commands,
// arrow keys + Enter, the same footer. The agent also finds monitors by name.

type Category = "navigation" | "monitors" | "create" | "general" | "help";

const categories: { id: Category; title: string }[] = [
  { id: "monitors", title: "Monitors" },
  { id: "navigation", title: "Navigation" },
  { id: "create", title: "Create" },
  { id: "general", title: "General" },
  { id: "help", title: "Help & Support" },
];

interface Command {
  id: string;
  label: string;
  description?: string;
  icon: React.ComponentType<{ className?: string }>;
  action: () => void;
  shortcut?: string;
  category: Category;
}

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  // Mounted only while open, so every opening starts with an empty search.
  return open ? <Palette onClose={onClose} /> : null;
}

function Palette({ onClose }: { onClose: () => void }) {
  const navigate = useNavigate();
  const me = useMe();
  const signOut = useSignOut();
  const { resolved, setTheme } = useTheme();
  const healthchecks = useQuery({ queryKey: ["healthchecks"], queryFn: api.healthchecks });
  const heartbeats = useQuery({ queryKey: ["heartbeats"], queryFn: api.heartbeats });
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);

  const commands = useMemo<Command[]>(() => {
    const go = (to: string) => () => navigate(to);
    const page = (id: string, label: string, icon: LucideIcon, to: string): Command => ({
      id,
      label,
      icon,
      action: go(to),
      shortcut: shortcutFor(to),
      category: "navigation",
    });
    const list: Command[] = [
      // Same order as the sidebar
      page("go-dashboard", "Go to Dashboard", LayoutDashboard, "/"),
      page("go-healthchecks", "Go to Healthchecks", HeartPulse, "/healthchecks"),
      page("go-heartbeats", "Go to Heartbeats", Activity, "/heartbeats"),
      page("go-status-page", "Go to Status page", FileText, "/status-page"),
      page("go-maintenance", "Go to Maintenance", Wrench, "/maintenance"),
      page("go-notifications", "Go to Notifications", Bell, "/notifications"),
      ...(me.role === "admin" ? [page("go-users", "Go to Users", Users, "/users")] : []),
      page("go-settings", "Go to Settings", Settings, "/settings"),
      page("go-account", "Go to Account", UserRound, "/account"),
    ];
    if (me.role === "admin") {
      list.push(
        {
          id: "new-healthcheck",
          label: "New Healthcheck",
          description: "HTTP, TCP, DNS, TLS, databases, Kubernetes",
          icon: Plus,
          action: go("/healthchecks/new"),
          shortcut: shortcutFor("/healthchecks/new"),
          category: "create",
        },
        {
          id: "new-heartbeat",
          label: "New Heartbeat",
          description: "Cron jobs and workers ping a URL",
          icon: Plus,
          action: go("/heartbeats/new"),
          shortcut: shortcutFor("/heartbeats/new"),
          category: "create",
        },
      );
    }
    list.push(
      {
        id: "toggle-sidebar",
        label: "Toggle Sidebar",
        icon: PanelLeftClose,
        action: toggleSidebarCollapsed,
        shortcut: "[",
        category: "general",
      },
      {
        id: "toggle-theme",
        label: resolved === "dark" ? "Switch to Light Theme" : "Switch to Dark Theme",
        icon: resolved === "dark" ? Sun : Moon,
        action: () => setTheme(resolved === "dark" ? "light" : "dark"),
        category: "general",
      },
      { id: "sign-out", label: "Sign Out", icon: LogOut, action: () => signOut.mutate(), category: "general" },
      {
        id: "docs",
        label: "Documentation",
        icon: BookOpen,
        action: () => window.open("https://github.com/uptimy/agent#readme", "_blank", "noopener"),
        category: "help",
      },
      {
        id: "discord",
        label: "Discord Community",
        icon: MessagesSquare,
        action: () => window.open("https://www.upti.my/discord", "_blank", "noopener"),
        category: "help",
      },
      {
        id: "bug",
        label: "Report a Bug",
        icon: Bug,
        action: () => window.open("https://github.com/uptimy/agent/issues/new", "_blank", "noopener"),
        category: "help",
      },
    );
    return list;
  }, [navigate, me.role, resolved, setTheme, signOut]);

  const q = query.trim().toLowerCase();

  const groups = useMemo(() => {
    const matches = q
      ? commands.filter((c) => c.label.toLowerCase().includes(q) || c.description?.toLowerCase().includes(q))
      : commands;
    // Monitors only once you type, so the palette opens on the commands.
    const found: Command[] = [];
    if (q) {
      const hit = (...texts: string[]) => texts.some((t) => t.toLowerCase().includes(q));
      for (const h of healthchecks.data ?? []) {
        if (!hit(h.name, h.target)) continue;
        found.push({
          id: `healthcheck-${h.id}`,
          label: h.name,
          description: h.target,
          icon: () => <StatusDot status={h.status} />,
          action: () => navigate(monitorPath(h)),
          category: "monitors",
        });
      }
      for (const h of heartbeats.data ?? []) {
        if (!hit(h.name, h.schedule)) continue;
        found.push({
          id: `heartbeat-${h.id}`,
          label: h.name,
          description: `Heartbeat · ${h.schedule}`,
          icon: () => <HeartbeatDot state={h.tracking.state} />,
          action: () => navigate(monitorPath(h)),
          category: "monitors",
        });
      }
    }
    found.splice(8);
    const all = [...found, ...matches];
    return categories
      .map((c) => ({ ...c, commands: all.filter((cmd) => cmd.category === c.id) }))
      .filter((g) => g.commands.length > 0);
  }, [commands, healthchecks.data, heartbeats.data, navigate, q]);

  const visible = useMemo(() => groups.flatMap((g) => g.commands), [groups]);

  useEffect(() => {
    listRef.current?.querySelector(`[data-cmd-index="${selected}"]`)?.scrollIntoView({ block: "nearest" });
  }, [selected]);

  const run = (cmd: Command) => {
    onClose();
    cmd.action();
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setSelected((i) => (i < visible.length - 1 ? i + 1 : 0));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setSelected((i) => (i > 0 ? i - 1 : visible.length - 1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      if (visible[selected]) run(visible[selected]);
    } else if (e.key === "Escape") {
      e.preventDefault();
      onClose();
    }
  };

  let index = -1;
  return (
    <div
      className="fixed inset-0 z-[9999] flex items-start justify-center bg-black/50 px-4 pt-[20vh] backdrop-blur-[4px]"
      onMouseDown={(e) => e.target === e.currentTarget && onClose()}
    >
      <div
        role="dialog"
        aria-label="Command palette"
        className="w-full max-w-[600px] overflow-hidden rounded-xl border bg-card shadow-xl"
        onKeyDown={onKeyDown}
      >
        <div className="flex items-center gap-3 border-b p-4">
          <Search className="size-5 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setSelected(0);
            }}
            placeholder="Type a command or search..."
            aria-label="Type a command or search"
            className="min-w-0 flex-1 bg-transparent text-lg outline-none placeholder:text-muted-foreground"
          />
          <Kbd>ESC</Kbd>
        </div>

        <div ref={listRef} className="max-h-[400px] overflow-y-auto p-2">
          {visible.length === 0 ? (
            <p className="p-4 text-center text-muted-foreground">No commands found</p>
          ) : (
            groups.map((group, gi) => (
              <Fragment key={group.id}>
                <div
                  className={cn(
                    "px-3 py-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase",
                    gi > 0 && "mt-2",
                  )}
                >
                  {group.title}
                </div>
                {group.commands.map((cmd) => {
                  index++;
                  const i = index;
                  const isSelected = i === selected;
                  const Icon = cmd.icon;
                  return (
                    <div
                      key={cmd.id}
                      data-cmd-index={i}
                      role="option"
                      aria-selected={isSelected}
                      onClick={() => run(cmd)}
                      onMouseMove={() => setSelected(i)}
                      className={cn(
                        "flex cursor-pointer items-center justify-between gap-3 rounded-lg px-3 py-2 transition-colors",
                        isSelected && "bg-nav-hover",
                      )}
                    >
                      <div className="flex min-w-0 items-center gap-3">
                        <span
                          className={cn(
                            "grid size-4 shrink-0 place-items-center",
                            isSelected ? "text-brand" : "text-muted-foreground",
                          )}
                        >
                          <Icon className="size-4" />
                        </span>
                        <div className="min-w-0">
                          <div className="truncate text-sm font-medium">{cmd.label}</div>
                          {cmd.description && (
                            <div className="truncate text-xs text-muted-foreground">{cmd.description}</div>
                          )}
                        </div>
                      </div>
                      {cmd.shortcut && (
                        <div className="flex shrink-0 gap-1">
                          {cmd.shortcut.split(" ").map((key, ki) => (
                            <Kbd key={ki}>{key}</Kbd>
                          ))}
                        </div>
                      )}
                    </div>
                  );
                })}
              </Fragment>
            ))
          )}
        </div>

        <div className="flex items-center justify-center gap-4 border-t bg-nav-hover p-3 text-xs text-muted-foreground">
          <span className="flex items-center gap-1">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> Navigate
          </span>
          <span className="flex items-center gap-1">
            <Kbd>↵</Kbd> Select
          </span>
          <span className="flex items-center gap-1">
            <Kbd>ESC</Kbd> Close
          </span>
        </div>
      </div>
    </div>
  );
}
