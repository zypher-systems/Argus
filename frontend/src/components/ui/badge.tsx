import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

type Tone = "up" | "down" | "pending" | "muted" | "primary" | "warning";

const tones: Record<Tone, string> = {
  up: "bg-success/10 text-success border-success/25",
  down: "bg-danger/10 text-danger border-danger/25",
  pending: "bg-warning/10 text-warning border-warning/25",
  muted: "bg-muted text-muted-foreground border-border",
  primary: "bg-primary/10 text-primary border-primary/25",
  warning: "bg-warning/10 text-warning border-warning/25",
};

export function StatusDot({
  status,
  className,
}: {
  status: string;
  className?: string;
}) {
  const color =
    status === "up"
      ? "bg-success"
      : status === "down"
        ? "bg-danger animate-pulse-dot"
        : status === "slow"
          ? "bg-warning animate-pulse-dot"
          : status === "muted"
            ? "bg-muted-foreground/50"
            : status === "stale"
              ? "bg-warning"
              : "bg-warning animate-pulse-dot";
  return (
    <span
      aria-hidden="true"
      className={cn(
        "inline-block h-2.5 w-2.5 shrink-0 rounded-full",
        color,
        className,
      )}
    />
  );
}

const TYPE_LABELS: Record<string, string> = {
  icmp: "ICMP",
  http: "HTTP",
  tcp: "TCP",
  dns: "DNS",
  cert: "CERT",
  push: "PUSH",
  group: "GROUP",
};

export function TypeBadge({ type }: { type: string }) {
  return <Badge tone="primary">{TYPE_LABELS[type] ?? type}</Badge>;
}

export function Badge({
  tone = "muted",
  className,
  ...props
}: HTMLAttributes<HTMLSpanElement> & { tone?: Tone }) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-xs font-medium",
        tones[tone],
        className,
      )}
      {...props}
    />
  );
}
