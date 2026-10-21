import { cn, formatLatency } from "../lib/utils";
import type { StepEvidence } from "../lib/types";

/** How each step of a transaction went, up to the one that failed. */
export function StepList({ steps }: { steps: StepEvidence[] }) {
  return (
    <ol className="flex flex-col gap-1.5">
      {steps.map((st, i) => (
        <li key={i} className="flex flex-wrap items-baseline gap-x-2 text-sm">
          <span
            className={cn("font-medium", st.error ? "text-danger" : "text-success")}
            aria-hidden="true"
          >
            {st.error ? "✕" : "✓"}
          </span>
          <span className="sr-only">{st.error ? "Failed:" : "Passed:"}</span>
          <span className="tabular-nums text-muted-foreground">{i + 1}.</span>
          <span className="font-medium">{st.name}</span>
          <span className="tabular-nums text-xs text-muted-foreground">
            {st.status_code ? `${st.status_code} · ` : ""}
            {formatLatency(st.latency_ms)}
          </span>
          {st.error && <span className="basis-full break-words pl-6 text-xs text-danger">{st.error}</span>}
        </li>
      ))}
    </ol>
  );
}
