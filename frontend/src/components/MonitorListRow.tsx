import { useEffect, useRef, useState } from "react";
import { BellOff } from "lucide-react";
import type { Monitor } from "../lib/types";
import {
  cn,
  formatLatency,
  isAcked,
  isMuted,
  since,
  typeLabel,
  uptimeLabel,
  visualStatus,
} from "../lib/utils";
import { StatusDot } from "./ui/badge";

/** Every row is this tall, so a long list can be drawn a screenful at a time. */
export const MONITOR_ROW_HEIGHT = 60;

/** One monitor in the Monitors list: two lines, enough to pick it out. */
export function MonitorListRow({
  monitor,
  nested,
  active,
  selected,
  onOpen,
  onToggleSelect,
}: {
  monitor: Monitor;
  nested?: boolean;
  /** Shown in the detail pane beside the list. */
  active?: boolean;
  selected?: boolean;
  onOpen: () => void;
  onToggleSelect?: (shift: boolean) => void;
}) {
  const status = visualStatus(monitor);
  const silenced = isMuted(monitor.state);
  const acked = isAcked(monitor.state);

  const word = status === "muted"
      ? "paused"
      : status === "down"
        ? `down ${monitor.state?.down_since ? since(monitor.state.down_since) : ""}`.trim()
        : status === "up"
          ? uptimeLabel(monitor.state?.uptime_24h, monitor.created_at)
          : status;

  const prevStatus = useRef<string | null>(null);
  const [flash, setFlash] = useState<string | null>(null);

  useEffect(() => {
    if (prevStatus.current == null) {
      prevStatus.current = status;
      return;
    }
    if (prevStatus.current === status) return;
    const from = prevStatus.current;
    prevStatus.current = status;
    if (status === "down") setFlash("flash-down");
    else if (status === "slow") setFlash("flash-slow");
    else if (status === "up" && (from === "down" || from === "slow")) setFlash("flash-up");
    const t = window.setTimeout(() => setFlash(null), 900);
    return () => clearTimeout(t);
  }, [status]);

  return (
    <div
      className={cn(
        "relative border-b border-border-soft",
        status === "down" && "bg-danger/[0.06]",
        (status === "slow" || status === "stale") && "bg-warning/[0.06]",
        active && "bg-primary/[0.12]",
        flash,
      )}
      style={{ height: MONITOR_ROW_HEIGHT }}
    >
      {onToggleSelect && (
        <label className="absolute left-0 top-0 z-10 flex h-full w-9 items-center justify-center">
          <input
            type="checkbox"
            checked={!!selected}
            aria-label={`Select ${monitor.name}`}
            onChange={() => {}}
            onClick={(e) => {
              e.stopPropagation();
              onToggleSelect(e.shiftKey);
            }}
          />
        </label>
      )}
      <button
        type="button"
        onClick={onOpen}
        aria-current={active ? "true" : undefined}
        aria-label={`${monitor.name}, ${word}, ${monitor.target}`}
        className={cn(
          "flex h-full w-full flex-col justify-center gap-1 px-3 text-left transition-colors hover:bg-muted/60 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
          onToggleSelect && "pl-9",
          nested && (onToggleSelect ? "pl-12" : "pl-7"),
        )}
      >
        <span className="flex w-full items-center gap-2">
          <StatusDot status={status} className="h-2 w-2" />
          <span className="truncate text-sm font-semibold leading-tight">{monitor.name}</span>
          {silenced && <BellOff size={11} className="shrink-0 text-warning" aria-hidden="true" />}
          <span
            className={cn(
              "ml-auto shrink-0 font-mono text-xs",
              status === "slow" ? "text-warning" : status === "up" ? "text-foreground" : "text-muted-foreground",
            )}
          >
            {!monitor.enabled || status === "down" ? "—" : formatLatency(monitor.state?.last_latency_ms)}
          </span>
        </span>
        <span className="flex w-full items-center gap-2 pl-4 text-xs leading-tight text-muted-foreground">
          <span className="truncate">
            {typeLabel(monitor.type)} · {monitor.target}
          </span>
          <span
            className={cn(
              "ml-auto shrink-0 font-mono",
              status === "down" && "font-medium text-danger",
              (status === "slow" || status === "stale") && "font-medium text-warning",
            )}
          >
            {word}
            {acked && status === "down" && " · acked"}
          </span>
        </span>
      </button>
    </div>
  );
}
