import { useCallback, useEffect, useMemo, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { Plus, Trash2 } from "lucide-react";
import { Button } from "../components/ui/button";
import { Field, Input } from "../components/ui/controls";
import { Sparkline } from "../components/Sparkline";
import { StatusDot } from "../components/ui/badge";
import { api, ApiError } from "../lib/api";
import { canWrite, useAuth } from "../lib/auth";
import { useMonitorStore } from "../lib/store";
import { useToast } from "../lib/toast";
import type { Realm } from "../lib/types";
import {
  averageSeries,
  monitorsInRealm,
  realmAvgLatency,
  realmCounts,
  realmMark,
} from "../lib/realms";
import { formatLatency, since, visualStatus } from "../lib/utils";

export function Realms() {
  const { monitors, series } = useMonitorStore();
  const { user } = useAuth();
  const writable = canWrite(user);
  const { toast } = useToast();
  const navigate = useNavigate();
  const [realms, setRealms] = useState<Realm[] | null>(null);
  const [name, setName] = useState("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      setRealms(await api.realms.list());
    } catch {
      setRealms([]);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const unassigned = useMemo(
    () => (monitors ?? []).filter((m) => !m.realm_ids || m.realm_ids.length === 0),
    [monitors],
  );

  const create = async (e: FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setSaving(true);
    try {
      await api.realms.create(name.trim());
      setName("");
      toast("success", "environment created");
      await load();
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "failed to create environment");
    } finally {
      setSaving(false);
    }
  };

  const remove = async (id: number, realmName: string) => {
    if (!window.confirm(`Delete environment “${realmName}”? Monitors are not deleted.`)) return;
    try {
      await api.realms.remove(id);
      toast("success", "environment deleted");
      await load();
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "failed to delete environment");
    }
  };

  return (
    <div className="animate-fade-in-up">
      <div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-end">
        <div className="min-w-0">
          <h1 className="font-brand text-xl tracking-tight">Environments</h1>
          <p className="mt-1 max-w-xl text-sm text-muted-foreground">
            Named groups of monitors — Production, Staging, a customer, a site.
            Open one to see its monitors. A monitor can belong to more than one.
          </p>
        </div>
        {writable && (
          <form onSubmit={create} className="flex flex-wrap items-end gap-2 sm:ml-auto">
            <Field label="New environment">
              <Input
                value={name}
                onChange={(e) => setName(e.target.value)}
                placeholder="Production"
                required
              />
            </Field>
            <Button type="submit" disabled={saving || !name.trim()}>
              <Plus size={14} />
              Create
            </Button>
          </form>
        )}
      </div>

      {realms === null || monitors === null ? (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 3 }).map((_, i) => (
            <div key={i} className="surface h-48 animate-pulse bg-muted/40" />
          ))}
        </div>
      ) : (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {realms.map((r) => {
            const members = monitorsInRealm(monitors, r.id);
            return (
              <RealmCard
                key={r.id}
                title={r.name}
                members={members}
                series={series}
                onOpen={() => navigate(`/monitors?realm=${r.id}`)}
                onDelete={writable ? () => remove(r.id, r.name) : undefined}
              />
            );
          })}
          <RealmCard
            title="Unassigned"
            members={unassigned}
            series={series}
            onOpen={() => navigate("/monitors?realm=unassigned")}
          />
        </div>
      )}
    </div>
  );
}

function RealmCard({
  title,
  members,
  series,
  onOpen,
  onDelete,
}: {
  title: string;
  members: ReturnType<typeof monitorsInRealm>;
  series: Record<number, import("../lib/types").SeriesPoint[]>;
  onOpen: () => void;
  onDelete?: () => void;
}) {
  const mark = realmMark(members);
  const counts = realmCounts(members);
  const avg = realmAvgLatency(members);
  const spark = averageSeries(
    series,
    members.map((m) => m.id),
  );
  const trouble = members
    .filter((m) => {
      const s = visualStatus(m);
      return s === "down" || s === "slow";
    })
    .sort((a, b) => (visualStatus(a) === "down" ? 0 : 1) - (visualStatus(b) === "down" ? 0 : 1))
    .slice(0, 3);
  const other = counts.total - counts.down - counts.slow - counts.up;

  return (
    <div className="surface relative">
      <button
        type="button"
        onClick={onOpen}
        className={`flex w-full flex-col gap-4 p-5 text-left transition-colors hover:bg-muted/40 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring ${
          onDelete ? "pr-12" : ""
        }`}
      >
        <div className="flex items-center gap-2">
          <StatusDot status={members.length === 0 ? "muted" : mark} />
          <span className="truncate text-sm font-semibold">{title}</span>
        </div>
        <div className="flex items-end justify-between gap-4">
          <div>
            <div className="font-display text-[34px] leading-none">{counts.total}</div>
            <div className="mt-1 text-xs text-muted-foreground">{counts.total === 1 ? "monitor" : "monitors"}</div>
          </div>
          <div className="w-28">
            <Sparkline points={spark} tone={mark === "down" ? "down" : mark === "slow" ? "slow" : "up"} />
            <div className="mt-1 text-right font-mono text-xs text-muted-foreground">{formatLatency(avg)}</div>
          </div>
        </div>
        {/* The split of the environment, one segment per state. */}
        <div
          role="img"
          aria-label={`${counts.up} up, ${counts.slow} slow, ${counts.down} down`}
          className="flex h-1.5 w-full gap-px overflow-hidden rounded-full bg-muted"
        >
          {counts.total > 0 && (
            <>
              {counts.down > 0 && <span className="bg-danger" style={{ flex: counts.down }} />}
              {counts.slow > 0 && <span className="bg-warning" style={{ flex: counts.slow }} />}
              {counts.up > 0 && <span className="bg-success" style={{ flex: counts.up }} />}
              {other > 0 && <span className="bg-muted-foreground/40" style={{ flex: other }} />}
            </>
          )}
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 font-mono text-xs text-muted-foreground">
          {counts.down > 0 && <span className="font-medium text-danger">{counts.down} down</span>}
          {counts.slow > 0 && <span className="font-medium text-warning">{counts.slow} slow</span>}
          {counts.up > 0 && <span className="text-success">{counts.up} up</span>}
          {counts.total === 0 && <span>empty</span>}
        </div>
        {trouble.length > 0 && (
          <ul className="-mx-2 border-t border-border-soft pt-3">
            {trouble.map((m) => {
              const s = visualStatus(m);
              return (
                <li key={m.id} className="flex items-center gap-2 px-2 py-1 text-xs">
                  <StatusDot status={s} className="h-2 w-2" />
                  <span className="min-w-0 flex-1 truncate">{m.name}</span>
                  <span className={s === "down" ? "font-mono text-danger" : "font-mono text-warning"}>
                    {s === "down" ? (m.state?.down_since ? since(m.state.down_since) : "down") : formatLatency(m.state?.last_latency_ms)}
                  </span>
                </li>
              );
            })}
          </ul>
        )}
      </button>
      {onDelete && (
        <button
          type="button"
          className="absolute right-2 top-2 flex h-10 w-10 items-center justify-center rounded-lg text-muted-foreground transition hover:bg-danger/10 hover:text-danger focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }}
          aria-label={`Delete ${title}`}
        >
          <Trash2 size={14} />
        </button>
      )}
    </div>
  );
}
