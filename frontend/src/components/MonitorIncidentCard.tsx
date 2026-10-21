import { useState } from "react";
import { Link } from "react-router-dom";
import { Bell, BellOff } from "lucide-react";
import { IncidentTimeline } from "./IncidentTimeline";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { api, ApiError } from "../lib/api";
import { incidentTone, useIncident } from "../lib/incidents";
import { useMonitorStore } from "../lib/store";
import { useToast } from "../lib/toast";
import type { Incident, Monitor } from "../lib/types";
import { formatDateTime, isAcked, isMuted, visualStatus } from "../lib/utils";

export function MonitorIncidentCard({
  monitor,
  incident,
  loading,
  writable,
}: {
  monitor: Monitor;
  /** This monitor's open incident, from the open list; none when it is healthy. */
  incident: Incident | undefined;
  /** The open list has not loaded yet, so no incident does not mean none is open. */
  loading: boolean;
  writable: boolean;
}) {
  const { refresh } = useMonitorStore();
  const { toast } = useToast();
  const [busy, setBusy] = useState(false);
  const detail = useIncident(incident?.id ?? null, incident?.status ?? "");
  const down = visualStatus(monitor) === "down";
  const acked = isAcked(monitor.state);
  const muted = isMuted(monitor.state);

  const act = async (what: "ack" | "mute" | "unmute") => {
    setBusy(true);
    try {
      if (what === "ack") await api.monitors.ack(monitor.id);
      else await api.monitors.mute(monitor.id, what === "mute" ? 60 : 0);
      await refresh();
      toast(
        "success",
        what === "ack"
          ? `Acknowledged ${monitor.name}`
          : what === "mute"
            ? `Muted ${monitor.name} for an hour`
            : `Unmuted ${monitor.name}`,
      );
    } catch (e) {
      toast("error", e instanceof ApiError ? e.message : `Could not update ${monitor.name}`);
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="surface p-4" aria-label="Incident">
      {incident ? (
        <>
          <div className="flex items-baseline justify-between gap-2">
            <h2 className="text-sm font-semibold">Active incident</h2>
            <span className="font-mono text-xs text-muted-foreground">#{incident.id}</span>
          </div>
          <Badge tone={incidentTone(incident)} className="mt-3 capitalize">
            {incident.status}
          </Badge>
          <p className="mt-3 break-words text-sm font-semibold leading-snug">{incident.title}</p>
          <p className="mt-1 text-xs text-muted-foreground">
            {incident.auto_opened ? "Opened automatically" : "Opened"} {formatDateTime(incident.opened_at)}
          </p>
          {detail && <IncidentTimeline updates={detail.updates ?? []} limit={4} className="mt-4" />}
        </>
      ) : loading ? (
        <>
          <h2 className="text-sm font-semibold">Incident</h2>
          <p className="mt-2 text-xs text-muted-foreground">Loading…</p>
        </>
      ) : (
        <>
          <h2 className="text-sm font-semibold">No open incident</h2>
          <p className="mt-2 text-xs text-muted-foreground">
            {visualStatus(monitor) === "slow"
              ? "A slow monitor does not open an incident. One opens when it goes down."
              : "Nothing is open for this monitor."}
          </p>
        </>
      )}

      <div className="mt-4 flex flex-col gap-2">
        {down && acked && <Badge tone="muted" className="justify-center py-1.5">acknowledged</Badge>}
        {writable && down && !acked && (
          <Button disabled={busy} onClick={() => act("ack")}>
            Acknowledge
          </Button>
        )}
        {writable &&
          (muted ? (
            <Button variant="outline" disabled={busy} onClick={() => act("unmute")}>
              <Bell size={14} />
              Unmute
            </Button>
          ) : (
            <Button variant="outline" disabled={busy} onClick={() => act("mute")}>
              <BellOff size={14} />
              Mute for 1 hour
            </Button>
          ))}
        <Link
          to="/incidents"
          className="mt-1 self-start rounded text-xs font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          {incident ? "Post an update" : "Incident history"}
        </Link>
      </div>
      {writable && down && !acked && (
        <p className="mt-3 text-xs text-muted-foreground">Acknowledging keeps the incident open.</p>
      )}
    </section>
  );
}
