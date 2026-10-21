import type { Monitor, SeriesPoint } from "./types";
import { splitTags, visualStatus } from "./utils";

export type RealmMark = "down" | "slow" | "up";

export function monitorsInRealm(
  monitors: Monitor[],
  realmId: number | "unassigned",
): Monitor[] {
  if (realmId === "unassigned") {
    return monitors.filter((m) => !m.realm_ids || m.realm_ids.length === 0);
  }
  return monitors.filter((m) => (m.realm_ids ?? []).includes(realmId));
}

export function realmMark(members: Monitor[]): RealmMark {
  const statuses = members.map((m) => visualStatus(m));
  if (statuses.some((s) => s === "down")) return "down";
  if (statuses.some((s) => s === "slow" || s === "stale")) return "slow";
  return "up";
}

export function realmCounts(members: Monitor[]): { up: number; down: number; slow: number; total: number } {
  let up = 0;
  let down = 0;
  let slow = 0;
  for (const m of members) {
    const s = visualStatus(m);
    if (s === "down") down += 1;
    else if (s === "slow" || s === "stale") slow += 1;
    else if (s === "up") up += 1;
  }
  return { up, down, slow, total: members.length };
}

export function realmAvgLatency(members: Monitor[]): number | null {
  const nums = members
    .map((m) => m.state?.last_latency_ms)
    .filter((v): v is number => v != null);
  if (!nums.length) return null;
  return nums.reduce((a, b) => a + b, 0) / nums.length;
}

export function averageSeries(
  series: Record<number, SeriesPoint[]>,
  ids: number[],
): SeriesPoint[] {
  const buckets = new Map<number, { sum: number; n: number }>();
  for (const id of ids) {
    for (const p of series[id] ?? []) {
      if (p.avg_latency == null) continue;
      const bucket = Math.floor(new Date(p.ts).getTime() / 120_000) * 120;
      const acc = buckets.get(bucket) ?? { sum: 0, n: 0 };
      acc.sum += p.avg_latency;
      acc.n += 1;
      buckets.set(bucket, acc);
    }
  }
  const keys = [...buckets.keys()].sort((a, b) => a - b);
  return keys.map((k) => {
    const acc = buckets.get(k)!;
    const avg = acc.n ? acc.sum / acc.n : null;
    return {
      monitor_id: 0,
      ts: new Date(k * 1000).toISOString(),
      avg_latency: avg,
      max_latency: avg,
      failures: 0,
      total: acc.n,
    };
  });
}

export function distinctTags(monitors: Monitor[]): string[] {
  const set = new Set<string>();
  for (const m of monitors) {
    for (const t of splitTags(m.tags)) set.add(t);
  }
  return [...set].sort((a, b) => a.localeCompare(b));
}
