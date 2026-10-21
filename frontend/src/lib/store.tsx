import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { api } from "./api";
import type { Monitor, SeriesPoint, WSMessage } from "./types";

interface MonitorStoreCtx {
  monitors: Monitor[] | null;
  series: Record<number, SeriesPoint[]>;
  connected: boolean;
  probeOk: boolean | null;
  refresh: () => Promise<void>;
}

const Ctx = createContext<MonitorStoreCtx>({
  monitors: null,
  series: {},
  connected: false,
  probeOk: null,
  refresh: async () => {},
});

export function MonitorStoreProvider({ children }: { children: ReactNode }) {
  const [monitors, setMonitors] = useState<Monitor[] | null>(null);
  const [series, setSeries] = useState<Record<number, SeriesPoint[]>>({});
  const [connected, setConnected] = useState(false);
  const [probeOk, setProbeOk] = useState<boolean | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const retryRef = useRef(0);
  const timerRef = useRef<number | null>(null);

  const applyResult = useCallback((msg: WSMessage) => {
    setMonitors((prev) => {
      if (!prev) return prev;
      return prev.map((m) => {
        if (m.id !== msg.monitor_id) return m;
        return {
          ...m,
          state: {
            ...m.state,
            status: msg.status,
            last_latency_ms: msg.latency_ms,
            last_check_at: msg.ts,
            consecutive_failures:
              msg.success ? 0 : (m.state?.consecutive_failures ?? 0) + 1,
            slow: msg.slow ?? false,
            slow_since: msg.slow
              ? (m.state?.slow_since ?? msg.ts)
              : null,
            tls_expires_at: msg.tls_expires_at ?? m.state?.tls_expires_at ?? null,
            stale: false,
          },
        };
      });
    });
    setSeries((prev) => {
      const cur = prev[msg.monitor_id] ?? [];
      const next = [
        ...cur,
        {
          monitor_id: msg.monitor_id,
          ts: msg.ts,
          avg_latency: msg.latency_ms,
          max_latency: msg.latency_ms,
          failures: msg.success ? 0 : 1,
          total: 1,
        },
      ].slice(-80);
      const ok = next.filter((p) => p.failures === 0).length;
      const uptime = next.length ? (100 * ok) / next.length : null;
      setMonitors((list) => {
        if (!list) return list;
        return list.map((m) =>
          m.id === msg.monitor_id
            ? { ...m, state: { ...m.state, uptime_24h: uptime } }
            : m,
        );
      });
      return { ...prev, [msg.monitor_id]: next };
    });
  }, []);

  const refresh = useCallback(async () => {
    try {
      const [list, s, health] = await Promise.all([
        api.monitors.list(),
        api.monitors.series().catch(() => ({ granularity: "hour", points: [] as SeriesPoint[] })),
        fetch("/api/health", { credentials: "include" })
          .then((r) => r.json())
          .catch(() => null),
      ]);
      setMonitors(list);
      const byMonitor: Record<number, SeriesPoint[]> = {};
      for (const p of s.points) {
        (byMonitor[p.monitor_id] ??= []).push(p);
      }
      setSeries(byMonitor);
      if (health && typeof health.probe?.ok === "boolean") {
        setProbeOk(health.probe.ok);
      }
    } catch {
      /* keep the last good list — wiping it blanks the whole app */
    }
  }, []);

  useEffect(() => {
    let closed = false;
    let ws: WebSocket | null = null;
    // A reconnect that takes a second is not worth announcing; missed
    // results are caught up on reconnect. Show "connecting" only if it lasts.
    let offlineTimer: number | undefined;
    const markOffline = () => {
      if (offlineTimer !== undefined) return;
      offlineTimer = window.setTimeout(() => setConnected(false), 3000);
    };
    const markOnline = () => {
      if (offlineTimer !== undefined) {
        clearTimeout(offlineTimer);
        offlineTimer = undefined;
      }
      setConnected(true);
    };

    const connect = () => {
      if (closed) return;
      const proto = location.protocol === "https:" ? "wss:" : "ws:";
      ws = new WebSocket(`${proto}//${location.host}/api/ws`);
      wsRef.current = ws;

      ws.onopen = () => {
        retryRef.current = 0;
        markOnline();
        void refresh();
      };
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data) as WSMessage;
          if (msg.type === "result") applyResult(msg);
        } catch {
          /* ignore malformed */
        }
      };
      ws.onclose = () => {
        if (closed) return;
        markOffline();
        const delay = Math.min(1000 * 2 ** retryRef.current, 15000);
        retryRef.current += 1;
        timerRef.current = window.setTimeout(connect, delay);
      };
      ws.onerror = () => {
        ws?.close();
      };
    };

    connect();
    refresh();
    const interval = window.setInterval(refresh, 60_000);

    return () => {
      closed = true;
      if (timerRef.current) clearTimeout(timerRef.current);
      if (offlineTimer !== undefined) clearTimeout(offlineTimer);
      clearInterval(interval);
      ws?.close();
      setConnected(false);
    };
  }, [applyResult, refresh]);

  const value = useMemo(
    () => ({ monitors, series, connected, probeOk, refresh }),
    [monitors, series, connected, probeOk, refresh],
  );

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useMonitorStore() {
  return useContext(Ctx);
}
