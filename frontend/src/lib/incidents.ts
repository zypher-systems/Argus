import { useEffect, useState } from "react";
import { api } from "./api";
import type { Incident, Monitor } from "./types";
import { visualStatus } from "./utils";

// An incident can be opened, updated or resolved by hand at any time, which
// nothing in the monitor list reveals, so both hooks also poll.
const POLL_MS = 30_000;

/**
 * Which monitors are down, as a string that changes whenever that set does.
 * A count would not do: one recovering as another fails leaves it unchanged.
 */
export function downKey(monitors: Monitor[] | null): string {
  return (monitors ?? [])
    .filter((m) => visualStatus(m) === "down")
    .map((m) => m.id)
    .sort((a, b) => a - b)
    .join(",");
}

/**
 * The open incidents, longest-running first; null until the first read succeeds. Outages open incidents on their
 * own, so the list is re-read when `key` (see downKey) changes.
 */
export function useOpenIncidents(key: string): Incident[] | null {
  const [list, setList] = useState<Incident[] | null>(null);
  useEffect(() => {
    let cancelled = false;
    const load = () =>
      api.incidents
        .list(true)
        .then((rows) => {
          if (cancelled) return;
          setList([...rows].sort((a, b) => new Date(a.opened_at).getTime() - new Date(b.opened_at).getTime()));
        })
        .catch(() => {
          // A failed read is not "no incidents": keep what was last known,
          // or stay unloaded until the next poll succeeds.
        });
    load();
    const interval = window.setInterval(load, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [key]);
  return list;
}

/** One incident with its updates; re-read on a timer and when `key` changes. */
export function useIncident(id: number | null, key: string | number = ""): Incident | null {
  const [incident, setIncident] = useState<Incident | null>(null);
  useEffect(() => {
    if (id == null) {
      setIncident(null);
      return;
    }
    let cancelled = false;
    const load = () =>
      api.incidents
        .get(id)
        .then((inc) => !cancelled && setIncident(inc))
        .catch(() => {
          /* keep the last good read; the next poll tries again */
        });
    load();
    const interval = window.setInterval(load, POLL_MS);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [id, key]);
  return incident?.id === id ? incident : null;
}

export function incidentTone(inc: { status: string; resolved_at: string | null }) {
  if (inc.resolved_at || inc.status === "resolved") return "muted" as const;
  if (inc.status === "monitoring") return "primary" as const;
  if (inc.status === "identified") return "warning" as const;
  return "down" as const;
}
