import { useEffect, useRef, useState } from "react";
import UPlot from "uplot";
import { areaFill, axisSizeFor, timeAxisKind, timeAxisValues, cssColor, CHART_DOWN, CHART_UP, splinePaths } from "../lib/chart";
import { prefersReducedMotion } from "../lib/motion";
import { useTheme } from "../lib/theme";
import type { RawPoint, ResultsResponse, RollupPoint } from "../lib/types";
import { formatLatency } from "../lib/utils";

interface TooltipState {
  left: number;
  top: number;
  x: string;
  y: string;
  failed: boolean;
}

function seriesFrom(data: ResultsResponse): [number[], (number | null)[], (number | null)[]] {
  const x: number[] = [];
  const y: (number | null)[] = [];
  const failed: (number | null)[] = [];

  if (data.granularity === "raw") {
    for (const p of data.points as {
      ts: string;
      latency_ms: number | null;
      success: boolean;
    }[]) {
      x.push(new Date(p.ts).getTime() / 1000);
      y.push(p.success ? p.latency_ms : null);
      failed.push(p.success ? null : 0);
    }
  } else {
    for (const p of data.points as {
      ts: string;
      avg_latency: number | null;
      failures: number;
    }[]) {
      x.push(new Date(p.ts).getTime() / 1000);
      y.push(p.avg_latency);
      failed.push(p.failures > 0 ? 0 : null);
    }
  }
  return [x, y, failed];
}

export function LatencyChart({
  data,
  animateKey,
  height = 300,
}: {
  data: ResultsResponse | null;
  animateKey?: string;
  /** Plot height in px; a side panel wants less than a full page. */
  height?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const chartRef = useRef<UPlot | null>(null);
  const { theme } = useTheme();
  const [tooltip, setTooltip] = useState<TooltipState | null>(null);
  const [drawIn, setDrawIn] = useState(false);
  const lastAnimate = useRef<string | undefined>(undefined);
  const dataRef = useRef(data);
  dataRef.current = data;
  const hasPoints = Boolean(data?.points?.length);
  // The axis formatter is fixed when the chart is built, so a range that needs
  // other labels (24 h of times, then a week of dates) needs a new chart.
  const pts = data?.points ?? [];
  const axisKind = pts.length
    ? timeAxisKind((new Date(pts[pts.length - 1].ts).getTime() - new Date(pts[0].ts).getTime()) / 1000)
    : "";

  useEffect(() => {
    const el = ref.current;
    const current = dataRef.current;
    if (!el || !current?.points?.length) {
      chartRef.current?.destroy();
      chartRef.current = null;
      setTooltip(null);
      return;
    }

    const [x, y, failed] = seriesFrom(current);
    if (x.length === 0) return;

    const up = cssColor("--chart-up", CHART_UP);
    const down = cssColor("--chart-down", CHART_DOWN);
    const muted = cssColor("--muted-foreground", "#9a9aa6");
    const border = cssColor("--border", "#2a2a32");
    const fmtTime = UPlot.fmtDate("{HH}:{mm}");
    const fmtDate = UPlot.fmtDate("{MMM} {DD}");
    const spline = splinePaths();

    const chart = new UPlot(
      {
        width: el.clientWidth || el.parentElement?.clientWidth || 640,
        height,
        padding: [12, 28, 0, 0], // room for the last time label, centred on the edge
        cursor: {
          drag: { x: false, y: false },
          points: { size: 9, width: 2, fill: up, stroke: up },
        },
        legend: { show: false },
        scales: {
          x: { time: true },
          y: {
            range: (_self, _dataMin, dataMax) => {
              const max = dataMax ?? 0;
              return [0, max > 0 ? max * 1.18 : 1];
            },
          },
        },
        axes: [
          {
            stroke: muted,
            grid: { show: false },
            ticks: { stroke: "transparent" },
            font: "11px ui-sans-serif, system-ui",
            size: 24,
            values: timeAxisValues(x[x.length - 1] - x[0]),
          },
          {
            stroke: muted,
            grid: { stroke: border, width: 1 },
            ticks: { stroke: "transparent" },
            font: "11px ui-sans-serif, system-ui",
            size: axisSizeFor("11px ui-sans-serif, system-ui"),
            values: (_u, vals) =>
              vals.map((v) => {
                if (v === 0) return "0";
                if (v < 1) return `${v.toFixed(2)}ms`;
                if (v < 10) return `${v.toFixed(1)}ms`;
                return `${Math.round(v)}ms`;
              }),
          },
        ],
        series: [
          {},
          {
            label: "latency",
            stroke: up,
            width: 2,
            fill: areaFill(up, 0.4, 0.02),
            paths: spline,
            points: { show: false },
            spanGaps: false,
          },
          {
            label: "failed",
            stroke: "transparent",
            width: 0,
            points: {
              show: true,
              size: 5,
              fill: down,
              stroke: down,
            },
          },
        ],
        hooks: {
          setCursor: [
            (u) => {
              const idx = u.cursor.idx;
              if (idx == null || u.cursor.left == null || u.cursor.top == null) {
                setTooltip(null);
                return;
              }
              const ts = new Date(u.data[0][idx] * 1000);
              const failedAt = u.data[2][idx] != null;
              setTooltip({
                left: u.cursor.left,
                top: u.cursor.top,
                x: `${fmtDate(ts)} ${fmtTime(ts)}`,
                y: formatLatency(u.data[1][idx] as number | null),
                failed: failedAt,
              });
            },
          ],
        },
      },
      [x, y, failed],
      el,
    );
    chartRef.current = chart;

    const ro = new ResizeObserver(() => {
      if (!ref.current || !chartRef.current) return;
      chartRef.current.setSize({ width: ref.current.clientWidth, height });
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.destroy();
      chartRef.current = null;
    };
  }, [theme, hasPoints, height, axisKind]);

  useEffect(() => {
    if (!chartRef.current || !data?.points?.length) return;
    const [x, y, failed] = seriesFrom(data);
    if (x.length === 0) return;
    chartRef.current.setData([x, y, failed]);
  }, [data]);

  useEffect(() => {
    if (!data?.points?.length) {
      setDrawIn(false);
      lastAnimate.current = animateKey;
      return;
    }
    if (prefersReducedMotion()) {
      setDrawIn(false);
      lastAnimate.current = animateKey;
      return;
    }
    if (lastAnimate.current === animateKey && lastAnimate.current !== undefined) {
      return;
    }
    lastAnimate.current = animateKey;
    setDrawIn(false);
    const id = requestAnimationFrame(() => setDrawIn(true));
    const timeout = window.setTimeout(() => setDrawIn(false), 500);
    return () => {
      cancelAnimationFrame(id);
      clearTimeout(timeout);
    };
  }, [animateKey, data]);

  if (!data?.points?.length) {
    return (
      <div className="flex items-center justify-center text-sm text-muted-foreground" style={{ height }}>
        No data for this range yet.
      </div>
    );
  }

  // Every check in the window failed: an empty axis with markers on the floor
  // reads as a broken chart, so say what happened instead.
  // Ranges past a day come as hourly or daily rollups, which carry counts and
  // an average instead of a success flag per check.
  const anySuccess =
    data.granularity === "raw"
      ? (data.points as RawPoint[]).some((p) => p.success !== false && p.latency_ms != null)
      : (data.points as RollupPoint[]).some((p) => p.avg_latency != null && p.failures < p.total);
  if (!anySuccess) {
    return (
      <div className="flex flex-col items-center justify-center gap-1 text-sm text-muted-foreground" style={{ height }}>
        <span className="font-medium text-danger">No successful checks in this range.</span>
        <span className="text-xs">Every check failed, so there is no latency to plot.</span>
      </div>
    );
  }

  const flip = tooltip && tooltip.left > (ref.current?.clientWidth ?? 640) * 0.62;

  return (
    <div className="relative">
      <div ref={ref} className={drawIn ? "chart-draw-in" : ""} style={{ height }} />
      {tooltip && (
        <div
          className="pointer-events-none absolute z-10 rounded-md bg-background/90 px-2.5 py-1.5 text-xs shadow-none ring-1 ring-border backdrop-blur"
          style={{
            left: tooltip.left,
            top: Math.max(4, tooltip.top - 36),
            transform: flip ? "translateX(-108%)" : "translateX(12px)",
          }}
        >
          <span className="tabular-nums text-muted-foreground">{tooltip.x}</span>
          <span className="mx-2 text-border">·</span>
          <span className="font-medium tabular-nums">
            {tooltip.y}
            {tooltip.failed ? " (failed)" : ""}
          </span>
        </div>
      )}
    </div>
  );
}
