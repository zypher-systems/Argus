import { useEffect, useRef, useState } from "react";
import UPlot from "uplot";
import { areaFill, axisSizeFor, timeAxisValues, cssColor, CHART_LINE, splinePaths } from "../lib/chart";
import { prefersReducedMotion } from "../lib/motion";
import { useTheme } from "../lib/theme";
import { formatLatency } from "../lib/utils";

const HEIGHT = 220;

interface TooltipState {
  left: number;
  top: number;
  x: string;
  y: string;
}

export function FleetChart({ x, y }: { x: number[]; y: (number | null)[] }) {
  const ref = useRef<HTMLDivElement>(null);
  const chartRef = useRef<UPlot | null>(null);
  const { theme } = useTheme();
  const [tooltip, setTooltip] = useState<TooltipState | null>(null);
  const [drawIn, setDrawIn] = useState(false);
  // One bucket has no line to draw and no span to label.
  const hasPoints = x.length > 1;
  const dataRef = useRef({ x, y });
  dataRef.current = { x, y };

  useEffect(() => {
    const el = ref.current;
    const current = dataRef.current;
    if (!el || current.x.length < 2) {
      chartRef.current?.destroy();
      chartRef.current = null;
      setTooltip(null);
      return;
    }

    const up = cssColor("--chart-line", CHART_LINE);
    const muted = cssColor("--muted-foreground", "#9a9aa6");
    const border = cssColor("--chart-grid", "#e6eaf0");
    const fmtTime = UPlot.fmtDate("{HH}:{mm}");
    const fmtDay = UPlot.fmtDate("{MMM} {DD}");
    const fmtDate = fmtDay;
    // Pick a tick format that does not repeat the same label: seconds on a
    // short window, the date once the window is longer than a day and a half.
    const spanSecs = current.x[current.x.length - 1] - current.x[0];
    const spline = splinePaths();

    const chart = new UPlot(
      {
        width: el.clientWidth || el.parentElement?.clientWidth || 640,
        height: HEIGHT,
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
            font: "11px Inter Variable, ui-sans-serif, system-ui",
            size: 24,
            space: 84,
            values: timeAxisValues(spanSecs),
          },
          {
            stroke: muted,
            grid: { stroke: border, width: 1 },
            ticks: { stroke: "transparent" },
            font: "11px Inter Variable, ui-sans-serif, system-ui",
            size: axisSizeFor("11px Inter Variable, ui-sans-serif, system-ui"),
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
            stroke: up,
            width: 2,
            fill: areaFill(up, 0.42, 0.02),
            paths: spline,
            points: { show: false },
            spanGaps: true,
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
              setTooltip({
                left: u.cursor.left,
                top: u.cursor.top,
                x: `${fmtDate(ts)} ${fmtTime(ts)}`,
                y: formatLatency(u.data[1][idx] as number | null),
              });
            },
          ],
        },
      },
      [current.x, current.y],
      el,
    );
    chartRef.current = chart;

    const ro = new ResizeObserver(() => {
      if (!ref.current || !chartRef.current) return;
      chartRef.current.setSize({ width: ref.current.clientWidth, height: HEIGHT });
    });
    ro.observe(el);

    return () => {
      ro.disconnect();
      chart.destroy();
      chartRef.current = null;
    };
  }, [theme, hasPoints]);

  useEffect(() => {
    if (!chartRef.current || x.length === 0) return;
    chartRef.current.setData([x, y]);
  }, [x, y]);

  useEffect(() => {
    if (!hasPoints || prefersReducedMotion()) {
      setDrawIn(false);
      return;
    }
    setDrawIn(false);
    const id = requestAnimationFrame(() => setDrawIn(true));
    const timeout = window.setTimeout(() => setDrawIn(false), 500);
    return () => {
      cancelAnimationFrame(id);
      clearTimeout(timeout);
    };
  }, [hasPoints]);

  if (!hasPoints) {
    return (
      <div className="flex h-[220px] items-center justify-center text-sm text-muted-foreground">
        Fleet latency appears after a few minutes of checks.
      </div>
    );
  }

  const flip = tooltip && tooltip.left > (ref.current?.clientWidth ?? 640) * 0.62;

  return (
    <div className="relative">
      <div ref={ref} className={`h-[220px] ${drawIn ? "chart-draw-in" : ""}`} />
      {tooltip && (
        <div
          className="pointer-events-none absolute z-10 bg-background/90 px-2.5 py-1.5 text-xs ring-1 ring-border backdrop-blur"
          style={{
            left: tooltip.left,
            top: Math.max(4, tooltip.top - 36),
            transform: flip ? "translateX(-108%)" : "translateX(12px)",
          }}
        >
          <span className="tabular-nums text-muted-foreground">{tooltip.x}</span>
          <span className="mx-2 text-border">·</span>
          <span className="font-medium tabular-nums">{tooltip.y} avg</span>
        </div>
      )}
    </div>
  );
}
