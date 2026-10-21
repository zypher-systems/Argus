import { useEffect, useMemo, useState } from "react";
import { Field, Input, Select } from "./ui/controls";
import { api } from "../lib/api";

const EXAMPLES: { expr: string; label: string }[] = [
  { expr: "0 2 * * *", label: "daily at 02:00" },
  { expr: "0 * * * *", label: "hourly" },
  { expr: "30 6 * * 1-5", label: "weekdays at 06:30" },
  { expr: "0 3 * * 0", label: "Sundays at 03:00" },
];

function zones(): string[] {
  const intl = Intl as unknown as { supportedValuesOf?: (key: string) => string[] };
  const list = intl.supportedValuesOf?.("timeZone") ?? [];
  return list.includes("UTC") ? list : ["UTC", ...list];
}

export function browserZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

/**
 * When a heartbeat is expected: every interval, or at cron times. The
 * preview shows the next times as the expression is typed, so a schedule
 * that means something else is caught before it pages anyone.
 */
export function ScheduleFields({
  mode,
  schedule,
  timezone,
  grace,
  onChange,
}: {
  mode: "interval" | "schedule";
  schedule: string;
  timezone: string;
  grace: number;
  onChange: (patch: { push_mode?: "interval" | "schedule"; schedule?: string; timezone?: string; grace_seconds?: number }) => void;
}) {
  const [preview, setPreview] = useState<{ next: string[]; error: string } | null>(null);
  const allZones = useMemo(zones, []);

  useEffect(() => {
    if (mode !== "schedule" || !schedule.trim()) {
      setPreview(null);
      return;
    }
    const t = setTimeout(() => {
      api
        .schedulePreview(schedule, timezone)
        .then(setPreview)
        .catch(() => setPreview(null));
    }, 300);
    return () => clearTimeout(t);
  }, [mode, schedule, timezone]);

  const fmt = (iso: string) =>
    new Intl.DateTimeFormat(undefined, {
      weekday: "short",
      day: "numeric",
      month: "short",
      hour: "2-digit",
      minute: "2-digit",
      timeZone: timezone || "UTC",
      timeZoneName: "short",
    }).format(new Date(iso));

  return (
    <div className="flex flex-col gap-4">
      <fieldset>
        <legend className="mb-2 text-xs font-medium text-muted-foreground">Expect a ping</legend>
        <div className="flex flex-col gap-2 text-sm sm:flex-row sm:gap-6">
          <label className="flex items-center gap-2">
            <input
              type="radio"
              name="push-mode"
              checked={mode === "interval"}
              onChange={() => onChange({ push_mode: "interval" })}
            />
            Every check interval
          </label>
          <label className="flex items-center gap-2">
            <input
              type="radio"
              name="push-mode"
              checked={mode === "schedule"}
              onChange={() =>
                onChange({
                  push_mode: "schedule",
                  schedule: schedule || "0 2 * * *",
                  timezone: timezone || browserZone(),
                  grace_seconds: grace || 300,
                })
              }
            />
            On a schedule
          </label>
        </div>
      </fieldset>

      {mode === "schedule" && (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-[1fr_1fr]">
            <Field label="Schedule (cron)" hint="Minute, hour, day of month, month, day of week.">
              <Input
                value={schedule}
                onChange={(e) => onChange({ schedule: e.target.value })}
                placeholder="0 2 * * *"
                className="font-mono"
                spellCheck={false}
              />
            </Field>
            <Field label="Time zone">
              <Select value={timezone || "UTC"} onChange={(e) => onChange({ timezone: e.target.value })}>
                {allZones.map((z) => (
                  <option key={z} value={z}>
                    {z.replace(/_/g, " ")}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <div className="-mt-2 flex flex-wrap gap-1.5 text-xs">
            {EXAMPLES.map((ex) => (
              <button
                key={ex.expr}
                type="button"
                onClick={() => onChange({ schedule: ex.expr })}
                className="rounded-md border border-border px-2 py-1 text-muted-foreground hover:bg-muted hover:text-foreground"
              >
                {ex.label}
              </button>
            ))}
          </div>
          <div aria-live="polite" className="text-xs">
            {preview?.error ? (
              <p className="text-danger">{preview.error}</p>
            ) : preview && preview.next.length > 0 ? (
              <p className="text-muted-foreground">
                Next: <span className="text-foreground">{preview.next.map(fmt).join(" · ")}</span>
              </p>
            ) : null}
          </div>
          <Field
            label="Grace (minutes)"
            hint="How long the job may run after its start time before a missing ping counts as down."
          >
            <Input
              type="number"
              min={1}
              max={1440}
              value={Math.round(grace / 60) || ""}
              onChange={(e) => onChange({ grace_seconds: Math.max(0, Number(e.target.value)) * 60 })}
              className="w-32"
            />
          </Field>
        </>
      )}
    </div>
  );
}
