import type { ResultsResponse } from "../lib/types";

const SEGMENTS = 60;

interface Segment {
  status: "up" | "down" | "no-data";
}

function bucketize(data: ResultsResponse): Segment[] {
  const segs: Segment[] = Array.from({ length: SEGMENTS }, () => ({
    status: "no-data",
  }));
  const points = data.points;
  if (points.length === 0) return segs;

  let minTs = Infinity;
  let maxTs = -Infinity;
  const tsList: { ts: number; ok: number; total: number }[] = [];
  if (data.granularity === "raw") {
    for (const p of points as { ts: string; success: boolean }[]) {
      const ts = new Date(p.ts).getTime();
      minTs = Math.min(minTs, ts);
      maxTs = Math.max(maxTs, ts);
      tsList.push({ ts, ok: p.success ? 1 : 0, total: 1 });
    }
  } else {
    for (const p of points as { ts: string; failures: number; total: number }[]) {
      const ts = new Date(p.ts).getTime();
      minTs = Math.min(minTs, ts);
      maxTs = Math.max(maxTs, ts);
      tsList.push({ ts, ok: p.total - p.failures, total: p.total });
    }
  }
  const span = Math.max(maxTs - minTs, 1);
  const totals = new Map<number, { ok: number; total: number }>();
  for (const p of tsList) {
    const idx = Math.min(SEGMENTS - 1, Math.floor(((p.ts - minTs) / span) * SEGMENTS));
    const acc = totals.get(idx) ?? { ok: 0, total: 0 };
    acc.ok += p.ok;
    acc.total += p.total;
    totals.set(idx, acc);
  }
  for (const [idx, acc] of totals) {
    segs[idx] = { status: acc.ok === acc.total ? "up" : "down" };
  }
  return segs;
}

export function UptimeBar({ data }: { data: ResultsResponse | null }) {
  if (!data?.points?.length) {
    return (
      <div className="flex h-7 items-center text-sm text-muted-foreground">
        no data yet
      </div>
    );
  }
  const segs = bucketize(data);
  const upCount = segs.filter((s) => s.status === "up").length;
  const known = segs.filter((s) => s.status !== "no-data").length;
  const pct = known > 0 ? (upCount / known) * 100 : 0;

  return (
    <div>
      <div className="flex h-7 w-full items-stretch gap-px overflow-hidden">
        {segs.map((seg, i) => (
          <div
            key={i}
            className="flex-1"
            style={{
              background:
                seg.status === "up"
                  ? "var(--chart-up)"
                  : seg.status === "down"
                    ? "var(--chart-down)"
                    : "var(--muted)",
              opacity: seg.status === "no-data" ? 0.35 : 0.9,
            }}
            title={
              seg.status === "up"
                ? "operational"
                : seg.status === "down"
                  ? "failing"
                  : "no data"
            }
          />
        ))}
      </div>
      <div className="mt-2 flex items-center gap-4 text-xs text-muted-foreground">
        <span className="flex items-center gap-1.5">
          <span
            className="inline-block h-1.5 w-3"
            style={{ background: "var(--chart-up)" }}
          />
          operational
        </span>
        <span className="flex items-center gap-1.5">
          <span
            className="inline-block h-1.5 w-3"
            style={{ background: "var(--chart-down)" }}
          />
          down
        </span>
        <span className="ml-auto font-medium tabular-nums">
          {pct.toFixed(2)}% uptime in view
        </span>
      </div>
    </div>
  );
}
