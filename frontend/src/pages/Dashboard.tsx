import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { Plus, Search } from "lucide-react";
import { GettingStarted } from "../components/GettingStarted";
import { MonitorIncidentCard } from "../components/MonitorIncidentCard";
import { MONITOR_ROW_HEIGHT, MonitorListRow } from "../components/MonitorListRow";
import { MonitorPanel } from "../components/MonitorPanel";
import { ProbeHealth } from "../components/ProbeHealth";
import { StatTile } from "../components/StatTile";
import { VirtualList } from "../components/VirtualList";
import { Button } from "../components/ui/button";
import { Dialog } from "../components/ui/dialog";
import { Field, Input, Select } from "../components/ui/controls";
import { MeterBar, MiniLine, StackBar } from "../components/ui/meters";
import { useMonitorStore } from "../lib/store";
import { api, ApiError } from "../lib/api";
import { canWrite, useAuth } from "../lib/auth";
import { downKey, useOpenIncidents } from "../lib/incidents";
import { useToast } from "../lib/toast";
import { useMediaQuery } from "../lib/useMediaQuery";
import type { Monitor, NotificationChannel, Realm } from "../lib/types";
import { averageSeries, distinctTags, monitorsInRealm } from "../lib/realms";
import {
  cn,
  formatLatency,
  formatUptime,
  NOT_SERVICE_LATENCY,
  splitTags,
  typeLabel,
  visualStatus,
} from "../lib/utils";

function groupedList(list: Monitor[]): Monitor[] {
  const groups = list.filter((m) => m.type === "group").sort((a, b) => a.name.localeCompare(b.name));
  const groupedIds = new Set(groups.map((g) => g.id));
  const childrenByParent = new Map<number, Monitor[]>();
  const rest: Monitor[] = [];
  for (const m of list) {
    if (m.type === "group") continue;
    if (m.parent_id && groupedIds.has(m.parent_id)) {
      const arr = childrenByParent.get(m.parent_id) ?? [];
      arr.push(m);
      childrenByParent.set(m.parent_id, arr);
      continue;
    }
    rest.push(m);
  }
  const rankSort = (a: Monitor, b: Monitor) => {
    const r = rank(a) - rank(b);
    if (r !== 0) return r;
    return a.name.localeCompare(b.name);
  };
  rest.sort(rankSort);
  const out: Monitor[] = [...rest];
  for (const g of groups.sort(rankSort)) {
    out.push(g);
    const kids = (childrenByParent.get(g.id) ?? []).sort(rankSort);
    out.push(...kids);
  }
  return out;
}

function rank(m: Monitor): number {
  switch (visualStatus(m)) {
    case "down":
      return 0;
    case "stale":
      return 1;
    case "slow":
      return 2;
    case "pending":
      return 3;
    case "muted":
      return 5;
    default:
      return 4;
  }
}

const TYPES = [
  "icmp", "http", "tcp", "dns", "cert", "domain",
  "smtp", "imap", "pop3", "postgres", "mysql", "redis", "transaction", "push", "group",
];
// The chips above the list. Stale and pending only appear when something is.
const STATUS_CHIPS: { key: string; label: string; always: boolean }[] = [
  { key: "down", label: "Down", always: true },
  { key: "slow", label: "Slow", always: true },
  { key: "up", label: "Up", always: true },
  { key: "paused", label: "Paused", always: true },
  { key: "stale", label: "Stale", always: false },
  { key: "pending", label: "Pending", always: false },
];

/** A monitor's status as the status filter names it. */
function statusKey(m: Monitor): string {
  const s = visualStatus(m);
  return s === "muted" ? "paused" : s;
}

export function Dashboard() {
  const { monitors, series, refresh } = useMonitorStore();
  const [params, setParams] = useSearchParams();
  const navigate = useNavigate();
  const { user } = useAuth();
  const writable = canWrite(user);
  const { toast } = useToast();
  const [realms, setRealms] = useState<Realm[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [selected, setSelected] = useState<Set<number>>(new Set());
  const lastClicked = useRef<number | null>(null);
  const [bulkBusy, setBulkBusy] = useState(false);
  const [bulkDialog, setBulkDialog] = useState<"tag" | "realm" | "channel" | "delete" | null>(null);
  const [bulkTag, setBulkTag] = useState("");
  const [bulkRealm, setBulkRealm] = useState("");
  const [bulkChannel, setBulkChannel] = useState("");

  useEffect(() => {
    api.realms
      .list()
      .then(setRealms)
      .catch(() => setRealms([]));
    api.channels
      .list()
      .then(setChannels)
      .catch(() => setChannels([]));
  }, []);

  const query = params.get("q") ?? "";
  const realmParam = params.get("realm") ?? "";
  const typeFilter = params.get("type") ?? "";
  const statusFilter = params.get("status") ?? "";
  const tagFilter = params.get("tag") ?? "";

  const setFilter = (key: string, value: string) => {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next, { replace: true });
  };

  // Everything but the status filter, so each status chip can say how many
  // monitors it would leave.
  const scoped = useMemo(() => {
    if (!monitors) return null;
    let list = monitors;
    if (realmParam === "unassigned" || /^\d+$/.test(realmParam)) {
      list = monitorsInRealm(list, realmParam === "unassigned" ? "unassigned" : Number(realmParam));
    }
    if (typeFilter) list = list.filter((m) => m.type === typeFilter);
    if (tagFilter) {
      list = list.filter((m) => splitTags(m.tags).includes(tagFilter));
    }
    const q = query.trim().toLowerCase();
    if (q) {
      list = list.filter(
        (m) =>
          m.name.toLowerCase().includes(q) ||
          m.target.toLowerCase().includes(q) ||
          (m.tags ?? "").toLowerCase().includes(q),
      );
    }
    return list;
  }, [monitors, query, realmParam, typeFilter, tagFilter]);

  const statusCounts = useMemo(() => {
    const counts: Record<string, number> = {};
    for (const m of scoped ?? []) {
      const k = statusKey(m);
      counts[k] = (counts[k] ?? 0) + 1;
    }
    return counts;
  }, [scoped]);

  const anyFilter = !!(query.trim() || realmParam || typeFilter || statusFilter || tagFilter);

  const filtered = useMemo(() => {
    if (!scoped) return null;
    const list = statusFilter ? scoped.filter((m) => statusKey(m) === statusFilter) : scoped;
    const sorted = [...list].sort((a, b) => {
      const r = rank(a) - rank(b);
      if (r !== 0) return r;
      return a.name.localeCompare(b.name);
    });
    return anyFilter ? sorted : groupedList(sorted);
  }, [scoped, statusFilter, anyFilter]);

  // The four tiles follow the filters, so picking an environment shows that
  // environment's numbers.
  const fleet = useMemo(() => {
    const list = filtered ?? [];
    const enabled = list.filter((m) => m.enabled);
    const count = (k: string) => list.filter((m) => statusKey(m) === k).length;
    const dayOld = enabled.filter(
      (m) => m.state?.uptime_24h != null && Date.now() - new Date(m.created_at).getTime() >= 86_400_000,
    );
    // A down check's latency is its timeout, not the service's speed.
    const latencies = enabled
      .filter((m) => !NOT_SERVICE_LATENCY.has(m.type) && visualStatus(m) !== "down")
      .map((m) => m.state?.last_latency_ms)
      .filter((v): v is number => v != null);
    return {
      enabled: enabled.length,
      paused: list.length - enabled.length,
      up: count("up"),
      down: count("down"),
      slow: count("slow"),
      // The probe stopped checking these: not an outage, not a latency warning.
      stale: count("stale"),
      pending: count("pending"),
      uptime: dayOld.length ? dayOld.reduce((sum, m) => sum + (m.state.uptime_24h ?? 0), 0) / dayOld.length : null,
      latency: latencies.length ? latencies.reduce((a, b) => a + b, 0) / latencies.length : null,
      trend: averageSeries(
        series,
        enabled.filter((m) => !NOT_SERVICE_LATENCY.has(m.type)).map((m) => m.id),
      )
        .map((p) => p.avg_latency)
        .slice(-30),
    };
  }, [filtered, series]);

  const tags = useMemo(() => distinctTags(monitors ?? []), [monitors]);

  // Beside the list sits one monitor's detail. On a narrow screen there is no
  // room for that, and a row opens the monitor's own page instead.
  const wide = useMediaQuery("(min-width: 1024px)");
  const scrollRef = useRef<HTMLDivElement>(null);
  const currentParam = params.get("m") ?? "";
  const current = useMemo(() => {
    if (!monitors || !filtered) return null;
    const picked = currentParam ? monitors.find((m) => String(m.id) === currentParam) : undefined;
    if (picked) return picked;
    // Nothing picked yet: the worst monitor in view. The unfiltered list puts
    // groups and their children last, so it is ranked here, not read off the top.
    let worst: Monitor | null = null;
    for (const m of filtered) {
      if (m.type === "group") continue;
      if (!worst || rank(m) < rank(worst)) worst = m;
    }
    return worst ?? filtered[0] ?? null;
  }, [monitors, filtered, currentParam]);

  // A linked or default pick can sit below what the pane has drawn; bring its
  // row into view, again when the filters change. Not on a live re-sort, so
  // the list never moves under someone who is scrolling it.
  const currentId = current?.id;
  const listReady = filtered !== null;
  const filteredRef = useRef(filtered);
  filteredRef.current = filtered;
  useEffect(() => {
    const el = scrollRef.current;
    const i = filteredRef.current?.findIndex((m) => m.id === currentId) ?? -1;
    if (!wide || !el || i < 0) return;
    const top = i * MONITOR_ROW_HEIGHT;
    if (top < el.scrollTop || top + MONITOR_ROW_HEIGHT > el.scrollTop + el.clientHeight) {
      el.scrollTop = Math.max(0, top - MONITOR_ROW_HEIGHT);
    }
  }, [currentId, wide, listReady, query, realmParam, typeFilter, statusFilter, tagFilter]);

  const openIncidents = useOpenIncidents(useMemo(() => downKey(monitors), [monitors]));
  const currentIncident = current ? openIncidents?.find((i) => i.monitor_id === current.id) : undefined;

  const openMonitor = (m: Monitor) => {
    if (wide) setFilter("m", String(m.id));
    else navigate(`/monitors/${m.id}`);
  };

  const clearFilters = () => {
    const next = new URLSearchParams();
    if (currentParam) next.set("m", currentParam);
    setParams(next, { replace: true });
  };

  const selectedIds = useMemo(() => [...selected], [selected]);
  const allFilteredSelected =
    !!filtered && filtered.length > 0 && filtered.every((m) => selected.has(m.id));

  const toggleSelect = (id: number, index: number, shift: boolean) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (shift && lastClicked.current != null && filtered) {
        const a = Math.min(lastClicked.current, index);
        const b = Math.max(lastClicked.current, index);
        for (let i = a; i <= b; i++) next.add(filtered[i].id);
      } else if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
    lastClicked.current = index;
  };

  const toggleSelectAll = () => {
    if (!filtered) return;
    setSelected((prev) => {
      if (filtered.every((m) => prev.has(m.id))) return new Set();
      return new Set(filtered.map((m) => m.id));
    });
  };

  const runBulk = async (
    action: "pause" | "resume" | "delete" | "add_tag" | "add_realm" | "attach_channel",
    extra?: { tag?: string; realm_id?: number; channel_id?: number },
  ) => {
    if (selectedIds.length === 0) return;
    setBulkBusy(true);
    try {
      const res = await api.monitors.bulk({ ids: selectedIds, action, ...extra });
      await refresh();
      setSelected(new Set());
      setBulkDialog(null);
      setBulkTag("");
      toast("success", `${res.updated} updated`);
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "bulk action failed");
    } finally {
      setBulkBusy(false);
    }
  };

  const row = (i: number) => {
    const m = filtered![i];
    return (
      <MonitorListRow
        key={m.id}
        monitor={m}
        nested={!anyFilter && !!m.parent_id}
        active={wide && current?.id === m.id}
        selected={selected.has(m.id)}
        onOpen={() => openMonitor(m)}
        onToggleSelect={writable ? (shift) => toggleSelect(m.id, i, shift) : undefined}
      />
    );
  };

  if (monitors !== null && monitors.length === 0 && writable) {
    return (
      <div>
        <h1 className="font-brand mb-5 text-xl tracking-tight">Monitors</h1>
        <GettingStarted welcome />
      </div>
    );
  }

  const attention = fleet.down + fleet.slow + fleet.stale;
  const shown = filtered?.length ?? 0;
  const total = monitors?.length ?? 0;

  return (
    <div>
      <div className="mb-5 flex flex-wrap items-center justify-between gap-2">
        <h1 className="font-brand text-xl tracking-tight">Monitors</h1>
        {writable && (
          <Button onClick={() => navigate("/monitors/new")} className="md:hidden">
            <Plus size={15} />
            Add
          </Button>
        )}
      </div>

      {filtered && (
        <div className="mb-4 grid grid-cols-2 gap-4 xl:grid-cols-4">
          <StatTile
            label="Uptime, 24 h"
            value={fleet.uptime == null ? "—" : formatUptime(fleet.uptime)}
            hint={
              fleet.uptime == null
                ? "needs a full day of checks"
                : `across ${fleet.enabled} enabled monitor${fleet.enabled === 1 ? "" : "s"}`
            }
          >
            <MeterBar
              fraction={(fleet.uptime ?? 0) / 100}
              label={fleet.uptime == null ? "No uptime yet" : `${formatUptime(fleet.uptime)} uptime`}
            />
          </StatTile>
          <StatTile
            label="Operational"
            value={String(fleet.up)}
            unit={`/ ${fleet.enabled}`}
            hint={
              fleet.enabled === 0
                ? "nothing enabled in view"
                : fleet.up === fleet.enabled && fleet.paused === 0
                  ? "everything in view is up"
                  : [
                      fleet.down > 0 && `${fleet.down} down`,
                      fleet.slow > 0 && `${fleet.slow} slow`,
                      fleet.stale > 0 && `${fleet.stale} stale`,
                      fleet.pending > 0 && `${fleet.pending} pending`,
                      fleet.paused > 0 && `${fleet.paused} paused`,
                    ]
                      .filter(Boolean)
                      .join(" · ")
            }
          >
            <StackBar
              parts={[
                { value: fleet.up, tone: "success" },
                { value: fleet.down, tone: "danger" },
                { value: fleet.slow + fleet.stale, tone: "warning" },
                { value: fleet.pending, tone: "muted" },
              ]}
              label={
                [
                  `${fleet.up} up`,
                  fleet.down > 0 && `${fleet.down} down`,
                  fleet.slow > 0 && `${fleet.slow} slow`,
                  fleet.stale > 0 && `${fleet.stale} stale`,
                  fleet.pending > 0 && `${fleet.pending} pending`,
                ]
                  .filter(Boolean)
                  .join(", ")
              }
            />
          </StatTile>
          <StatTile
            label="Needs attention"
            value={String(attention)}
            tone={fleet.down > 0 ? "danger" : attention > 0 ? "warning" : undefined}
            hint={
              attention === 0
                ? "nothing down, slow or stale"
                : [
                    fleet.down > 0 && `${fleet.down} outage${fleet.down === 1 ? "" : "s"}`,
                    fleet.slow > 0 && `${fleet.slow} latency warning${fleet.slow === 1 ? "" : "s"}`,
                    fleet.stale > 0 && `${fleet.stale} stale`,
                  ]
                    .filter(Boolean)
                    .join(" · ")
            }
          >
            <StackBar
              parts={[
                { value: fleet.down, tone: "danger" },
                { value: fleet.slow + fleet.stale, tone: "warning" },
              ]}
              label={`${fleet.down} down, ${fleet.slow} slow, ${fleet.stale} stale`}
            />
          </StatTile>
          <StatTile label="Average latency" value={formatLatency(fleet.latency)} hint="latest successful checks">
            <MiniLine values={fleet.trend} className="h-1.5" />
          </StatTile>
        </div>
      )}

      <div className="grid items-start gap-4 lg:grid-cols-[21rem_minmax(0,1fr)] min-[88rem]:grid-cols-[21rem_minmax(0,1fr)_16rem]">
        <section
          className="surface overflow-hidden lg:sticky lg:top-4 lg:row-span-2 min-[88rem]:row-span-1"
          aria-label="Monitor list"
        >
          <div className="flex flex-col gap-2 p-3">
            <div className="relative">
              <label htmlFor="monitor-search" className="sr-only">
                Search monitors
              </label>
              <Search
                size={15}
                aria-hidden="true"
                className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground"
              />
              <Input
                id="monitor-search"
                value={query}
                onChange={(e) => setFilter("q", e.target.value)}
                placeholder="Name, target or tag"
                className="pl-9"
              />
            </div>
            <div className="grid grid-cols-2 gap-2">
              <Select
                aria-label="Environment"
                value={realmParam}
                onChange={(e) => setFilter("realm", e.target.value)}
                className="col-span-2"
              >
                <option value="">All environments</option>
                <option value="unassigned">Unassigned</option>
                {realms.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </Select>
              <Select aria-label="Type" value={typeFilter} onChange={(e) => setFilter("type", e.target.value)}>
                <option value="">All types</option>
                {TYPES.map((t) => (
                  <option key={t} value={t}>
                    {typeLabel(t)}
                  </option>
                ))}
              </Select>
              <Select aria-label="Tag" value={tagFilter} onChange={(e) => setFilter("tag", e.target.value)}>
                <option value="">All tags</option>
                {tags.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </Select>
            </div>
            <div role="group" aria-label="Status" className="flex flex-wrap gap-0.5">
              <StatusChip
                label="All"
                count={scoped?.length ?? 0}
                active={!statusFilter}
                onClick={() => setFilter("status", "")}
              />
              {STATUS_CHIPS.filter((c) => c.always || (statusCounts[c.key] ?? 0) > 0 || statusFilter === c.key).map(
                (c) => (
                  <StatusChip
                    key={c.key}
                    label={c.label}
                    count={statusCounts[c.key] ?? 0}
                    active={statusFilter === c.key}
                    onClick={() => setFilter("status", statusFilter === c.key ? "" : c.key)}
                  />
                ),
              )}
            </div>
          </div>

          <div className="flex h-10 items-center gap-2 border-y border-border bg-muted/40 px-3 text-xs text-muted-foreground">
            {writable && shown > 0 && (
              <input
                type="checkbox"
                checked={allFilteredSelected}
                onChange={toggleSelectAll}
                aria-label="Select all monitors in the list"
              />
            )}
            <span className="font-mono">
              {monitors === null ? "loading" : shown === total ? `${total}` : `${shown} of ${total}`}
            </span>
            <span>worst first</span>
            {anyFilter && (
              <button
                type="button"
                onClick={clearFilters}
                className="ml-auto h-8 rounded px-1.5 font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              >
                Clear filters
              </button>
            )}
          </div>

          {/* On a wide screen the list scrolls in its own pane, so the filters
              and the monitor beside it stay put. */}
          <div
            ref={scrollRef}
            className="lg:max-h-[calc(100vh-20rem)] lg:min-h-72 lg:overflow-y-auto"
          >
            {filtered === null ? (
              Array.from({ length: 6 }).map((_, i) => (
                <div
                  key={i}
                  className="animate-pulse border-b border-border-soft bg-muted/40"
                  style={{ height: MONITOR_ROW_HEIGHT }}
                />
              ))
            ) : shown === 0 ? (
              <p className="px-4 py-16 text-center text-sm text-muted-foreground">
                {total === 0
                  ? "Nothing is watched yet. An admin or operator can add monitors."
                  : "No monitors match these filters."}
              </p>
            ) : shown > 40 ? (
              <VirtualList
                key={wide ? "pane" : "page"}
                count={shown}
                estimateSize={MONITOR_ROW_HEIGHT}
                scrollRef={wide ? scrollRef : undefined}
              >
                {row}
              </VirtualList>
            ) : (
              filtered.map((_, i) => row(i))
            )}
          </div>
        </section>

        {/* With room for two columns only, the incident card goes above the
            pane: Acknowledge has to be in view without scrolling past a chart. */}
        {wide && current && (
          <div className="grid gap-4 lg:col-start-2 lg:row-start-1 lg:grid-cols-2 min-[88rem]:col-start-3 min-[88rem]:grid-cols-1">
            <MonitorIncidentCard
              monitor={current}
              incident={currentIncident}
              loading={openIncidents === null}
              writable={writable}
            />
            <ProbeHealth />
          </div>
        )}
        {wide && current && (
          <div className="min-w-0 lg:col-start-2 lg:row-start-2 min-[88rem]:row-start-1">
            <MonitorPanel
              key={current.id}
              monitor={current}
              realms={realms}
              channels={channels}
              writable={writable}
            />
          </div>
        )}
      </div>

      {writable && selectedIds.length > 0 && (
        <div className="surface sticky bottom-3 z-30 mt-4 flex flex-wrap items-center gap-2 bg-background/95 px-3 py-2 backdrop-blur">
          <span className="mr-2 font-mono text-sm">{selectedIds.length} selected</span>
          <Button size="sm" variant="outline" disabled={bulkBusy} onClick={() => runBulk("pause")}>
            Pause
          </Button>
          <Button size="sm" variant="outline" disabled={bulkBusy} onClick={() => runBulk("resume")}>
            Resume
          </Button>
          <Button size="sm" variant="outline" disabled={bulkBusy} onClick={() => setBulkDialog("tag")}>
            Tag
          </Button>
          <Button size="sm" variant="outline" disabled={bulkBusy} onClick={() => setBulkDialog("realm")}>
            Environment
          </Button>
          <Button size="sm" variant="outline" disabled={bulkBusy} onClick={() => setBulkDialog("channel")}>
            Channel
          </Button>
          <Button size="sm" variant="danger" disabled={bulkBusy} onClick={() => setBulkDialog("delete")}>
            Delete
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
            Clear
          </Button>
        </div>
      )}

      <Dialog open={bulkDialog === "tag"} onClose={() => setBulkDialog(null)} title="Add tag">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void runBulk("add_tag", { tag: bulkTag.trim() });
          }}
          className="flex flex-col gap-3"
        >
          <Field label="Tag">
            <Input value={bulkTag} onChange={(e) => setBulkTag(e.target.value)} placeholder="prod" required />
          </Field>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setBulkDialog(null)}>
              Cancel
            </Button>
            <Button type="submit" disabled={bulkBusy || !bulkTag.trim()}>
              Add
            </Button>
          </div>
        </form>
      </Dialog>

      <Dialog open={bulkDialog === "realm"} onClose={() => setBulkDialog(null)} title="Add to environment">
        {realms.length === 0 ? (
          <p className="text-sm text-muted-foreground">Create an environment on the Environments page first.</p>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void runBulk("add_realm", { realm_id: Number(bulkRealm) });
            }}
            className="flex flex-col gap-3"
          >
            <Field label="Environment">
              <Select value={bulkRealm} onChange={(e) => setBulkRealm(e.target.value)} required>
                <option value="">Choose…</option>
                {realms.map((r) => (
                  <option key={r.id} value={r.id}>
                    {r.name}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setBulkDialog(null)}>
                Cancel
              </Button>
              <Button type="submit" disabled={bulkBusy || !bulkRealm}>
                Add
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      <Dialog open={bulkDialog === "channel"} onClose={() => setBulkDialog(null)} title="Attach channel">
        {channels.length === 0 ? (
          <p className="text-sm text-muted-foreground">Add a notification channel in Settings first.</p>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void runBulk("attach_channel", { channel_id: Number(bulkChannel) });
            }}
            className="flex flex-col gap-3"
          >
            <Field
              label="Channel"
              hint="Only monitors with an explicit channel list change. The default set already receives every channel."
            >
              <Select value={bulkChannel} onChange={(e) => setBulkChannel(e.target.value)} required>
                <option value="">Choose…</option>
                {channels.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name}
                  </option>
                ))}
              </Select>
            </Field>
            <div className="flex justify-end gap-2">
              <Button type="button" variant="outline" onClick={() => setBulkDialog(null)}>
                Cancel
              </Button>
              <Button type="submit" disabled={bulkBusy || !bulkChannel}>
                Attach
              </Button>
            </div>
          </form>
        )}
      </Dialog>

      <Dialog open={bulkDialog === "delete"} onClose={() => setBulkDialog(null)} title="Delete monitors?">
        <p className="text-sm text-muted-foreground">
          Permanently delete {selectedIds.length} monitor{selectedIds.length === 1 ? "" : "s"} and their history.
          This cannot be undone.
        </p>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="outline" onClick={() => setBulkDialog(null)}>
            Cancel
          </Button>
          <Button variant="danger" disabled={bulkBusy} onClick={() => void runBulk("delete")}>
            Delete
          </Button>
        </div>
      </Dialog>
    </div>
  );
}

function StatusChip({
  label,
  count,
  active,
  onClick,
}: {
  label: string;
  count: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        "inline-flex h-8 items-center gap-1.5 rounded-md border px-1.5 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
        active
          ? "border-border bg-muted text-foreground"
          : "border-transparent text-muted-foreground hover:text-foreground",
      )}
    >
      {label}
      <span className="font-mono text-[11px]">{count}</span>
    </button>
  );
}
