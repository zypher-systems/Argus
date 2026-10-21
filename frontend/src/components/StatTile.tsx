import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { cn } from "../lib/utils";

export function StatTile({
  label,
  value,
  unit,
  hint,
  tone,
  hintTone,
  to,
  children,
}: {
  label: string;
  value: string;
  /** Sits after the number, smaller: "ms", "/ 408". */
  unit?: string;
  hint?: string;
  tone?: "danger" | "warning";
  hintTone?: "warning";
  to?: string;
  children: ReactNode;
}) {
  const body = (
    <>
      <div className="text-xs font-medium text-muted-foreground">{label}</div>
      <div
        className={cn(
          "mt-2 font-mono text-[28px] leading-none tracking-tight",
          tone === "danger" && "text-danger",
          tone === "warning" && "text-warning",
        )}
      >
        {value}
        {unit && <span className="ml-1.5 text-[15px] tracking-normal text-muted-foreground">{unit}</span>}
      </div>
      <div className="mt-4">{children}</div>
      {hint && (
        <div className={cn("mt-2 text-xs text-muted-foreground", hintTone === "warning" && "text-warning")}>
          {hint}
        </div>
      )}
    </>
  );
  const cls = "surface block p-4 md:p-5";
  return to ? (
    <Link
      to={to}
      className={cn(
        cls,
        "transition-colors hover:bg-muted/50 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
      )}
    >
      {body}
    </Link>
  ) : (
    <div className={cls}>{body}</div>
  );
}
