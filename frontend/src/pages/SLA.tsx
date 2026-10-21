import { useEffect, useMemo, useState } from "react";
import { Download } from "lucide-react";
import { useNavigate } from "react-router-dom";
import { api, ApiError } from "../lib/api";
import { Badge } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { MeterBar } from "../components/ui/meters";
import { Segmented } from "../components/ui/segmented";
import type { SLARow } from "../lib/types";
import { cn, formatUptime } from "../lib/utils";

const RANGES = [
  { value: "7d", label: "7 d" },
  { value: "30d", label: "30 d" },
  { value: "90d", label: "90 d" },
  { value: "365d", label: "1 y" },
];

/** Share of the error budget spent: 0 is untouched, 1 is gone. */
export function budgetUsed(row: SLARow): number | null {
  if (row.uptime_pct == null) return null;
  const allowed = 100 - row.slo_target;
  if (allowed <= 0) return row.uptime_pct >= 100 ? 0 : 1;
  return Math.max(0, (100 - row.uptime_pct) / allowed);
}

export function slaState(row: SLARow): "missed" | "risk" | "ok" | "none" {
  const used = budgetUsed(row);
  if (used == null) return "none";
  if (!row.within_slo) return "missed";
  return used >= 0.7 ? "risk" : "ok";
}

export function SLA() {
  const navigate = useNavigate();
  const [range, setRange] = useState("30d");
  const [rows, setRows] = useState<SLARow[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    setError(null);
    api
      .sla(range)
      .then((r) => setRows(r.rows))
      .catch((err) => {
        setError(err instanceof ApiError ? err.message : "failed to load SLA");
        setRows([]);
      });
  }, [range]);

  const sorted = useMemo(
    () => [...(rows ?? [])].sort((a, b) => (budgetUsed(b) ?? -1) - (budgetUsed(a) ?? -1)),
    [rows],
  );
  const summary = useMemo(() => {
    const measured = (rows ?? []).filter((r) => r.uptime_pct != null);
    const missed = measured.filter((r) => !r.within_slo).length;
    const risk = measured.filter((r) => slaState(r) === "risk").length;
    const avg = measured.length
      ? measured.reduce((s, r) => s + (r.uptime_pct ?? 0), 0) / measured.length
      : null;
    return { measured: measured.length, missed, risk, avg };
  }, [rows]);

  return (
    <div className="animate-fade-in-up">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="font-brand text-xl tracking-tight">SLA reports</h1>
          <p className="mt-1 text-sm text-muted-foreground">
            Uptime against each monitor&apos;s SLO target, from daily rollups. Worst first.
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Segmented value={range} onChange={setRange} options={RANGES} label="Report period" />
          <Button
            variant="outline"
            onClick={() => {
              window.location.href = api.slaCsvUrl(range);
            }}
          >
            <Download size={14} />
            CSV
          </Button>
        </div>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-warning/40 bg-warning/10 px-4 py-3 text-sm">{error}</div>
      )}

      {rows && rows.length > 0 && (
        <div className="surface mb-6 grid grid-cols-2 divide-border-soft lg:grid-cols-4 lg:divide-x">
          <Stat label="Monitors with a target" value={String(summary.measured)} />
          <Stat label="Average uptime" value={formatUptime(summary.avg)} />
          <Stat label="Missed" value={String(summary.missed)} tone={summary.missed > 0 ? "danger" : undefined} />
          <Stat label="At risk" value={String(summary.risk)} tone={summary.risk > 0 ? "warning" : undefined} hint="70% or more of the error budget used" />
        </div>
      )}

      <div className="surface overflow-hidden">
        <div className="hidden grid-cols-[minmax(0,1.6fr)_5rem_6rem_minmax(8rem,1fr)_8rem_7rem] gap-x-4 border-b border-border bg-muted/40 px-4 py-2.5 text-[11px] font-medium uppercase tracking-wider text-muted-foreground md:grid">
          <span>Monitor</span>
          <span className="text-right">Target</span>
          <span className="text-right">Uptime</span>
          <span>Error budget</span>
          <span className="text-right">Failed</span>
          <span />
        </div>
        {rows === null ? (
          <div className="px-4 py-8 text-sm text-muted-foreground">Loading…</div>
        ) : rows.length === 0 ? (
          !error && <p className="px-4 py-8 text-sm text-muted-foreground">No monitors yet.</p>
        ) : (
          <ul className="divide-y divide-border-soft">
            {sorted.map((row) => {
              const used = budgetUsed(row);
              const state = slaState(row);
              return (
                <li key={row.monitor_id}>
                  <button
                    type="button"
                    onClick={() => navigate(`/monitors/${row.monitor_id}`)}
                    className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-2 px-4 py-3 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring md:grid-cols-[minmax(0,1.6fr)_5rem_6rem_minmax(8rem,1fr)_8rem_7rem]"
                  >
                    <span className="truncate text-sm font-medium">{row.name}</span>
                    <span className="hidden text-right font-mono text-sm text-muted-foreground md:block">
                      {row.slo_target.toFixed(row.slo_target % 1 === 0 ? 0 : 2)}%
                    </span>
                    <span
                      className={cn(
                        "text-right font-mono text-sm md:order-none",
                        state === "missed" && "text-danger",
                        state === "risk" && "text-warning",
                      )}
                    >
                      {formatUptime(row.uptime_pct)}
                    </span>
                    <span className="col-span-2 md:col-span-1">
                      {used == null ? (
                        <span className="text-xs text-muted-foreground">no checks in period</span>
                      ) : (
                        <MeterBar
                          fraction={used}
                          tone={state === "missed" ? "danger" : state === "risk" ? "warning" : "success"}
                          label={`${Math.min(100, Math.round(used * 100))}% of the error budget used against a ${row.slo_target}% target`}
                          className="h-1"
                        />
                      )}
                    </span>
                    <span className="hidden whitespace-nowrap text-right font-mono text-sm text-muted-foreground md:block">
                      {row.failed_checks}
                      <span> / {row.total_checks}</span>
                    </span>
                    <span className="hidden justify-end md:flex">
                      {state !== "none" && (
                        <Badge tone={state === "missed" ? "down" : state === "risk" ? "warning" : "up"}>
                          {state === "missed" ? "Missed" : state === "risk" ? "At risk" : "On track"}
                        </Badge>
                      )}
                    </span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}

function Stat({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: string;
  hint?: string;
  tone?: "danger" | "warning";
}) {
  return (
    <div className="p-4 md:p-5">
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div className={cn("font-display mt-1 text-[34px] leading-none", tone === "danger" && "text-danger", tone === "warning" && "text-warning")}>
        {value}
      </div>
      {hint && <div className="mt-2 text-xs text-muted-foreground">{hint}</div>}
    </div>
  );
}
