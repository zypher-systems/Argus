import UPlot from "uplot";

export function cssColor(varName: string, fallback: string): string {
  if (typeof window === "undefined") return fallback;
  const value = getComputedStyle(document.documentElement)
    .getPropertyValue(varName)
    .trim();
  return value || fallback;
}

export function withAlpha(color: string, alpha: number): string {
  const hex = color.trim();
  if (hex.startsWith("#")) {
    const full =
      hex.length === 4
        ? `#${hex[1]}${hex[1]}${hex[2]}${hex[2]}${hex[3]}${hex[3]}`
        : hex;
    if (full.length >= 7) {
      const r = parseInt(full.slice(1, 3), 16);
      const g = parseInt(full.slice(3, 5), 16);
      const b = parseInt(full.slice(5, 7), 16);
      if ([r, g, b].every((n) => Number.isFinite(n))) {
        return `rgba(${r}, ${g}, ${b}, ${alpha})`;
      }
    }
  }
  const rgb = hex.match(/rgba?\(\s*([\d.]+)\s*,\s*([\d.]+)\s*,\s*([\d.]+)/);
  if (rgb) return `rgba(${rgb[1]}, ${rgb[2]}, ${rgb[3]}, ${alpha})`;
  return hex;
}

export function areaFill(
  color: string,
  topAlpha = 0.42,
  bottomAlpha = 0.02,
): (u: UPlot) => CanvasGradient {
  return (u) => {
    const top = u.bbox.top;
    const bot = u.bbox.top + u.bbox.height;
    const gradient = u.ctx.createLinearGradient(0, top, 0, bot);
    gradient.addColorStop(0, withAlpha(color, topAlpha));
    gradient.addColorStop(1, withAlpha(color, bottomAlpha));
    return gradient;
  };
}

export function splinePaths() {
  return UPlot.paths?.spline?.();
}

export const CHART_UP = "#137241";
export const CHART_DOWN = "#b82e27";
export const CHART_LINE = "#1f5bd8";
export const CHART_SLOW = "#955905";

let measureCtx: CanvasRenderingContext2D | null = null;

/**
 * Sizes a y-axis to its widest label, so "0.40ms" is not clipped the way it
 * was in a fixed 52px. uPlot also reserves the tick length and a gap.
 */
export function axisSizeFor(font: string) {
  return (_u: unknown, values: string[] | null): number => {
    if (!values || values.length === 0) return 52;
    measureCtx ??= document.createElement("canvas").getContext("2d");
    if (!measureCtx) return 52;
    measureCtx.font = font;
    const widest = Math.max(...values.map((v) => measureCtx!.measureText(v).width));
    return Math.max(40, Math.ceil(widest) + 10 + 5 + 6);
  };
}

/**
 * Time-axis labels that never say the same thing twice: seconds on a short
 * window, hours and minutes on a normal one, the date past a day and a half,
 * and a label equal to the one before it is left blank.
 */
/** Which labels a time axis over this span uses: dates, minutes or seconds. */
export function timeAxisKind(spanSecs: number): "date" | "minute" | "second" {
  return spanSecs > 36 * 3600 ? "date" : spanSecs < 6 * 60 ? "second" : "minute";
}

export function timeAxisValues(spanSecs: number) {
  const kind = timeAxisKind(spanSecs);
  const fmt =
    kind === "date"
      ? UPlot.fmtDate("{MMM} {DD}")
      : kind === "second"
        ? UPlot.fmtDate("{HH}:{mm}:{ss}")
        : UPlot.fmtDate("{HH}:{mm}");
  return (_u: unknown, vals: number[]): string[] => {
    let prev = "";
    return vals.map((v) => {
      const label = fmt(new Date(v * 1000));
      if (label === prev) return "";
      prev = label;
      return label;
    });
  };
}
