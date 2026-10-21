import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

/** Uptime with two decimals, as everywhere in Uptimy: 99.98%, 100.00%. */
export function formatUptime(ratio: number | null | undefined) {
  if (ratio == null) return "—";
  return `${(ratio * 100).toFixed(2)}%`;
}

/** Response time: ms below a second, seconds with one decimal above (1.8 s). */
export function formatLatency(ms: number | null | undefined) {
  if (ms == null) return "—";
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)} s` : `${Math.round(ms)} ms`;
}

export function formatInterval(seconds: number) {
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  if (seconds % 60 === 0) return `${seconds / 60}m`;
  return `${seconds}s`;
}

/** 21600 → "6 hours", 604800 → "1 week". */
export function formatSpan(seconds: number) {
  const units: [number, string][] = [
    [31536000, "year"],
    [604800, "week"],
    [86400, "day"],
    [3600, "hour"],
    [60, "minute"],
  ];
  const [size, name] = units.find(([size]) => seconds >= size && seconds % size === 0) ?? [1, "second"];
  const n = seconds / size;
  return `${n} ${name}${n === 1 ? "" : "s"}`;
}

/** The stop nearest to seconds, so a typed value still places the thumb. */
export function nearestStep(seconds: number, steps: number[]) {
  let best = 0;
  steps.forEach((s, i) => {
    if (Math.abs(s - seconds) < Math.abs(steps[best] - seconds)) best = i;
  });
  return best;
}

/** 5 → "5 min", 60 → "1 hour", 240 → "4 hours". */
export function formatMinutes(minutes: number) {
  if (minutes % 60 === 0) return minutes === 60 ? "1 hour" : `${minutes / 60} hours`;
  return `${minutes} min`;
}

/** "15:30" in the viewer's locale, with the date when it isn't today. */
export function formatClock(iso: string) {
  const d = new Date(iso);
  const time = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  return d.toDateString() === new Date().toDateString()
    ? time
    : `${d.toLocaleDateString(undefined, { weekday: "short" })} ${time}`;
}

/**
 * Relative to now, compact like the Uptimy app: "just now", "5m ago", "3h ago",
 * "2d ago", "in 4m". Older than a week falls back to weeks, months and years.
 */
export function timeAgo(iso: string) {
  const diff = Math.round((Date.now() - new Date(iso).getTime()) / 1000);
  const future = diff < 0;
  const abs = Math.abs(diff);
  if (abs < 5) return "just now";
  const units: [number, string][] = [
    [31_536_000, "y"],
    [2_592_000, "mo"],
    [604_800, "w"],
    [86_400, "d"],
    [3_600, "h"],
    [60, "m"],
    [1, "s"],
  ];
  const [size, unit] = units.find(([size]) => abs >= size)!;
  const n = `${Math.floor(abs / size)}${unit}`;
  return future ? `in ${n}` : `${n} ago`;
}

/** timeAgo within the last week; older ones have their date beside them already. */
export function recentAgo(iso: string) {
  return Date.now() - new Date(iso).getTime() < 7 * 86400_000 ? timeAgo(iso) : null;
}

export function formatDateTime(iso: string) {
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/** A heartbeat's ping URL. */
export function pingURL(token: string) {
  return `${window.location.origin}/ping/${token}`;
}

/** 850 → "850 ms", 75_000 → "1m 15s", 5_400_000 → "1h 30m". */
export function formatDuration(ms: number) {
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const s = Math.round(ms / 1000);
  const m = Math.floor(s / 60);
  if (m < 60) return s % 60 ? `${m}m ${s % 60}s` : `${m}m`;
  const h = Math.floor(m / 60);
  return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
}

/** An ISO time as a datetime-local value, in the viewer's time zone. */
export function toLocalInput(iso: string) {
  const d = new Date(iso);
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export const fromLocalInput = (v: string) => new Date(v).toISOString();
