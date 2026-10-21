import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";
import type { CheckEvidence } from "./types";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export function formatLatency(ms: number | null | undefined): string {
  if (ms == null) return "—";
  if (ms < 1) return `${ms.toFixed(2)}ms`;
  if (ms < 100) return `${ms.toFixed(1)}ms`;
  return `${Math.round(ms)}ms`;
}

export function formatUptime(pct: number | null | undefined): string {
  if (pct == null) return "—";
  return `${pct.toFixed(2)}%`;
}

const DAY_MS = 86_400_000;

/**
 * 24-hour uptime for a monitor, or "collecting" while it is younger than a
 * day. A monitor created two minutes ago has no meaningful 24h figure, and
 * printing 0.00% reads as an outage that never happened.
 */
export function uptimeLabel(
  pct: number | null | undefined,
  createdAt: string | null | undefined,
  now = Date.now(),
): string {
  if (pct == null) return "—";
  if (createdAt) {
    const age = now - new Date(createdAt).getTime();
    if (age >= 0 && age < DAY_MS) return "collecting";
  }
  return formatUptime(pct);
}

/** Elapsed time since iso as a bare duration, e.g. "8m" or "3d". */
export function since(iso: string | null | undefined, now = Date.now()): string {
  if (!iso) return "";
  const secs = Math.max(0, Math.floor((now - new Date(iso).getTime()) / 1000));
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

/** Human window label for a series, e.g. "last 3 hours". */
export function windowLabel(firstTs: number, lastTs: number): string {
  const mins = Math.max(1, Math.round((lastTs - firstTs) / 60));
  if (mins < 90) return `last ${mins} min`;
  const hours = Math.round(mins / 60);
  if (hours < 36) return `last ${hours} hour${hours === 1 ? "" : "s"}`;
  const days = Math.round(hours / 24);
  return `last ${days} day${days === 1 ? "" : "s"}`;
}

export function timeAgo(iso: string | null | undefined): string {
  if (!iso) return "never";
  const seconds = Math.floor((Date.now() - new Date(iso).getTime()) / 1000);
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export function formatDateTime(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  });
}

export function formatInterval(seconds: number): string {
  if (seconds < 60) return `${seconds}s`;
  if (seconds < 3600 && seconds % 60 === 0) return `${seconds / 60}m`;
  if (seconds % 3600 === 0) return `${seconds / 3600}h`;
  return `${Math.floor(seconds / 60)}m ${seconds % 60}s`;
}

export const CERT_WARN_DAYS = 14;

export function certDaysLeft(iso: string | null | undefined, now = Date.now()): number | null {
  if (!iso) return null;
  const ms = new Date(iso).getTime() - now;
  if (Number.isNaN(ms)) return null;
  return Math.ceil(ms / 86_400_000);
}

export function formatCertExpiry(iso: string | null | undefined): string {
  const days = certDaysLeft(iso);
  if (days == null) return "—";
  if (days < 0) return `expired ${Math.abs(days)}d ago`;
  if (days === 0) return "expires today";
  if (days === 1) return "1 day";
  return `${days} days`;
}

/** Domains are renewed on a longer cycle than certificates. */
export const DOMAIN_WARN_DAYS = 30;

export function domainIsWarning(iso: string | null | undefined): boolean {
  const days = certDaysLeft(iso);
  return days != null && days <= DOMAIN_WARN_DAYS;
}

export function certIsWarning(iso: string | null | undefined): boolean {
  const days = certDaysLeft(iso);
  return days != null && days <= CERT_WARN_DAYS;
}

export function isMuted(
  state: { muted_until?: string | null } | null | undefined,
  now = Date.now(),
): boolean {
  if (!state?.muted_until) return false;
  const t = new Date(state.muted_until).getTime();
  return !Number.isNaN(t) && t > now;
}

export function isAcked(
  state: { acked_at?: string | null; acked_until?: string | null } | null | undefined,
  now = Date.now(),
): boolean {
  if (!state?.acked_at) return false;
  if (!state.acked_until) return true;
  const t = new Date(state.acked_until).getTime();
  return !Number.isNaN(t) && t > now;
}

const TYPE_LABELS: Record<string, string> = {
  icmp: "ICMP",
  http: "HTTP",
  tcp: "TCP",
  dns: "DNS",
  cert: "CERT",
  domain: "DOMAIN",
  smtp: "SMTP",
  imap: "IMAP",
  pop3: "POP3",
  postgres: "POSTGRES",
  mysql: "MYSQL",
  redis: "REDIS",
  transaction: "TXN",
  push: "PUSH",
  group: "GROUP",
};

/**
 * Checks whose latency is not the service's: a registry lookup, a TLS
 * handshake once an hour, a heartbeat, or a group with no check of its own.
 */
export const NOT_SERVICE_LATENCY = new Set(["domain", "cert", "push", "group"]);

/** The short name a monitor type goes by in a list. */
export function typeLabel(type: string): string {
  return TYPE_LABELS[type] ?? type.toUpperCase();
}

/**
 * Why the last failed check failed, in one line. A transaction names the
 * step that broke, since "step 3 of 4" says where to look.
 */
export function failureLine(monitor: {
  config?: { steps?: unknown[] } | null;
  state?: { last_failure_evidence?: CheckEvidence | null } | null;
}): string {
  const ev = monitor.state?.last_failure_evidence;
  if (!ev) return "";
  const steps = ev.steps ?? [];
  if (steps.length > 0) {
    const failed = steps.findIndex((s) => s.error);
    const i = failed < 0 ? steps.length - 1 : failed;
    const step = steps[i];
    const total = Math.max(monitor.config?.steps?.length ?? 0, steps.length);
    const what = step.error || (step.status_code ? `HTTP ${step.status_code}` : ev.error || "failed");
    return `Step ${i + 1} of ${total} · ${step.name}: ${what}`;
  }
  return ev.error || ev.tls_error || (ev.status_code ? `HTTP ${ev.status_code}` : "");
}

export function splitTags(tags: string | null | undefined): string[] {
  if (!tags) return [];
  return tags
    .split(",")
    .map((t) => t.trim())
    .filter(Boolean);
}

/** Availability plus slow warning. Down always wins. Stale means the probe stopped checking. */
export function visualStatus(
  monitor: {
    enabled: boolean;
    interval_seconds?: number;
    state?: { status?: string; slow?: boolean; stale?: boolean; last_check_at?: string | null } | null;
  },
): "up" | "down" | "pending" | "slow" | "muted" | "stale" {
  if (!monitor.enabled) return "muted";
  if (monitor.state?.stale) return "stale";
  const status = monitor.state?.status ?? "pending";
  if (status === "down") return "down";
  if (status === "pending") return "pending";
  if (monitor.state?.slow) return "slow";
  if (status === "up") return "up";
  return "pending";
}

function relativeLuminance(hex: string): number | null {
  let h = hex.trim().replace(/^#/, "");
  if (h.length === 3) h = h.split("").map((c) => c + c).join("");
  if (!/^[0-9a-fA-F]{6}$/.test(h)) return null;
  const [r, g, b] = [0, 2, 4].map((i) => {
    const c = parseInt(h.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

/**
 * Applies a branding accent, or restores the theme's own when there is none.
 * Text on the accent is white or near-black, whichever reads better, so a
 * customer's colour never leaves a button unreadable.
 */
export function applyAccent(root: HTMLElement, accent: string | undefined | null) {
  const color = accent?.trim() ?? "";
  const lum = color ? relativeLuminance(color) : null;
  if (lum === null) {
    root.style.removeProperty("--primary");
    root.style.removeProperty("--ring");
    root.style.removeProperty("--primary-foreground");
    return;
  }
  root.style.setProperty("--primary", color);
  root.style.setProperty("--ring", color);
  // Contrast against white vs against #071430 (luminance ~0.0076).
  const onWhite = 1.05 / (lum + 0.05);
  const onDark = (lum + 0.05) / (0.0076 + 0.05);
  root.style.setProperty("--primary-foreground", onWhite >= onDark ? "#ffffff" : "#071430");
}
