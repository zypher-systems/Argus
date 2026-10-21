import { cn } from "../lib/utils";
import type { SeriesPoint } from "../lib/types";

const HEIGHT = 40;
const W = 160;
const PAD = 2;

/** A bucket where every check failed: there is no latency to plot. */
function isOutage(p: SeriesPoint): boolean {
  return p.total > 0 && p.failures >= p.total;
}

/**
 * Latency trend. The line breaks where checks were failing and the failure is
 * marked along the baseline, so a down monitor never draws a healthy-looking
 * line out of the latency of its failed attempts.
 */
export function Sparkline({
  points,
  tone = "up",
}: {
  points: SeriesPoint[];
  tone?: "up" | "down" | "muted" | "slow";
}) {
  if (!points || points.length === 0) {
    return (
      <div className="flex h-10 items-center text-[11px] text-muted-foreground">
        collecting data…
      </div>
    );
  }

  const innerH = HEIGHT - PAD * 2;
  const innerW = W - PAD * 2;
  const n = Math.max(points.length, 1);
  const plotted = points.map((p) => (isOutage(p) ? null : p.avg_latency));
  const nums = plotted.filter((v): v is number => v != null);
  const max = Math.max(1, ...nums);
  const xAt = (i: number) => PAD + (i / Math.max(n - 1, 1)) * innerW;

  // Unbroken runs of measured latency become one path each.
  const segments: string[] = [];
  let current: string[] = [];
  plotted.forEach((v, i) => {
    if (v == null) {
      if (current.length > 1) segments.push(current.join(" "));
      current = [];
      return;
    }
    const y = PAD + innerH - (v / max) * innerH;
    current.push(`${current.length === 0 ? "M" : "L"}${xAt(i).toFixed(1)} ${y.toFixed(1)}`);
  });
  if (current.length > 1) segments.push(current.join(" "));
  // A single measurement has no line; show it as a dot.
  const dots = plotted
    .map((v, i) =>
      v != null && plotted[i - 1] == null && plotted[i + 1] == null
        ? { x: xAt(i), y: PAD + innerH - (v / max) * innerH }
        : null,
    )
    .filter((d): d is { x: number; y: number } => d != null);

  const outages: { x: number; w: number }[] = [];
  points.forEach((p, i) => {
    if (!isOutage(p)) return;
    const x = xAt(i);
    const w = Math.max(innerW / n, 1.5);
    const last = outages[outages.length - 1];
    if (last && x <= last.x + last.w + 0.5) last.w = x + w - last.x;
    else outages.push({ x, w });
  });

  const stroke =
    tone === "down"
      ? "var(--chart-down, #b82e27)"
      : tone === "slow"
        ? "var(--warning, #955905)"
        : tone === "muted"
          ? "var(--muted-foreground, #5c626c)"
          : "var(--chart-line, #1f5bd8)";

  return (
    <svg
      viewBox={`0 0 ${W} ${HEIGHT}`}
      className={cn("h-10 w-full")}
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      {segments.map((d, i) => (
        <path key={i} d={d} fill="none" stroke={stroke} strokeWidth="1.6" vectorEffect="non-scaling-stroke" />
      ))}
      {dots.map((d, i) => (
        <circle key={`d${i}`} cx={d.x} cy={d.y} r="1.6" fill={stroke} vectorEffect="non-scaling-stroke" />
      ))}
      {outages.map((o, i) => (
        <rect
          key={`o${i}`}
          x={o.x}
          y={HEIGHT - 3}
          width={o.w}
          height="3"
          fill="var(--chart-down, #b82e27)"
          opacity="0.85"
        />
      ))}
    </svg>
  );
}
