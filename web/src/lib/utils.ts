import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatUptime(ratio: number | null | undefined) {
  if (ratio == null) return "—";
  const pct = ratio * 100;
  return `${pct >= 99.995 ? "100" : pct.toFixed(2)}%`;
}

export function formatLatency(ms: number | null | undefined) {
  if (ms == null) return "—";
  return ms >= 1000 ? `${(ms / 1000).toFixed(2)}s` : `${Math.round(ms)}ms`;
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

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" });

/** Relative to now, past or future: "5 minutes ago", "in 3 hours". */
export function timeAgo(iso: string) {
  const diff = (new Date(iso).getTime() - Date.now()) / 1000;
  const abs = Math.abs(diff);
  if (abs < 60) return rtf.format(Math.round(diff), "second");
  if (abs < 3600) return rtf.format(Math.round(diff / 60), "minute");
  if (abs < 86400) return rtf.format(Math.round(diff / 3600), "hour");
  return rtf.format(Math.round(diff / 86400), "day");
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
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
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
