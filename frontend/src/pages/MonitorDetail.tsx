import { useCallback, useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import {
  ArrowLeft,
  Bell,
  BellOff,
  Clock,
  Copy,
  Mail,
  Pause,
  Play,
  RefreshCw,
  Trash2,
  Zap,
} from "lucide-react";
import { LatencyChart } from "../components/LatencyChart";
import { UptimeBar } from "../components/UptimeBar";
import { StepList } from "../components/StepList";
import { Button } from "../components/ui/button";
import { Segmented } from "../components/ui/segmented";
import { Menu, MenuItem } from "../components/ui/menu";
import { Badge, StatusDot, TypeBadge } from "../components/ui/badge";
import { Dialog } from "../components/ui/dialog";
import { api, ApiError } from "../lib/api";
import { canWrite, useAuth } from "../lib/auth";
import { useMonitorStore } from "../lib/store";
import { useSettings } from "../lib/settings";
import { useToast } from "../lib/toast";
import type {
  Alert,
  CheckEvidence,
  LatencyPercentiles,
  Monitor,
  MonitorEvent,
  ResultsResponse,
} from "../lib/types";
import {
  certIsWarning,
  formatCertExpiry,
  formatDateTime,
  formatLatency,
  formatUptime,
  isAcked,
  isMuted,
  domainIsWarning,
  uptimeLabel,
  timeAgo,
  visualStatus,
} from "../lib/utils";

const RANGES = [
  { key: "1h", label: "1H" },
  { key: "24h", label: "24H" },
  { key: "7d", label: "7D" },
  { key: "30d", label: "30D" },
  { key: "90d", label: "90D" },
  { key: "365d", label: "1Y" },
];

const EVENT_TONES: Record<string, "up" | "down" | "muted" | "warning"> = {
  up: "up",
  down: "down",
  paused: "muted",
  resumed: "up",
  cert: "warning",
  slow: "warning",
  resend: "warning",
  muted: "muted",
  unmuted: "up",
  acked: "warning",
};

const MUTE_PRESETS = [
  { minutes: 15, label: "15 minutes" },
  { minutes: 60, label: "1 hour" },
  { minutes: 240, label: "4 hours" },
  { minutes: 1440, label: "24 hours" },
  { minutes: -1, label: "Until I unmute" },
];

export function MonitorDetail() {
  const { id } = useParams();
  const monitorId = Number(id);
  const { monitors, refresh } = useMonitorStore();
  const { toast } = useToast();
  const navigate = useNavigate();
  const { user } = useAuth();
  const writable = canWrite(user);

  const { settings } = useSettings();
  const [range, setRange] = useState("24h");
  const [results, setResults] = useState<ResultsResponse | null>(null);
  const [events, setEvents] = useState<MonitorEvent[] | null>(null);
  const [alerts, setAlerts] = useState<Alert[] | null>(null);
  const [pct, setPct] = useState<LatencyPercentiles | null>(null);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [muteOpen, setMuteOpen] = useState(false);
  const [busy, setBusy] = useState(false);

  const [fetched, setFetched] = useState<Monitor | null>(null);
  const monitor = useMemo(
    () => {
      const live = monitors?.find((m) => m.id === monitorId) ?? fetched;
      if (!live) return live;
      if (fetched?.regions) return { ...live, regions: fetched.regions, slo_target: fetched.slo_target };
      return live;
    },
    [monitors, monitorId, fetched],
  );

  useEffect(() => {
    if (!monitorId || Number.isNaN(monitorId)) return;
    let cancelled = false;
    api.monitors
      .get(monitorId)
      .then((m) => {
        if (!cancelled) setFetched(m);
      })
      .catch(() => {
        // A lost session is handled once, for every page (see lib/api.ts):
        // going to sign-in from here as well raced it and lost the reason
        // and the way back.
      });
    return () => {
      cancelled = true;
    };
  }, [monitorId, navigate]);

  const loadHistory = useCallback(async () => {
    try {
      const [r, e, a, p] = await Promise.all([
        api.monitors.results(monitorId, range),
        api.monitors.events(monitorId),
        api.monitors.alerts(monitorId),
        api.monitors.percentiles(monitorId, range === "7d" || range === "30d" ? "7d" : "24h"),
      ]);
      setResults(r);
      setEvents(e);
      setAlerts(a);
      setPct(p);
    } catch (err) {
      // Not signed in is the app's to handle; say nothing over the top of it.
      if (!(err instanceof ApiError && err.status === 401)) toast("error", "failed to load monitor history");
    }
  }, [monitorId, range, toast, navigate]);

  useEffect(() => {
    loadHistory();
  }, [loadHistory]);

  // Keyed on whether there is a monitor, not on the monitor: that object is
  // new on every live result, and a timer restarted that often never fires.
  const loaded = !!monitor;
  useEffect(() => {
    if (!loaded) return;
    const interval = setInterval(loadHistory, range.endsWith("h") ? 30_000 : 300_000);
    return () => clearInterval(interval);
  }, [loaded, range, loadHistory]);

  const togglePause = async () => {
    setBusy(true);
    try {
      const res = await api.monitors.pause(monitorId);
      await refresh();
      toast("success", res.enabled ? "monitor resumed" : "monitor paused");
    } catch {
      toast("error", "failed to toggle monitor");
    } finally {
      setBusy(false);
    }
  };

  // Only worth a table when there is more than one place, or the one
  // place is not this server.
  const locationIds = monitor
    ? [...new Set((monitor.probe_node_ids?.length ? monitor.probe_node_ids : [0]))]
    : [0];
  const locationCount = locationIds.length;
  const showLocations =
    !!monitor && (monitor.regions?.length ?? 0) > 0 && (locationCount > 1 || locationIds[0] !== 0);

  const runCheckNow = async () => {
    setBusy(true);
    try {
      const res = await api.monitors.checkNow(monitorId);
      await refresh();
      const live = await api.monitors.get(monitorId).catch(() => null);
      if (live) setFetched(live);
      await loadHistory();
      // A paused monitor, or one checked only from probe nodes, is tested
      // from here, but the result does not change its state.
      const note = res.applied
        ? ""
        : monitor?.enabled === false
          ? " (paused; not counted)"
          : " (from this server; not counted)";
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

  const muteFor = async (minutes: number) => {
    setBusy(true);
    try {
      const m = await api.monitors.mute(monitorId, minutes);
      setFetched(m);
      await refresh();
      setMuteOpen(false);
      toast("success", minutes === 0 ? "alerts unmuted" : "alerts muted");
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "failed to mute");
    } finally {
      setBusy(false);
    }
  };

  const ackNow = async () => {
    setBusy(true);
    try {
      const m = await api.monitors.ack(monitorId, 0);
      setFetched(m);
      await refresh();
      toast("success", "acknowledged — resend is off until the next down");
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "failed to acknowledge");
    } finally {
      setBusy(false);
    }
  };

  const cloneMonitor = async () => {
    setBusy(true);
    try {
      const copy = await api.monitors.clone(monitorId);
      await refresh();
      toast("success", "monitor cloned");
      navigate(`/monitors/${copy.id}`);
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "failed to clone");
    } finally {
      setBusy(false);
    }
  };

  const del = async () => {
    setBusy(true);
    try {
      await api.monitors.remove(monitorId);
      await refresh();
      toast("success", "monitor deleted");
      navigate("/");
    } catch {
      toast("error", "failed to delete monitor");
    } finally {
      setBusy(false);
      setConfirmDelete(false);
    }
  };

  if (!monitor) {
    return (
      <div className="py-20 text-center text-sm text-muted-foreground">
        {monitors === null ? "loading…" : "monitor not found"}
      </div>
    );
  }

  const state = monitor.state ?? {
    status: "pending" as const,
    last_latency_ms: null,
    last_check_at: null,
    last_change_at: null,
    consecutive_failures: 0,
    down_since: null,
    escalated_at: null,
    uptime_24h: null,
    tls_expires_at: null,
    slow: false,
    slow_since: null,
  };
  const vis = visualStatus(monitor);

  return (
    <div className="animate-fade-in-up">
      <Link
        to="/monitors"
        className="mb-3 inline-flex h-8 items-center gap-1 rounded text-sm text-muted-foreground transition hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ArrowLeft size={15} />
        Monitors
      </Link>
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-center">
        <div className="flex min-w-0 flex-wrap items-center gap-2.5">
          <StatusDot status={vis} />
          <h1 className="font-brand truncate text-xl">{monitor.name}</h1>
          <TypeBadge type={monitor.type} />
          {!monitor.enabled && <Badge tone="muted">paused</Badge>}
          {vis === "slow" && <Badge tone="warning">slow</Badge>}
          {isMuted(monitor.state) && <Badge tone="warning">muted</Badge>}
          {isAcked(monitor.state) && <Badge tone="warning">acked</Badge>}
        </div>
        <div className="flex flex-wrap items-center gap-2 sm:ml-auto">
          {writable && monitor.type !== "group" && (
            <Button onClick={runCheckNow} disabled={busy}>
              <Zap size={14} />
              Check now
            </Button>
          )}
          {writable && vis === "down" && !isAcked(monitor.state) && (
            <Button variant="outline" onClick={ackNow} disabled={busy}>
              Acknowledge
            </Button>
          )}
          {writable &&
            (isMuted(monitor.state) ? (
              <Button variant="outline" onClick={() => muteFor(0)} disabled={busy}>
                <Bell size={14} />
                Unmute
              </Button>
            ) : (
              <Button variant="outline" onClick={() => setMuteOpen(true)} disabled={busy}>
                <BellOff size={14} />
                Mute
              </Button>
            ))}
          {writable && (
            <Button variant="outline" onClick={() => navigate(`/monitors/${monitorId}/edit`)}>
              Edit
            </Button>
          )}
          <Menu>
            <MenuItem onClick={() => loadHistory()}>
              <RefreshCw size={14} />
              Refresh data
            </MenuItem>
            {writable && (
              <MenuItem onClick={togglePause} disabled={busy}>
                {monitor.enabled ? <Pause size={14} /> : <Play size={14} />}
                {monitor.enabled ? "Pause monitor" : "Resume monitor"}
              </MenuItem>
            )}
            {writable && (
              <MenuItem onClick={cloneMonitor} disabled={busy}>
                <Copy size={14} />
                Duplicate
              </MenuItem>
            )}
            {writable && (
              <MenuItem danger onClick={() => setConfirmDelete(true)}>
                <Trash2 size={14} />
                Delete monitor
              </MenuItem>
            )}
          </Menu>
        </div>
      </div>

      <div className="surface mb-6 grid grid-cols-2 gap-x-8 gap-y-6 p-5 sm:grid-cols-4">
        <Metric label="Target" value={monitor.target} />
        <Metric
          label="Status"
          value={vis === "muted" ? "Paused" : vis === "slow" ? "Slow" : vis[0].toUpperCase() + vis.slice(1)}
          className={
            vis === "muted"
              ? "text-muted-foreground"
              : vis === "up"
                ? "text-success"
                : vis === "down"
                  ? "text-danger"
                  : "text-warning"
          }
        />
        <Metric
          label="Latency"
          // A failed check has no latency worth showing.
          value={vis === "down" ? "—" : formatLatency(state.last_latency_ms)}
          className={vis === "slow" ? "text-warning" : undefined}
        />
        {monitor.type === "domain" && (
          <Metric
            label="Domain expires"
            value={state.expires_at ? formatCertExpiry(state.expires_at) : "collecting…"}
            className={domainIsWarning(state.expires_at) ? "text-warning" : undefined}
          />
        )}
        <Metric label="Uptime, 24h" value={uptimeLabel(state.uptime_24h, monitor.created_at)} />
        {monitor.type === "push" && monitor.config.schedule ? (
          <>
            <Metric
              label="Schedule"
              value={
                <span className="font-mono text-sm">
                  {monitor.config.schedule}{" "}
                  <span className="font-sans text-xs text-muted-foreground">{monitor.config.timezone || "UTC"}</span>
                </span>
              }
            />
            <Metric
              label="Next ping due"
              value={state.next_ping_at ? formatDateTime(state.next_ping_at) : "—"}
            />
          </>
        ) : (
          <Metric
            label="Interval"
            value={
              <span className="flex items-center gap-1">
                <Clock size={13} className="text-muted-foreground" />
                {monitor.interval_seconds}s
              </span>
            }
          />
        )}
        <Metric label="Last check" value={timeAgo(state.last_check_at)} />
        <Metric label="Type" value={monitor.type.toUpperCase()} />
        {pct && pct.samples > 0 && (
          <Metric
            label="Latency p50 / p95 / p99"
            value={`${formatLatency(pct.p50)} / ${formatLatency(pct.p95)} / ${formatLatency(pct.p99)}`}
          />
        )}
        {monitor.type === "push" && monitor.config?.push_token && (
          <Metric
            label="Push URL"
            value={`${(settings?.general.base_url || window.location.origin).replace(/\/$/, "")}/api/push/${monitor.config.push_token}`}
          />
        )}
        {monitor.latency_threshold_ms > 0 && (
          <Metric
            label="Slow after"
            value={`${monitor.latency_threshold_ms}ms × ${monitor.retries}`}
          />
        )}
        {(state.tls_expires_at || monitor.target.startsWith("https:") || monitor.type === "cert") && (
          <Metric
            label="TLS expires"
            value={state.tls_expires_at ? formatCertExpiry(state.tls_expires_at) : "collecting…"}
            className={
              certIsWarning(state.tls_expires_at) ? "text-warning" : undefined
            }
          />
        )}
        {monitor.slo_target > 0 && (
          <Metric label="SLO" value={`${monitor.slo_target}%`} />
        )}
        {isMuted(monitor.state) && (
          <Metric
            label="Muted until"
            value={
              monitor.state.muted_until
                ? formatDateTime(monitor.state.muted_until)
                : "on"
            }
            className="text-warning"
          />
        )}
        {isAcked(monitor.state) && (
          <Metric
            label="Acked"
            value={
              monitor.state.acked_until
                ? `until ${formatDateTime(monitor.state.acked_until)}`
                : "until next down"
            }
            className="text-warning"
          />
        )}
      </div>

      {monitor.state.last_failure_evidence && (
        <FailureEvidence evidence={monitor.state.last_failure_evidence} />
      )}

      {showLocations && (
        <section className="surface mb-6 p-5">
          <div className="mb-3 flex flex-wrap items-baseline justify-between gap-2">
            <h2 className="text-sm font-semibold">Locations</h2>
            <p className="text-xs text-muted-foreground">
              {locationCount > 1
                ? `Down when ${Math.min(monitor.confirm_locations ?? 1, locationCount)} of ${locationCount} locations agree, each after ${monitor.retries} failed check${monitor.retries === 1 ? "" : "s"}.`
                : "Checked from one probe node."}
            </p>
          </div>
          <div className="overflow-x-auto border-t border-border">
            <table className="w-full text-left text-sm">
              <thead>
                <tr className="text-xs text-muted-foreground">
                  <th className="py-2 font-medium">Location</th>
                  <th className="py-2 font-medium">Region</th>
                  <th className="py-2 font-medium">Status</th>
                  <th className="py-2 font-medium">Latency</th>
                  <th className="py-2 font-medium">Last check</th>
                </tr>
              </thead>
              <tbody>
                {monitor.regions!.map((r) => {
                  const stale =
                    !r.last_check_at ||
                    Date.now() - new Date(r.last_check_at).getTime() > (2 * monitor.interval_seconds + 30) * 1000;
                  const failing = r.status !== "down" && (r.consecutive_failures ?? 0) > 0;
                  return (
                    <tr key={r.node_id} className="border-t border-border align-top">
                      <td className="py-2">{r.node_id === 0 ? "This server" : r.name}</td>
                      <td className="py-2 text-muted-foreground">{r.node_id === 0 ? "—" : r.region || "—"}</td>
                      <td className="py-2">
                        <div className="flex flex-wrap items-center gap-1.5">
                          <Badge tone={stale ? "muted" : r.status === "down" ? "down" : r.status === "up" ? "up" : "pending"}>
                            {stale ? "not reporting" : r.status}
                          </Badge>
                          {!stale && failing && (
                            <span className="text-xs text-warning">
                              {r.consecutive_failures} of {monitor.retries} failed
                            </span>
                          )}
                        </div>
                        {!stale && r.last_error && (r.status === "down" || failing) && (
                          <div className="mt-1 max-w-md break-words text-xs text-danger">{r.last_error}</div>
                        )}
                      </td>
                      <td className="py-2 tabular-nums">{r.status === "down" ? "—" : formatLatency(r.last_latency_ms)}</td>
                      <td className="py-2 tabular-nums">{timeAgo(r.last_check_at)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </section>
      )}

      <section className="surface mb-6 p-5">
        <div className="mb-3 flex flex-wrap items-baseline gap-x-3 gap-y-2">
          <h2 className="text-sm font-semibold">Latency</h2>
          <Segmented
            value={range}
            onChange={setRange}
            options={RANGES.map((r) => ({ value: r.key, label: r.label }))}
            label="Latency range"
            className="h-9"
          />
          <span className="ml-auto text-xs text-muted-foreground">
            {results?.granularity === "raw" ? "per-check data" : "hourly/daily rollups"}
          </span>
        </div>
        <LatencyChart data={results} animateKey={range} />
        <div className="mt-5">
          <UptimeBar data={results} />
        </div>
      </section>

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <section className="surface p-5">
          <h2 className="mb-3 text-sm font-semibold">Events</h2>
          {events === null ? (
            <p className="text-sm text-muted-foreground">loading…</p>
          ) : events.length === 0 ? (
            <p className="text-sm text-muted-foreground">no events recorded</p>
          ) : (
            <ul className="divide-y divide-border border-t border-border">
              {events.slice(0, 15).map((e) => (
                <li key={e.id} className="flex items-center gap-3 py-2.5 text-sm">
                  <Badge tone={EVENT_TONES[e.type] ?? "muted"}>{e.type}</Badge>
                  <span className="truncate text-muted-foreground">{e.detail || "—"}</span>
                  <span className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">
                    {formatDateTime(e.ts)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className="surface p-5">
          <h2 className="mb-3 flex items-center gap-2 text-sm font-semibold">
            <Bell size={14} className="text-muted-foreground" />
            Alerts
          </h2>
          {alerts === null ? (
            <p className="text-sm text-muted-foreground">loading…</p>
          ) : alerts.length === 0 ? (
            <p className="text-sm text-muted-foreground">no alerts sent yet</p>
          ) : (
            <ul className="divide-y divide-border border-t border-border">
              {alerts.slice(0, 15).map((a) => (
                <li key={a.id} className="flex items-center gap-3 py-2.5 text-sm">
                  <Mail size={13} className="shrink-0 text-muted-foreground" />
                  <span className="font-medium capitalize">{a.type}</span>
                  <span
                    className={`text-xs font-medium ${
                      a.status === "sent"
                        ? "text-success"
                        : a.status === "failed"
                          ? "text-danger"
                          : a.status === "retrying"
                            ? "text-warning"
                            : "text-muted-foreground"
                    }`}
                    // Why it failed, or is being retried, or was skipped.
                    title={a.error || undefined}
                  >
                    {a.status}
                  </span>
                  <span
                    className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground"
                    title={a.recipients}
                  >
                    {formatDateTime(a.created_at)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>

      <Dialog
        open={muteOpen}
        onClose={() => setMuteOpen(false)}
        title="Mute alerts"
      >
        <p className="mb-4 text-sm text-muted-foreground">
          Checks keep running. Down, recovery, slow, cert, escalation, and resend
          stay quiet until the mute ends.
        </p>
        <div className="flex flex-col gap-2">
          {MUTE_PRESETS.map((p) => (
            <Button
              key={p.minutes}
              variant="outline"
              onClick={() => muteFor(p.minutes)}
              disabled={busy}
            >
              {p.label}
            </Button>
          ))}
        </div>
      </Dialog>

      <Dialog
        open={confirmDelete}
        onClose={() => setConfirmDelete(false)}
        title="Delete monitor?"
      >
        <p className="text-sm text-muted-foreground">
          <span className="font-medium text-foreground">{monitor.name}</span> and all
          of its recorded history will be permanently removed. This cannot be undone.
        </p>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="outline" onClick={() => setConfirmDelete(false)}>
            Cancel
          </Button>
          <Button variant="danger" onClick={del} disabled={busy}>
            <Trash2 size={14} />
            Delete permanently
          </Button>
        </div>
      </Dialog>
    </div>
  );
}

function FailureEvidence({ evidence }: { evidence: CheckEvidence }) {
  const { toast } = useToast();
  const copyCurl = async () => {
    if (!evidence.curl) return;
    try {
      await navigator.clipboard.writeText(evidence.curl);
      toast("success", "curl copied");
    } catch {
      toast("error", "could not copy");
    }
  };
  return (
    <section className="surface mb-6 p-5">
      <h2 className="mb-3 text-sm font-semibold">Last failure</h2>
      <dl className="grid grid-cols-1 gap-x-8 gap-y-3 text-sm sm:grid-cols-2">
        <div>
          <dt className="text-[11px] font-medium text-muted-foreground">When</dt>
          <dd className="mt-1 tabular-nums">{formatDateTime(evidence.at)}</dd>
        </div>
        {evidence.error && (
          <div>
            <dt className="text-[11px] font-medium text-muted-foreground">Error</dt>
            <dd className="mt-1 break-words text-danger">{evidence.error}</dd>
          </div>
        )}
        {evidence.status_code ? (
          <div>
            <dt className="text-[11px] font-medium text-muted-foreground">
              {evidence.steps?.length ? "HTTP status (failing step)" : "HTTP status"}
            </dt>
            <dd className="mt-1 tabular-nums">{evidence.status_code}</dd>
          </div>
        ) : null}
        {evidence.tls_error && (
          <div>
            <dt className="text-[11px] font-medium text-muted-foreground">TLS</dt>
            <dd className="mt-1">{evidence.tls_error}</dd>
          </div>
        )}
        {evidence.remote_addr && (
          <div>
            <dt className="text-[11px] font-medium text-muted-foreground">Remote</dt>
            <dd className="mt-1 font-mono text-xs">{evidence.remote_addr}</dd>
          </div>
        )}
        {evidence.dns_answers && evidence.dns_answers.length > 0 && (
          <div className="sm:col-span-2">
            <dt className="text-[11px] font-medium text-muted-foreground">DNS answers</dt>
            <dd className="mt-1 font-mono text-xs">{evidence.dns_answers.join(", ")}</dd>
          </div>
        )}
        {evidence.steps && evidence.steps.length > 0 && (
          <div className="sm:col-span-2">
            <dt className="text-[11px] font-medium text-muted-foreground">Steps</dt>
            <dd className="mt-1">
              <StepList steps={evidence.steps} />
            </dd>
          </div>
        )}
        {evidence.body_snippet && (
          <div className="sm:col-span-2">
            <dt className="text-[11px] font-medium text-muted-foreground">
              {evidence.steps?.length ? "Body of the failing step" : "Body"}
            </dt>
            <dd className="mt-1">
              {/* Scrollable, so it has to be reachable from the keyboard. */}
              <div
                tabIndex={0}
                role="region"
                aria-label="Response body"
                className="max-h-40 overflow-auto whitespace-pre-wrap break-words rounded font-mono text-xs text-muted-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                {evidence.body_snippet}
              </div>
            </dd>
          </div>
        )}
      </dl>
      {evidence.curl && (
        <Button variant="outline" size="sm" className="mt-4" onClick={copyCurl}>
          <Copy size={14} />
          Copy as curl
        </Button>
      )}
    </section>
  );
}

function Metric({
  label,
  value,
  className,
}: {
  label: string;
  value: ReactNode;
  className?: string;
}) {
  return (
    <div>
      <div className="text-[11px] font-medium text-muted-foreground">
        {label}
      </div>
      <div className={`mt-1 truncate font-mono text-sm ${className ?? ""}`}>
        {value}
      </div>
    </div>
  );
}
