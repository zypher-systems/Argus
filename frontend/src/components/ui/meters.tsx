import { cn } from "../../lib/utils";

type Tone = "primary" | "success" | "warning" | "danger";

const fills: Record<Tone, string> = {
  primary: "bg-primary",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-danger",
};

/** A thin fill bar: how much of something, as a share of the whole. */
export function MeterBar({
  fraction,
  tone = "primary",
  label,
  className,
}: {
  /** 0 to 1; anything above 1 shows as full. */
  fraction: number;
  tone?: Tone;
  label: string;
  className?: string;
}) {
  const pct = Math.max(0, Math.min(1, fraction)) * 100;
  return (
    <div
      role="img"
      aria-label={label}
      className={cn("h-1.5 w-full overflow-hidden rounded-full bg-muted", className)}
    >
      <div className={cn("h-full rounded-full", fills[tone])} style={{ width: `${pct}%` }} />
    </div>
  );
}

/** Shares of one whole. `min-w` keeps a share too small to draw visible. */
export function StackBar({
  parts,
  label,
  className,
}: {
  parts: { value: number; tone: Tone | "muted" }[];
  label: string;
  className?: string;
}) {
  const shown = parts.filter((p) => p.value > 0);
  return (
    <div role="img" aria-label={label} className={cn("flex h-1.5 w-full gap-0.5", className)}>
      {shown.length === 0 && <span className="h-full flex-1 rounded-full bg-muted" />}
      {shown.map((p, i) => (
        <span
          key={i}
          className={cn("h-full min-w-1.5 rounded-full", p.tone === "muted" ? "bg-muted-foreground/40" : fills[p.tone])}
          style={{ flexGrow: p.value }}
        />
      ))}
    </div>
  );
}

/**
 * A reading against its own limit. The tick is the limit and sits mid-track,
 * so the fill past it is how far over; twice the limit fills the track.
 */
export function ThresholdBar({
  ratio,
  label,
  className,
}: {
  /** Reading divided by limit: 1.5 is half again over. */
  ratio: number;
  label: string;
  className?: string;
}) {
  const pct = Math.max(0, Math.min(1, ratio / 2)) * 100;
  return (
    <div role="img" aria-label={label} className={cn("relative h-1.5 w-full rounded-full bg-muted", className)}>
      <div className="h-full rounded-full bg-warning" style={{ width: `${pct}%` }} />
      <span className="absolute left-1/2 top-1/2 h-3 w-0.5 -translate-y-1/2 bg-foreground" />
    </div>
  );
}

/**
 * One block per thing, so a count of five reads as five. Past the cap the
 * blocks stop and the rest is said in words, or a wall of red would follow.
 */
export function CountBlocks({
  count,
  tone,
  label,
  cap = 12,
}: {
  count: number;
  tone: Tone;
  label: string;
  cap?: number;
}) {
  const shown = Math.min(count, cap);
  return (
    <div role="img" aria-label={label} className="flex h-1.5 items-center gap-[3px]">
      {Array.from({ length: shown }).map((_, i) => (
        <span key={i} className={cn("h-full w-4 rounded-sm", fills[tone])} />
      ))}
      {count > cap && <span className="ml-1 text-[11px] leading-none text-muted-foreground">+{count - cap}</span>}
      {count === 0 && <span className="h-full w-4 rounded-sm bg-muted" />}
    </div>
  );
}

/** A small line of values, sized by its box. */
export function MiniLine({ values, className }: { values: (number | null)[]; className?: string }) {
  const nums = values.filter((v): v is number => v != null);
  if (nums.length < 2) return <div className={cn("h-3.5", className)} />;
  const max = Math.max(1, ...nums);
  const min = Math.min(...nums);
  const span = Math.max(max - min, 1);
  const n = values.length;
  const pts = values
    .map((v, i) => (v == null ? null : `${(i / (n - 1)) * 200},${14 - 2 - ((v - min) / span) * 10}`))
    .filter(Boolean)
    .join(" ");
  return (
    <svg
      viewBox="0 0 200 14"
      preserveAspectRatio="none"
      aria-hidden="true"
      className={cn("h-3.5 w-full", className)}
    >
      <polyline
        points={pts}
        fill="none"
        stroke="var(--chart-line)"
        strokeWidth="1.5"
        vectorEffect="non-scaling-stroke"
      />
    </svg>
  );
}
