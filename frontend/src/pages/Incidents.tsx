import { useCallback, useEffect, useState, type FormEvent } from "react";
import { Loader2, Plus, X } from "lucide-react";
import { Link } from "react-router-dom";
import { Button } from "../components/ui/button";
import { Field, Input, Select, Textarea } from "../components/ui/controls";
import { Badge } from "../components/ui/badge";
import { IncidentTimeline } from "../components/IncidentTimeline";
import { Segmented } from "../components/ui/segmented";
import { api, ApiError } from "../lib/api";
import { canWrite, useAuth } from "../lib/auth";
import { useToast } from "../lib/toast";
import { incidentTone } from "../lib/incidents";
import { useMonitorStore } from "../lib/store";
import type { Incident } from "../lib/types";
import { cn, formatDateTime, since } from "../lib/utils";

const STATUSES = ["open", "investigating", "identified", "monitoring", "resolved"] as const;
const IMPACTS = ["none", "minor", "major", "critical"] as const;

export function Incidents() {
  // Viewers read incidents; opening and updating them is for writers.
  const writable = canWrite(useAuth().user);
  const { toast } = useToast();
  const { monitors } = useMonitorStore();
  const [list, setList] = useState<Incident[] | null>(null);
  const [openOnly, setOpenOnly] = useState(true);
  const [selected, setSelected] = useState<Incident | null>(null);
  const [composing, setComposing] = useState(false);
  const [saving, setSaving] = useState(false);
  const [title, setTitle] = useState("");
  const [impact, setImpact] = useState("major");
  const [status, setStatus] = useState("investigating");
  const [message, setMessage] = useState("");
  const [monitorID, setMonitorID] = useState<number | "">("");
  const [updateStatus, setUpdateStatus] = useState("investigating");
  const [updateMessage, setUpdateMessage] = useState("");

  const load = useCallback(async () => {
    try {
      setList(await api.incidents.list(openOnly));
    } catch {
      setList([]);
    }
  }, [openOnly]);

  useEffect(() => {
    load();
  }, [load]);

  const handleErr = (err: unknown, fallback: string) => {
    toast("error", err instanceof ApiError ? err.message : fallback);
  };

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const inc = await api.incidents.create({
        title,
        impact,
        status,
        message,
        monitor_id: monitorID === "" ? null : Number(monitorID),
        public: true,
      });
      toast("success", "incident opened");
      setTitle("");
      setMessage("");
      setComposing(false);
      await load();
      if (inc && typeof inc === "object" && "id" in inc) void open((inc as Incident).id);
    } catch (err) {
      handleErr(err, "failed to open incident");
    } finally {
      setSaving(false);
    }
  };

  const open = async (id: number) => {
    try {
      const inc = await api.incidents.get(id);
      setSelected(inc);
      setComposing(false);
      setUpdateStatus(inc.status === "open" ? "investigating" : inc.status);
      setUpdateMessage("");
    } catch (err) {
      handleErr(err, "failed to load incident");
    }
  };

  const postUpdate = async (e: FormEvent) => {
    e.preventDefault();
    if (!selected) return;
    setSaving(true);
    try {
      const next = await api.incidents.update(selected.id, {
        status: updateStatus,
        message: updateMessage,
      });
      setSelected(next);
      toast("success", "update posted");
      setUpdateMessage("");
      await load();
    } catch (err) {
      handleErr(err, "failed to post update");
    } finally {
      setSaving(false);
    }
  };

  const rows = list ?? [];
  const showPanel = selected || (composing && writable);

  return (
    <div className="animate-fade-in-up">
      <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="font-brand text-xl tracking-tight">Incidents</h1>
          <p className="mt-1 max-w-2xl text-sm text-muted-foreground">
            Outages open an incident on their own.{" "}
            {writable
              ? "Post updates here; confirmed status-page subscribers get email or webhook copies without logging in."
              : "Admins and operators post updates, which status-page subscribers receive by email or webhook."}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Segmented
            value={openOnly ? "open" : "all"}
            onChange={(v) => setOpenOnly(v === "open")}
            options={[
              { value: "open", label: "Open" },
              { value: "all", label: "All" },
            ]}
            label="Which incidents"
          />
          {writable && (
            <Button
              onClick={() => {
                setSelected(null);
                setComposing(true);
              }}
            >
              <Plus size={16} />
              Open incident
            </Button>
          )}
        </div>
      </div>

      <div className={cn("grid gap-5", showPanel && "xl:grid-cols-[minmax(0,1fr)_28rem]")}>
        <section className="surface overflow-hidden self-start">
          {list === null ? (
            <p className="px-5 py-6 text-sm text-muted-foreground">Loading…</p>
          ) : rows.length === 0 ? (
            <p className="px-5 py-8 text-sm text-muted-foreground">
              {openOnly ? "No open incidents." : "No incidents yet."}
            </p>
          ) : (
            <ul className="divide-y divide-border-soft">
              {rows.map((inc) => {
                const active = selected?.id === inc.id;
                const live = !inc.resolved_at;
                return (
                  <li
                    key={inc.id}
                    className={cn(
                      "flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-3",
                      live && incidentTone(inc) === "down" && "bg-danger/[0.06]",
                      active && "bg-muted/60",
                    )}
                  >
                    <button
                      type="button"
                      className="min-w-0 flex-1 basis-56 rounded text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                      onClick={() => open(inc.id)}
                      aria-expanded={active}
                    >
                      <span className="block truncate text-sm font-medium">{inc.title}</span>
                      <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                        {inc.monitor_name || "no monitor"}
                        {inc.auto_opened && " · opened automatically"}
                        {" · "}
                        {formatDateTime(inc.opened_at)}
                      </span>
                    </button>
                    <span
                      className={cn(
                        "w-20 text-right font-mono text-sm",
                        live ? "text-foreground" : "text-muted-foreground",
                      )}
                      title={live ? "open for" : "resolved"}
                    >
                      {live ? since(inc.opened_at) : "resolved"}
                    </span>
                    <Badge tone={incidentTone(inc)} className="w-28 justify-center capitalize">
                      {inc.status}
                    </Badge>
                    {writable && live && (
                      <Button size="sm" variant="outline" onClick={() => open(inc.id)}>
                        Post update
                      </Button>
                    )}
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        {selected && (
          <section className="surface self-start p-5" aria-label="Incident detail">
            <div className="flex items-start gap-3">
              <div className="min-w-0 flex-1">
                <h2 className="text-base font-semibold leading-snug">{selected.title}</h2>
                <p className="mt-1 text-xs text-muted-foreground">
                  <span className="capitalize">{selected.impact}</span> impact ·{" "}
                  {selected.monitor_id ? (
                    <Link to={`/monitors/${selected.monitor_id}`} className="text-primary hover:underline">
                      {selected.monitor_name || "monitor"}
                    </Link>
                  ) : (
                    "no monitor"
                  )}{" "}
                  · opened {formatDateTime(selected.opened_at)}
                  {selected.resolved_at && ` · resolved ${formatDateTime(selected.resolved_at)}`}
                </p>
              </div>
              <Button variant="ghost" size="icon" aria-label="Close incident detail" onClick={() => setSelected(null)}>
                <X size={16} />
              </Button>
            </div>
            <IncidentTimeline updates={selected.updates ?? []} className="mt-5" />
            {!selected.resolved_at && writable && (
              <form onSubmit={postUpdate} className="mt-5 flex flex-col gap-3 border-t border-border-soft pt-5">
                <Field label="Status">
                  <Select value={updateStatus} onChange={(e) => setUpdateStatus(e.target.value)}>
                    {STATUSES.map((st) => (
                      <option key={st} value={st}>
                        {st}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label="Update">
                  <Textarea
                    value={updateMessage}
                    onChange={(e) => setUpdateMessage(e.target.value)}
                    required
                    placeholder="We are investigating packet loss at the edge."
                  />
                </Field>
                <div className="flex justify-end">
                  <Button type="submit" disabled={saving}>
                    {saving && <Loader2 size={14} className="animate-spin" />}
                    Post update
                  </Button>
                </div>
              </form>
            )}
          </section>
        )}

        {composing && writable && !selected && (
          <form onSubmit={create} className="surface flex flex-col gap-4 self-start p-5" aria-label="Open an incident">
            <div className="flex items-center justify-between">
              <h2 className="text-base font-semibold">Open an incident</h2>
              <Button type="button" variant="ghost" size="icon" aria-label="Cancel" onClick={() => setComposing(false)}>
                <X size={16} />
              </Button>
            </div>
            <Field label="Title">
              <Input value={title} onChange={(e) => setTitle(e.target.value)} required placeholder="API latency" autoFocus />
            </Field>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field label="Impact">
                <Select value={impact} onChange={(e) => setImpact(e.target.value)}>
                  {IMPACTS.map((v) => (
                    <option key={v} value={v}>
                      {v}
                    </option>
                  ))}
                </Select>
              </Field>
              <Field label="Status">
                <Select value={status} onChange={(e) => setStatus(e.target.value)}>
                  {STATUSES.filter((st) => st !== "resolved").map((st) => (
                    <option key={st} value={st}>
                      {st}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            <Field label="Monitor">
              <Select
                value={monitorID === "" ? "" : String(monitorID)}
                onChange={(e) => setMonitorID(e.target.value === "" ? "" : Number(e.target.value))}
              >
                <option value="">None</option>
                {(monitors ?? []).map((m) => (
                  <option key={m.id} value={m.id}>
                    {m.name}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label="First update">
              <Textarea
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                placeholder="We are investigating."
              />
            </Field>
            <div className="flex justify-end">
              <Button type="submit" disabled={saving}>
                {saving && <Loader2 size={14} className="animate-spin" />}
                Open incident
              </Button>
            </div>
          </form>
        )}
      </div>
    </div>
  );
}
