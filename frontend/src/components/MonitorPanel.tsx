import { useEffect, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { ArrowUpRight, Zap } from "lucide-react";
import { LatencyChart } from "./LatencyChart";
import { UptimeBar } from "./UptimeBar";
import { Badge, StatusDot } from "./ui/badge";
import { Button } from "./ui/button";
import { Segmented } from "./ui/segmented";
import { api, ApiError } from "../lib/api";
import { useMonitorStore } from "../lib/store";
import { useToast } from "../lib/toast";
import type { Monitor, NotificationChannel, Realm, ResultsResponse } from "../lib/types";
import {
  failureLine,
  formatInterval,
  formatLatency,
  isAcked,
  isMuted,
  since,
  splitTags,
  typeLabel,
  uptimeLabel,
  visualStatus,
} from "../lib/utils";

const RANGES = [
  { value: "1h", label: "1H" },
  { value: "24h", label: "24H" },
  { value: "7d", label: "7D" },
];

const STATUS_WORD: Record<string, string> = {
  up: "Operational",
  down: "Down",
  slow: "Slow",
  stale: "Stale",
  pending: "Pending",
  muted: "Paused",
};

/**
 * The monitor picked in the Monitors list: what is wrong, how it has been
 * doing, and how it is set up. The full page is one link away.
 */
export function MonitorPanel({
  monitor,
  realms,
  channels,
  writable,
}: {
  monitor: Monitor;
  realms: Realm[];
  channels: NotificationChannel[];
  writable: boolean;
}) {
  const navigate = useNavigate();
  const { refresh } = useMonitorStore();
  const { toast } = useToast();
  const [range, setRange] = useState("24h");
  const [results, setResults] = useState<{ id: number; range: string; data: ResultsResponse } | null>(null);
  const [reload, setReload] = useState(0);
  const [busy, setBusy] = useState(false);
  const id = monitor.id;

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api.monitors
        .results(id, range)
        .then((data) => !cancelled && setResults({ id, range, data }))
        .catch(() => {
          /* keep the last good chart for this range; the next poll tries again */
        });
    load();
    const interval = window.setInterval(load, range.endsWith("h") ? 30_000 : 300_000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [id, range, reload]);

  // Another monitor's chart, or another range's, must not show under this
  // one's label while its own loads or after its request failed.
  const data = results?.id === id && results.range === range ? results.data : null;

  const checkNow = async () => {
    setBusy(true);
    try {
      const res = await api.monitors.checkNow(id);
      await refresh();
      setReload((n) => n + 1);
      const note = res.applied ? "" : monitor.enabled ? " (from this server; not counted)" : " (paused; not counted)";
      if (res.success) {
        toast("success", `up${res.latency_ms != null ? ` in ${formatLatency(res.latency_ms)}` : ""}${note}`);
      } else {
        toast("error", `${res.error || "check failed"}${note}`);
      }
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "check failed");
    } finally {
      setBusy(false);
    }
  };

  const vis = visualStatus(monitor);
  const state = monitor.state;
  const tone = vis === "up" ? "up" : vis === "down" ? "down" : vis === "muted" ? "muted" : "warning";
  const why = vis === "down" ? failureLine(monitor) : "";
  const envs = (monitor.realm_ids ?? [])
    .map((rid) => realms.find((r) => r.id === rid)?.name)
    .filter((n): n is string => !!n);
  const alertNames =
    monitor.channel_ids == null
      ? "Default channels"
      : monitor.channel_ids.length === 0
        ? "None"
        : monitor.channel_ids.map((cid) => channels.find((c) => c.id === cid)?.name ?? `#${cid}`).join(", ");
  const locations = monitor.probe_node_ids?.length ?? 0;
  const tags = splitTags(monitor.tags);

  return (
    <section className="surface min-w-0" aria-label="Selected monitor">
      <div className="p-5">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={tone}>
            <StatusDot status={vis} className="h-2 w-2" />
            {STATUS_WORD[vis]}
          </Badge>
          {isMuted(state) && <Badge tone="warning">muted</Badge>}
          {isAcked(state) && <Badge tone="warning">acked</Badge>}
          <span className="ml-auto font-mono text-xs text-muted-foreground">{typeLabel(monitor.type)}</span>
        </div>
        <h2 className="mt-3 break-words text-xl font-semibold leading-tight tracking-tight">{monitor.name}</h2>
        <p className="mt-1.5 break-all font-mono text-xs text-muted-foreground">{monitor.target}</p>
        <div className="mt-4 flex flex-wrap items-center gap-2">
          {writable && monitor.type !== "group" && (
            <Button size="sm" onClick={checkNow} disabled={busy}>
              <Zap size={14} />
              Check now
            </Button>
          )}
          {writable && (
            <Button size="sm" variant="outline" onClick={() => navigate(`/monitors/${id}/edit`)}>
              Edit
            </Button>
          )}
          <Link
            to={`/monitors/${id}`}
            className="inline-flex h-9 items-center gap-1 rounded-lg px-2 text-[13px] font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            Full detail
            <ArrowUpRight size={14} aria-hidden="true" />
          </Link>
        </div>
      </div>

      {vis === "down" && (
        <div role="status" className="mx-5 rounded-lg border border-danger/25 bg-danger/10 px-4 py-3">
          <p className="break-words text-sm font-semibold text-danger">{why || "The last check failed."}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {state?.down_since ? `Down for ${since(state.down_since)}` : "Down"}
            {(state?.consecutive_failures ?? 0) > 0 &&
              ` · ${state.consecutive_failures} failed check${state.consecutive_failures === 1 ? "" : "s"} in a row`}
          </p>
        </div>
      )}
      {vis === "slow" && (
        <div role="status" className="mx-5 rounded-lg border border-warning/25 bg-warning/10 px-4 py-3">
          <p className="text-sm font-semibold text-warning">
            {formatLatency(state?.last_latency_ms)} against a {formatLatency(monitor.latency_threshold_ms)} threshold
          </p>
          <p className="mt-1 text-xs text-muted-foreground">
            {state?.slow_since ? `Slow for ${since(state.slow_since)}` : "Slow"} · checks are passing
          </p>
        </div>
      )}
      {vis === "stale" && (
        <div role="status" className="mx-5 rounded-lg border border-warning/25 bg-warning/10 px-4 py-3">
          <p className="text-sm font-semibold text-warning">The probe has stopped checking this monitor.</p>
          <p className="mt-1 text-xs text-muted-foreground">Its status is frozen at the last result.</p>
        </div>
      )}
      <dl className="grid grid-cols-3 gap-4 p-5">
        <Stat label="Uptime, 24 h" value={uptimeLabel(state?.uptime_24h, monitor.created_at)} />
        <Stat label="Last latency" value={vis === "down" ? "—" : formatLatency(state?.last_latency_ms)} />
        <Stat label="Check interval" value={formatInterval(monitor.interval_seconds)} />
      </dl>

      <div className="border-t border-border-soft p-5">
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          <h3 className="text-sm font-semibold">Response time</h3>
          <Segmented value={range} onChange={setRange} options={RANGES} label="Response time range" className="h-9" />
        </div>
        <LatencyChart data={data} animateKey={`${id}-${range}`} height={200} />
      </div>

      <div className="border-t border-border-soft p-5">
        <h3 className="mb-3 text-sm font-semibold">Availability</h3>
        <UptimeBar data={data} />
      </div>

      <div className="border-t border-border-soft p-5">
        <h3 className="mb-3 text-sm font-semibold">Configuration</h3>
        <dl className="space-y-2.5 text-sm">
          <Row label="Environment" value={envs.length ? envs.join(", ") : "Unassigned"} />
          <Row label="Monitor type" value={monitor.type === "transaction" ? "Transaction" : typeLabel(monitor.type)} />
          <Row label="Alerts" value={alertNames} />
          <Row
            label="Checked from"
            value={locations > 0 ? `${locations} location${locations === 1 ? "" : "s"}` : "This server"}
          />
          {monitor.latency_threshold_ms > 0 && (
            <Row label="Slow after" value={`${formatLatency(monitor.latency_threshold_ms)} × ${monitor.retries}`} />
          )}
          {monitor.slo_target > 0 && <Row label="SLO" value={`${monitor.slo_target}%`} />}
          {tags.length > 0 && <Row label="Tags" value={tags.join(", ")} />}
        </dl>
      </div>
    </section>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="mt-1.5 truncate font-mono text-xl leading-none">{value}</dd>
    </div>
  );
}

function Row({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4">
      <dt className="shrink-0 text-muted-foreground">{label}</dt>
      <dd className="min-w-0 break-words text-right">{value}</dd>
    </div>
  );
}
