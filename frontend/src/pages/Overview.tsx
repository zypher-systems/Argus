import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { FleetChart } from "../components/FleetChart";
import { GettingStarted } from "../components/GettingStarted";
import { IncidentTimeline } from "../components/IncidentTimeline";
import { ProbeHealth } from "../components/ProbeHealth";
import { StatTile } from "../components/StatTile";
import { Badge, StatusDot } from "../components/ui/badge";
import { Button } from "../components/ui/button";
import { CountBlocks, MeterBar, MiniLine, StackBar, ThresholdBar } from "../components/ui/meters";
import { api } from "../lib/api";
import { canWrite, useAuth } from "../lib/auth";
import { downKey, incidentTone, useIncident, useOpenIncidents } from "../lib/incidents";
import { useMonitorStore } from "../lib/store";
import { useToast } from "../lib/toast";
import { budgetUsed } from "./SLA";
import type { Incident, Monitor, SLARow, SeriesPoint } from "../lib/types";
import {
  CERT_WARN_DAYS,
  certDaysLeft,
  cn,
  failureLine,
  formatDateTime,
  formatLatency,
  formatUptime,
  isAcked,
  NOT_SERVICE_LATENCY,
  since,
  typeLabel,
  uptimeLabel,
  visualStatus,
  windowLabel,
} from "../lib/utils";

const TOP = 5;

/**
 * Two-minute averages across the fleet. Each monitor sends its last checks,
 * so a 30-second check covers minutes and an hourly one covers days; the
 * window starts where every monitor has data, or the line would jump each
 * time one drops out of the average.
 */
function fleetBuckets(
  series: Record<number, SeriesPoint[]>,
  monitors: Monitor[] | null,
): {
  x: number[];
  y: (number | null)[];
} {
  const included = (monitors ?? []).filter(
    (m) => m.enabled && !NOT_SERVICE_LATENCY.has(m.type) && (series[m.id]?.length ?? 0) > 1,
  );
  const start = Math.max(0, ...included.map((m) => new Date(series[m.id][0].ts).getTime()));
  const buckets = new Map<number, { sum: number; n: number }>();
  for (const m of included) {
    for (const p of series[m.id]) {
      // A failed check's latency is how long it waited, not how fast it was.
      if (p.avg_latency == null || p.failures > 0) continue;
      const t = new Date(p.ts).getTime();
      if (t < start) continue;
      const bucket = Math.floor(t / 120_000) * 120;
      const acc = buckets.get(bucket) ?? { sum: 0, n: 0 };
      acc.sum += p.avg_latency;
      acc.n += 1;
      buckets.set(bucket, acc);
    }
  }
  const keys = [...buckets.keys()].sort((a, b) => a - b);
  return {
    x: keys,
    y: keys.map((k) => {
      const acc = buckets.get(k)!;
      return acc.n ? acc.sum / acc.n : null;
    }),
  };
}

export function Overview() {
  const { monitors, series, refresh } = useMonitorStore();
  const navigate = useNavigate();
  const { user } = useAuth();
  const { toast } = useToast();
  const writable = canWrite(user);
  const [sla, setSla] = useState<SLARow[] | null>(null);
  const [busy, setBusy] = useState<number | null>(null);

  const stats = useMemo(() => {
    const list = (monitors ?? []).filter((m) => m.enabled);
    const dayOld = list.filter(
      (m) => m.state?.uptime_24h != null && Date.now() - new Date(m.created_at).getTime() >= 86_400_000,
    );
    const uptime = dayOld.length
      ? dayOld.reduce((sum, m) => sum + (m.state.uptime_24h ?? 0), 0) / dayOld.length
      : null;
    // A down check's latency is its timeout, not the service's speed.
    const latencies = list
      .filter((m) => !NOT_SERVICE_LATENCY.has(m.type) && visualStatus(m) !== "down")
      .map((m) => m.state?.last_latency_ms)
      .filter((v): v is number => v != null);
    return {
      total: (monitors ?? []).length,
      up: list.filter((m) => visualStatus(m) === "up").length,
      down: list.filter((m) => visualStatus(m) === "down").length,
      slow: list.filter((m) => visualStatus(m) === "slow").length,
      paused: (monitors ?? []).filter((m) => !m.enabled).length,
      uptime,
      latency: latencies.length
        ? latencies.reduce((a, b) => a + b, 0) / latencies.length
        : null,
      certs: list.filter((m) => {
        const d = certDaysLeft(m.state?.tls_expires_at);
        return d != null && d <= CERT_WARN_DAYS;
      }).length,
    };
  }, [monitors]);

  // Longest outage first: it has been hurting the longest and is the one
  // most likely to need a person.
  const downNow = useMemo(
    () =>
      (monitors ?? [])
        .filter((m) => visualStatus(m) === "down")
        .sort(
          (a, b) =>
            new Date(a.state?.down_since ?? Date.now()).getTime() -
            new Date(b.state?.down_since ?? Date.now()).getTime(),
        ),
    [monitors],
  );

  // Worst overrun of its own threshold first.
  const slowNow = useMemo(
    () =>
      (monitors ?? [])
        .filter((m) => visualStatus(m) === "slow")
        .sort((a, b) => overrun(b) - overrun(a)),
    [monitors],
  );

  const lowestUptime = useMemo(() => {
    return (monitors ?? [])
      .filter((m) => m.enabled && m.state?.uptime_24h != null)
      .sort((a, b) => (a.state.uptime_24h ?? 100) - (b.state.uptime_24h ?? 100))
      .slice(0, TOP);
  }, [monitors]);

  const fleet = useMemo(() => fleetBuckets(series, monitors), [series, monitors]);
  const incidents = useOpenIncidents(useMemo(() => downKey(monitors), [monitors]));

  // The latency meter's line: the fleet's last hour, thinned to a few points.
  const latencyTrend = useMemo(() => {
    const n = fleet.y.length;
    if (n < 2) return [];
    const tail = fleet.y.slice(Math.max(0, n - 30));
    return tail;
  }, [fleet]);

  useEffect(() => {
    let cancelled = false;
    api
      .sla("30d")
      .then((r) => !cancelled && setSla(r.rows))
      .catch(() => !cancelled && setSla([]));
    return () => {
      cancelled = true;
    };
    // Re-read when the down count changes so the budget bars keep up.
  }, [stats.down]);

  const slaWorst = useMemo(() => {
    if (!sla) return [];
    return [...sla]
      .filter((r) => r.uptime_pct != null)
      .sort((a, b) => (budgetUsed(b) ?? 0) - (budgetUsed(a) ?? 0))
      .slice(0, TOP);
  }, [sla]);

  const act = async (m: Monitor, what: "ack" | "mute") => {
    setBusy(m.id);
    try {
      if (what === "ack") await api.monitors.ack(m.id);
      else await api.monitors.mute(m.id, 60);
      await refresh();
      toast("success", what === "ack" ? `Acknowledged ${m.name}` : `Muted ${m.name} for an hour`);
    } catch (e) {
      toast("error", e instanceof Error ? e.message : `Could not update ${m.name}`);
    } finally {
      setBusy(null);
    }
  };

  if (monitors === null) {
    return (
      <div className="flex justify-center py-24">
        <span className="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-primary" />
      </div>
    );
  }

  if (monitors.length === 0) {
    return writable ? (
      <div className="animate-fade-in-up mx-auto max-w-2xl py-8">
        <GettingStarted welcome />
      </div>
    ) : (
      <div className="flex flex-col items-center justify-center py-20 text-center">
        <h1 className="font-brand text-xl tracking-tight">Nothing is watched yet</h1>
        <p className="mt-2 max-w-md text-sm text-muted-foreground">
          An admin or operator can add monitors. They will show up here.
        </p>
      </div>
    );
  }

  const enabled = stats.total - stats.paused;
  const acked = downNow.filter((m) => isAcked(m.state)).length;
  const downMs = (m: Monitor) => Date.now() - new Date(m.state?.down_since ?? Date.now()).getTime();
  const longestMs = downNow.length ? Math.max(1, downMs(downNow[0])) : 1;

  return (
    <div className="animate-fade-in-up">
      <div className="mb-5 flex flex-wrap items-baseline justify-between gap-2">
        <h1 className="font-brand text-xl tracking-tight">Overview</h1>
        <span className="text-xs text-muted-foreground">
          {stats.total} monitor{stats.total === 1 ? "" : "s"}
          {stats.paused > 0 && ` · ${stats.paused} paused`}
        </span>
      </div>

      {writable && <GettingStarted />}

      {/* The four tiles. Each is a number and a picture of the same fact. */}
      <div className="mb-4 grid grid-cols-2 gap-4 xl:grid-cols-4">
        <StatTile
          label="Availability, 24 h"
          value={stats.uptime == null ? "—" : formatUptime(stats.uptime)}
          hint={
            stats.uptime == null
              ? "needs a full day of checks"
              : [`${stats.up} up`, stats.down > 0 && `${stats.down} down`, stats.slow > 0 && `${stats.slow} slow`]
                  .filter(Boolean)
                  .join(" · ")
          }
        >
          <StackBar
            parts={[
              { value: stats.up, tone: "success" },
              { value: stats.down, tone: "danger" },
              { value: stats.slow, tone: "warning" },
            ]}
            label={`${stats.up} of ${enabled} monitors up`}
          />
        </StatTile>
        <StatTile
          label="Down"
          value={String(stats.down)}
          unit={`/ ${enabled}`}
          tone={stats.down > 0 ? "danger" : undefined}
          hint={
            stats.down === 0
              ? "nothing down"
              : `longest ${since(downNow[0]?.state?.down_since) || "just now"}${acked > 0 ? ` · ${acked} acknowledged` : ""}`
          }
          to={stats.down > 0 ? "/monitors?status=down" : undefined}
        >
          <CountBlocks count={stats.down} tone="danger" label={`${stats.down} down`} />
        </StatTile>
        <StatTile
          label="Slow"
          value={String(stats.slow)}
          unit={`/ ${enabled}`}
          tone={stats.slow > 0 ? "warning" : undefined}
          hint={stats.slow === 0 ? "all inside threshold" : "over their own threshold"}
          to={stats.slow > 0 ? "/monitors?status=slow" : undefined}
        >
          <CountBlocks count={stats.slow} tone="warning" label={`${stats.slow} slow`} />
        </StatTile>
        <StatTile
          label="Average latency"
          value={formatLatency(stats.latency)}
          hint={
            stats.certs > 0
              ? `${stats.certs} certificate${stats.certs === 1 ? "" : "s"} expiring`
              : "latest successful checks"
          }
          hintTone={stats.certs > 0 ? "warning" : undefined}
        >
          <MiniLine values={latencyTrend} className="h-1.5" />
        </StatTile>
      </div>

      <div className="grid items-start gap-4 xl:grid-cols-[minmax(0,1fr)_17rem]">
        <div className="grid min-w-0 gap-4 lg:grid-cols-2">
          <Panel
            title="Down"
            count={downNow.length}
            countTone={downNow.length > 0 ? "danger" : undefined}
            aside={downNow.length > 1 ? <span className="text-xs text-muted-foreground">longest first</span> : undefined}
            empty="Nothing is down."
            className="lg:col-span-2"
          >
            {downNow.length > 0 && (
              <ul className="divide-y divide-border-soft">
                {downNow.map((m) => {
                  const isAck = isAcked(m.state);
                  const why = failureLine(m);
                  const dur = m.state?.down_since ? since(m.state.down_since) : "just now";
                  return (
                    <li
                      key={m.id}
                      className={cn(
                        "flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3",
                        !isAck && "bg-danger/[0.06]",
                      )}
                    >
                      <Link
                        to={`/monitors/${m.id}`}
                        className="min-w-0 flex-1 basis-56 rounded focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      >
                        <span className="flex items-center gap-2">
                          <StatusDot status="down" className="h-2 w-2" />
                          <span className="truncate text-sm font-semibold leading-snug">{m.name}</span>
                          <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
                            {typeLabel(m.type)}
                          </span>
                        </span>
                        <span
                          className={cn(
                            "mt-0.5 block truncate pl-4 text-xs leading-snug",
                            why ? "text-danger" : "text-muted-foreground",
                          )}
                        >
                          {why || m.target}
                        </span>
                      </Link>
                      <span className="w-32 shrink-0">
                        <span className="flex items-baseline justify-between text-xs">
                          <span className="text-muted-foreground">Down for</span>
                          <span className="font-mono text-sm">{dur}</span>
                        </span>
                        <MeterBar
                          fraction={downMs(m) / longestMs}
                          tone="danger"
                          label={`Down for ${dur}`}
                          className="mt-1.5 h-1"
                        />
                      </span>
                      {isAck ? (
                        <Badge tone="muted" className="w-[13.75rem] justify-center">
                          acknowledged
                        </Badge>
                      ) : writable ? (
                        <span className="flex shrink-0 gap-2">
                          <Button
                            size="sm"
                            disabled={busy === m.id}
                            onClick={() => act(m, "ack")}
                            aria-label={`Acknowledge ${m.name}`}
                          >
                            Acknowledge
                          </Button>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={busy === m.id}
                            onClick={() => act(m, "mute")}
                            aria-label={`Mute ${m.name} for an hour`}
                          >
                            Mute 1 h
                          </Button>
                        </span>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            )}
          </Panel>

          <section className="surface p-5 lg:col-span-2">
            <div className="mb-3 flex items-baseline justify-between">
              <h2 className="text-sm font-semibold">Fleet latency</h2>
              <span className="text-xs text-muted-foreground">
                {fleet.x.length > 1
                  ? `${windowLabel(fleet.x[0], fleet.x[fleet.x.length - 1])} · 2-minute average`
                  : "collecting data"}
              </span>
            </div>
            <FleetChart x={fleet.x} y={fleet.y} />
          </section>

          <Panel
            title="Slow"
            count={slowNow.length}
            countTone={slowNow.length > 0 ? "warning" : undefined}
            aside={<ThresholdKey />}
            empty="Every monitor is inside its latency threshold."
          >
            {slowNow.length > 0 && (
              <ul className="divide-y divide-border-soft">
                {slowNow.slice(0, TOP * 2).map((m) => {
                  const over = overrun(m);
                  return (
                    <li key={m.id}>
                      <button
                        type="button"
                        onClick={() => navigate(`/monitors/${m.id}`)}
                        className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-2 px-4 py-3 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                      >
                        <span className="truncate text-sm font-medium leading-snug">{m.name}</span>
                        <span className="font-mono text-xs text-muted-foreground">
                          <span className="text-sm text-warning">{formatLatency(m.state?.last_latency_ms)}</span> /{" "}
                          {formatLatency(m.latency_threshold_ms)}
                        </span>
                        <span className="col-span-2">
                          <ThresholdBar
                            ratio={over}
                            label={`${Math.round(over * 100)}% of its ${formatLatency(m.latency_threshold_ms)} threshold`}
                          />
                        </span>
                      </button>
                    </li>
                  );
                })}
              </ul>
            )}
          </Panel>

          <Panel
              title="SLA, 30 days"
              aside={
                <Link to="/sla" className="text-xs font-medium text-primary hover:underline">
                  All reports
                </Link>
              }
              empty={sla === null ? "Loading…" : "No SLA data yet."}
            >
              {slaWorst.length > 0 && (
                <ul className="divide-y divide-border-soft">
                  {slaWorst.map((r) => {
                    const used = budgetUsed(r) ?? 0;
                    const left = Math.max(0, Math.round((1 - used) * 100));
                    const state = !r.within_slo ? "missed" : used >= 0.7 ? "risk" : "ok";
                    return (
                      <li key={r.monitor_id}>
                        <button
                          type="button"
                          onClick={() => navigate(`/monitors/${r.monitor_id}`)}
                          className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-2 px-4 py-3 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                        >
                          <span className="flex min-w-0 items-baseline gap-2">
                            <span className="truncate text-sm font-medium leading-snug">{r.name}</span>
                            <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{r.slo_target}%</span>
                          </span>
                          <span
                            className={cn(
                              "font-mono text-xs",
                              state === "missed"
                                ? "font-medium text-danger"
                                : state === "risk"
                                  ? "font-medium text-warning"
                                  : "text-muted-foreground",
                            )}
                          >
                            {state === "missed" ? "missed" : `${left}% left`}
                          </span>
                          <span className="col-span-2">
                            <MeterBar
                              fraction={1 - used}
                              tone={state === "missed" ? "danger" : state === "risk" ? "warning" : "primary"}
                              label={`${left}% of the error budget left against a ${r.slo_target}% target, ${formatUptime(r.uptime_pct)} uptime`}
                            />
                          </span>
                        </button>
                      </li>
                    );
                  })}
                </ul>
              )}
            </Panel>
            <Panel title="Lowest uptime, 24 h" empty="No uptime yet.">
              {lowestUptime.length > 0 && (
                <ul className="divide-y divide-border-soft">
                  {lowestUptime.map((m) => (
                    <li key={m.id}>
                      <button
                        type="button"
                        onClick={() => navigate(`/monitors/${m.id}`)}
                        className="grid w-full grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-4 py-3 text-left transition-colors hover:bg-muted focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
                      >
                        <StatusDot status={visualStatus(m)} className="h-2 w-2" />
                        <span className="truncate text-sm font-medium leading-snug">{m.name}</span>
                        <span className="text-right font-mono text-xs">{uptimeLabel(m.state.uptime_24h, m.created_at)}</span>
                        <span className="col-span-3">
                          <MeterBar
                            fraction={(m.state.uptime_24h ?? 0) / 100}
                            tone={(m.state.uptime_24h ?? 100) < 99 ? "warning" : "primary"}
                            label={`${formatUptime(m.state.uptime_24h)} uptime`}
                          />
                        </span>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </Panel>
        </div>

        <div className="grid min-w-0 gap-4 sm:grid-cols-2 xl:grid-cols-1">
          <ActiveIncidents incidents={incidents} />
          <ProbeHealth />
        </div>
      </div>

      {writable && <GettingStarted variant="restore" />}
    </div>
  );
}

/** The open incidents: the longest-running one with its updates, the rest as lines. */
function ActiveIncidents({ incidents }: { incidents: Incident[] | null }) {
  const first = incidents?.[0];
  const detail = useIncident(first?.id ?? null, first?.status ?? "");
  return (
    <section className="surface p-4" aria-label="Active incidents">
      <div className="flex items-baseline justify-between gap-2">
        <h2 className="text-sm font-semibold">Active incidents</h2>
        {incidents && incidents.length > 0 && (
          <span className="font-mono text-xs text-danger">{incidents.length}</span>
        )}
      </div>
      {incidents === null ? (
        <p className="mt-3 text-sm text-muted-foreground">Loading…</p>
      ) : !first ? (
        <p className="mt-3 text-sm text-muted-foreground">No open incidents.</p>
      ) : (
        <>
          <Badge tone={incidentTone(first)} className="mt-3 capitalize">
            {first.status}
          </Badge>
          <div className="mt-3 flex items-baseline justify-between gap-2">
            <p className="min-w-0 break-words text-sm font-semibold leading-snug">{first.title}</p>
            <span className="shrink-0 font-mono text-xs text-muted-foreground">#{first.id}</span>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {first.monitor_name ? `${first.monitor_name} · ` : ""}
            {first.auto_opened ? "opened automatically" : "opened"} {formatDateTime(first.opened_at)}
          </p>
          {detail && <IncidentTimeline updates={detail.updates ?? []} limit={3} className="mt-4" />}
          {incidents.length > 1 && (
            <ul className="mt-4 divide-y divide-border-soft border-t border-border-soft">
              {incidents.slice(1, 6).map((inc) => (
                <li key={inc.id} className="flex items-baseline justify-between gap-3 py-2.5">
                  <span className="min-w-0 truncate text-sm font-medium">{inc.title}</span>
                  <span className="shrink-0 font-mono text-xs text-muted-foreground">
                    #{inc.id} · {since(inc.opened_at)}
                  </span>
                </li>
              ))}
            </ul>
          )}
          <Link
            to="/incidents"
            className="mt-3 inline-block rounded text-xs font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            {incidents.length > 6 ? `All ${incidents.length} incidents` : "Open Incidents"}
          </Link>
        </>
      )}
    </section>
  );
}

/** What the tick on a Slow bar means. */
function ThresholdKey() {
  return (
    <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
      <span className="h-3 w-0.5 bg-foreground" aria-hidden="true" />
      threshold
    </span>
  );
}

/** How far over its own threshold a monitor is: 1.5 means half again as slow. */
function overrun(m: Monitor): number {
  const l = m.state?.last_latency_ms;
  const t = m.latency_threshold_ms;
  if (l == null || !t) return 1;
  return l / t;
}

function Panel({
  title,
  count,
  countTone,
  aside,
  empty,
  className,
  children,
}: {
  title: string;
  count?: number;
  countTone?: "danger" | "warning";
  aside?: React.ReactNode;
  empty: string;
  className?: string;
  children?: React.ReactNode;
}) {
  return (
    <section className={cn("surface overflow-hidden", className)}>
      <div className="flex items-center gap-2 border-b border-border px-4 py-3">
        <h2 className="text-sm font-semibold">{title}</h2>
        {count != null && (
          <span
            className={cn(
              "font-mono text-xs",
              countTone === "danger" ? "text-danger" : countTone === "warning" ? "text-warning" : "text-muted-foreground",
            )}
          >
            {count}
          </span>
        )}
        {aside && <span className="ml-auto">{aside}</span>}
      </div>
      {children || <p className="px-4 py-6 text-sm text-muted-foreground">{empty}</p>}
    </section>
  );
}
