import type { IncidentUpdate } from "../lib/types";
import { formatDateTime } from "../lib/utils";

export function IncidentTimeline({
  updates,
  limit,
  className,
}: {
  updates: IncidentUpdate[];
  /** Show only the latest this many. Updates arrive oldest first. */
  limit?: number;
  className?: string;
}) {
  const shown = limit ? updates.slice(-limit) : updates;
  const earlier = updates.length - shown.length;
  return (
    <ol className={`space-y-4 border-l border-border-soft pl-4 ${className ?? ""}`}>
      {earlier > 0 && (
        <li className="text-xs text-muted-foreground">
          {earlier} earlier update{earlier === 1 ? "" : "s"} on the Incidents page
        </li>
      )}
      {shown.map((u) => (
        <li key={u.id} className="relative text-sm">
          <span
            className="absolute -left-[1.3rem] top-1.5 h-2 w-2 rounded-full bg-muted-foreground/60"
            aria-hidden="true"
          />
          <div className="flex flex-wrap items-baseline gap-x-2">
            <span className="font-medium capitalize">{u.status}</span>
            <span className="font-mono text-xs text-muted-foreground">{formatDateTime(u.created_at)}</span>
          </div>
          {u.message && <p className="mt-0.5 break-words text-muted-foreground">{u.message}</p>}
        </li>
      ))}
      {updates.length === 0 && <li className="text-sm text-muted-foreground">No updates yet.</li>}
    </ol>
  );
}
