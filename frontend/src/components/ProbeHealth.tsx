import { useEffect, useState } from "react";
import { StatusDot } from "./ui/badge";
import { api } from "../lib/api";
import { useMonitorStore } from "../lib/store";
import type { ProbeNode } from "../lib/types";
import { timeAgo } from "../lib/utils";

// A probe node reports at least once a minute; two missed is not reporting.
const NODE_STALE_MS = 120_000;

/** Whether the things doing the checking are themselves alive. */
export function ProbeHealth() {
  const { probeOk } = useMonitorStore();
  const [nodes, setNodes] = useState<ProbeNode[]>([]);

  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api.probeNodes
        .list()
        .then((list) => !cancelled && setNodes(list))
        .catch(() => {
          /* keep the last known nodes; the next poll tries again */
        });
    load();
    const interval = window.setInterval(load, 60_000);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, []);

  return (
    <section className="surface p-4" aria-label="Probe health">
      <h2 className="text-sm font-semibold">Probe health</h2>
      <ul className="mt-3 space-y-2.5 text-sm">
        <li className="flex items-center gap-2">
          <StatusDot status={probeOk === false ? "stale" : probeOk ? "up" : "muted"} className="h-2 w-2" />
          <span className="truncate">This server</span>
          <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
            {probeOk === false ? "not reporting" : probeOk ? "healthy" : "—"}
          </span>
        </li>
        {nodes.map((n) => {
          const fresh = !!n.last_seen && Date.now() - new Date(n.last_seen).getTime() < NODE_STALE_MS;
          return (
            <li key={n.id} className="flex items-center gap-2">
              <StatusDot status={!n.enabled ? "muted" : fresh ? "up" : "stale"} className="h-2 w-2" />
              <span className="truncate">{n.name}</span>
              <span className="ml-auto shrink-0 font-mono text-xs text-muted-foreground">
                {!n.enabled ? "disabled" : n.last_seen ? timeAgo(n.last_seen) : "never seen"}
              </span>
            </li>
          );
        })}
      </ul>
    </section>
  );
}
