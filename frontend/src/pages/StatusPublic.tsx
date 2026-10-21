import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useParams } from "react-router-dom";
import { AlertTriangle, CheckCircle2, Moon, Sun, XCircle } from "lucide-react";
import { Button } from "../components/ui/button";
import { Input } from "../components/ui/controls";
import { ApiError, api } from "../lib/api";
import { useTheme } from "../lib/theme";
import type { PublicStatus, PublicStatusIncident, PublicStatusMonitor } from "../lib/types";
import { applyAccent, cn, formatUptime } from "../lib/utils";

const REFRESH_MS = 60_000;

const OVERALL = {
  operational: {
    label: "All systems operational",
    tone: "text-success",
    ring: "border-success/30 bg-success/10",
    Icon: CheckCircle2,
  },
  degraded: {
    label: "Degraded performance",
    tone: "text-warning",
    ring: "border-warning/30 bg-warning/10",
    Icon: AlertTriangle,
  },
  down: {
    label: "Major outage",
    tone: "text-danger",
    ring: "border-danger/30 bg-danger/10",
    Icon: XCircle,
  },
} as const;

/** One consistent timestamp format for everything a visitor reads. */
function stamp(iso: string): string {
  return new Date(iso).toLocaleString(undefined, {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function duration(fromIso: string, toIso: string | null): string {
  const mins = Math.max(
    1,
    Math.round(((toIso ? new Date(toIso).getTime() : Date.now()) - new Date(fromIso).getTime()) / 60000),
  );
  if (mins < 60) return `${mins} min`;
  const hours = Math.round(mins / 60);
  if (hours < 48) return `${hours} hr`;
  return `${Math.round(hours / 24)} days`;
}

export function StatusPublic({ initial }: { initial?: PublicStatus }) {
  const { slug } = useParams();
  const { theme, toggle } = useTheme();
  const [page, setPage] = useState<PublicStatus | null>(initial ?? null);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<Date>(new Date());
  const [email, setEmail] = useState("");
  const [subMsg, setSubMsg] = useState<string | null>(null);

  // A page served on its own domain has no slug in the address; it is the
  // page for this host.
  const byHost = !!initial;
  const load = useCallback(
    async (signal?: { cancelled: boolean }) => {
      if (!byHost && !slug) return;
      try {
        const p = byHost ? await api.status.byHost() : await api.status.public(slug!);
        if (signal?.cancelled) return;
        setPage(p);
        setUpdatedAt(new Date());
        setError(null);
      } catch (err) {
        if (signal?.cancelled) return;
        // Gone is gone. Anything else is one poll that did not get through:
        // keep showing the page already loaded, and try again next time.
        if (err instanceof ApiError && err.status === 404) setError("not found");
        else setError((prev) => prev ?? "failed to load");
      }
    },
    [slug, byHost],
  );

  useEffect(() => {
    const signal = { cancelled: false };
    if (initial) setPage(initial);
    else void load(signal);
    // Keeps going after a failure, so the page comes back by itself.
    const timer = window.setInterval(() => void load(signal), REFRESH_MS);
    return () => {
      signal.cancelled = true;
      clearInterval(timer);
    };
  }, [load, initial]);

  useEffect(() => {
    const root = document.documentElement;
    applyAccent(root, page?.branding?.accent_color);
    return () => applyAccent(root, null);
  }, [page]);

  useEffect(() => {
    document.title = page ? page.name : "Status";
  }, [page]);

  // Visitors read this, not operators: say what they can act on and nothing
  // about how the page is managed.
  if (error === "not found" || (error && !page)) {
    return (
      <div className="flex min-h-screen flex-col items-center justify-center px-6 text-center">
        <h1 className="font-brand text-xl tracking-tight">
          {error === "not found" ? "No status page here" : "This status page is not loading"}
        </h1>
        <p className="mt-2 max-w-md text-sm text-muted-foreground">
          {error === "not found"
            ? "There is no published status page at this address. Check the link you were given."
            : "It will appear here as soon as it can be reached. There is no need to reload."}
        </p>
      </div>
    );
  }

  if (!page) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <span className="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-primary" />
      </div>
    );
  }

  const overall = OVERALL[page.overall] ?? OVERALL.operational;
  const brandName = page.branding?.product_name?.trim();
  const logo = page.branding?.logo_url?.trim();
  const open = page.incidents.filter((i) => i.status !== "resolved");
  const past = page.incidents.filter((i) => i.status === "resolved");

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b border-border bg-card">
        <div className="mx-auto flex h-16 max-w-3xl items-center gap-3 px-4">
          {logo ? <img src={logo} alt="" className="h-8 w-8 object-contain" /> : null}
          <span className="font-brand text-[15px] tracking-tight">{brandName || page.name}</span>
          <Button
            variant="ghost"
            size="icon"
            className="ml-auto"
            aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
            onClick={toggle}
          >
            {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
          </Button>
        </div>
      </header>

      <main className="mx-auto max-w-3xl px-4 py-8">
        <section className={cn("flex items-center gap-3 rounded-[var(--radius)] border p-5", overall.ring)}>
          <overall.Icon size={24} className={cn("shrink-0", overall.tone)} aria-hidden="true" />
          <div className="min-w-0">
            <h1 className={cn("font-brand text-xl tracking-tight", overall.tone)}>{overall.label}</h1>
            <p className="mt-0.5 text-sm text-muted-foreground">
              {page.headline || page.name} · updated {stamp(updatedAt.toISOString())}
              {error ? " · not refreshing, retrying" : ""}
            </p>
          </div>
        </section>

        {(page.maintenance ?? []).length > 0 && (
          <section className="mt-6">
            <h2 className="mb-2 text-sm font-semibold">Scheduled maintenance</h2>
            <div className="flex flex-col gap-3">
              {(page.maintenance ?? []).map((w, i) => (
                <article key={i} className="surface p-5">
                  <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                    <h3 className="text-sm font-semibold">{w.note || "Planned maintenance"}</h3>
                    {w.active ? (
                      <span className="rounded-full border border-warning/30 bg-warning/10 px-2 py-0.5 text-[11px] font-medium text-warning">
                        in progress
                      </span>
                    ) : (
                      <span className="rounded-full border border-border px-2 py-0.5 text-[11px] text-muted-foreground">
                        scheduled
                      </span>
                    )}
                    {w.monitor && <span className="text-xs text-muted-foreground">{w.monitor}</span>}
                  </div>
                  <p className="mt-1 text-xs tabular-nums text-muted-foreground">
                    {stamp(w.starts_at)} → {stamp(w.ends_at)}
                  </p>
                </article>
              ))}
            </div>
          </section>
        )}

        {open.length > 0 && (
          <section className="mt-6">
            <h2 className="mb-2 text-sm font-semibold">Active incidents</h2>
            <div className="flex flex-col gap-3">
              {open.map((inc) => (
                <IncidentCard key={inc.id} incident={inc} />
              ))}
            </div>
          </section>
        )}

        <section className="surface mt-6 overflow-hidden">
          <div className="flex items-baseline justify-between border-b border-border px-5 py-3">
            <h2 className="text-sm font-semibold">Services</h2>
            <span className="text-xs text-muted-foreground">uptime, last 90 days</span>
          </div>
          {page.monitors.length === 0 ? (
            <p className="px-5 py-6 text-sm text-muted-foreground">No services listed.</p>
          ) : (
            <>
              <ul className="divide-y divide-border">
                {page.monitors.map((m) => (
                  <ServiceRow key={m.name} monitor={m} />
                ))}
              </ul>
              {/* Colour alone does not tell a colourblind visitor what a bar means. */}
              <div className="flex flex-wrap items-center gap-4 border-t border-border px-5 py-3 text-[11px] text-muted-foreground">
                {[
                  ["Operational", "var(--chart-up)"],
                  ["Outage", "var(--chart-down)"],
                  ["No data", "var(--muted)"],
                ].map(([label, colour]) => (
                  <span key={label} className="flex items-center gap-1.5">
                    <span
                      className="h-3 w-2 rounded-[2px] border border-border"
                      style={{ background: colour }}
                      aria-hidden="true"
                    />
                    {label}
                  </span>
                ))}
              </div>
            </>
          )}
        </section>

        <section className="mt-8">
          <h2 className="mb-2 text-sm font-semibold">Incident history</h2>
          {past.length === 0 ? (
            <p className="surface px-5 py-6 text-sm text-muted-foreground">
              No incidents resolved in the last 90 days.
            </p>
          ) : (
            <div className="flex flex-col gap-3">
              {past.map((inc) => (
                <IncidentCard key={inc.id} incident={inc} />
              ))}
            </div>
          )}
        </section>

        <section className="surface mt-8 p-5">
          <h2 className="text-sm font-semibold">Get incident updates</h2>
          <p className="mb-3 mt-1 text-sm text-muted-foreground">
            We will email you when an incident opens or is updated. Confirm from your inbox;
            unsubscribe any time.
          </p>
          <form
            className="flex flex-col gap-2 sm:flex-row"
            onSubmit={async (e: FormEvent) => {
              e.preventDefault();
              setSubMsg(null);
              try {
                const res = await api.status.subscribe(page.slug, email);
                setSubMsg(
                  res.status === "already_subscribed"
                    ? "This address is already subscribed."
                    : "Check your inbox to confirm.",
                );
                setEmail("");
              } catch (err) {
                setSubMsg(err instanceof ApiError ? err.message : "subscribe failed");
              }
            }}
          >
            <Input
              type="email"
              required
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              placeholder="you@example.com"
            />
            <Button type="submit">Subscribe</Button>
          </form>
          {subMsg && <p className="mt-2 text-sm text-muted-foreground">{subMsg}</p>}
          <p className="mt-3 text-xs text-muted-foreground">
            Prefer a feed?{" "}
            <a
              href={`/api/status/${page.slug}/feed.xml`}
              className="font-medium text-primary hover:underline"
            >
              Subscribe by RSS
            </a>
            .
          </p>
        </section>

        {!brandName && (
          <p className="mt-10 text-center text-xs text-muted-foreground">
            Status page powered by{" "}
            <a
              href="https://zyphersystems.com/argus"
              className="font-medium text-primary hover:underline"
              target="_blank"
              rel="noreferrer"
            >
              Argus
            </a>
          </p>
        )}
      </main>
    </div>
  );
}

function IncidentCard({ incident }: { incident: PublicStatusIncident }) {
  const resolved = incident.status === "resolved";
  const updates = [...(incident.updates ?? [])].sort(
    (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
  );
  return (
    <article className="surface p-5">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="text-sm font-semibold">{incident.title}</h3>
        <span
          className={cn(
            "rounded-full border px-2 py-0.5 text-[11px] font-medium capitalize",
            resolved
              ? "border-success/30 bg-success/10 text-success"
              : "border-danger/30 bg-danger/10 text-danger",
          )}
        >
          {incident.status}
        </span>
        {incident.monitor && (
          <span className="text-xs text-muted-foreground">{incident.monitor}</span>
        )}
        <span className="ml-auto text-xs tabular-nums text-muted-foreground">
          {stamp(incident.opened_at)}
          {" · "}
          {resolved
            ? `lasted ${duration(incident.opened_at, incident.resolved_at)}`
            : `ongoing for ${duration(incident.opened_at, null)}`}
        </span>
      </div>
      {updates.length > 0 && (
        <ol className="mt-3 border-l border-border pl-4">
          {updates.map((u, i) => (
            <li key={i} className="relative pb-3 last:pb-0 text-sm">
              <span className="absolute -left-[21px] top-1.5 h-2 w-2 rounded-full bg-border" aria-hidden="true" />
              <span className="font-medium capitalize">{u.status}</span>
              {u.message ? <span className="text-muted-foreground"> — {u.message}</span> : null}
              <div className="mt-0.5 text-xs tabular-nums text-muted-foreground">
                {stamp(u.created_at)}
              </div>
            </li>
          ))}
        </ol>
      )}
    </article>
  );
}

function ServiceRow({ monitor }: { monitor: PublicStatusMonitor }) {
  const vis =
    !monitor.slow && monitor.status === "up"
      ? "up"
      : monitor.status === "down"
        ? "down"
        : monitor.slow
          ? "slow"
          : "pending";
  const label =
    vis === "slow" ? "Degraded" : vis === "up" ? "Operational" : vis === "down" ? "Outage" : "Pending";
  const days = monitor.days ?? [];
  // A young page has only a few days. Keep each day a day-sized bar instead of
  // stretching one day across the full width, and say how much history there is.
  const full = days.length >= 85;
  const historyLabel = full
    ? "90 days ago"
    : `${days.length} day${days.length === 1 ? "" : "s"} of history`;

  return (
    <li className="px-5 py-4">
      <div className="flex items-center gap-3">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{monitor.name}</span>
        <span className="text-xs tabular-nums text-muted-foreground">
          {monitor.uptime_90d == null || days.length < 2
            ? "collecting uptime"
            : `${formatUptime(monitor.uptime_90d)} uptime, ${full ? "90 days" : `${days.length} days`}`}
        </span>
        <span
          className={cn(
            "w-24 text-right text-xs font-semibold",
            vis === "up" && "text-success",
            vis === "down" && "text-danger",
            vis === "slow" && "text-warning",
            vis === "pending" && "text-muted-foreground",
          )}
        >
          {label}
        </span>
      </div>
      {days.length > 0 && (
        <>
          <div
            className="mt-2.5 flex h-7 w-full justify-end gap-[2px]"
            role="img"
            aria-label={`${monitor.name}: ${days.filter((d) => d.status === "down").length} of ${days.length} days with an outage`}
          >
            {days.map((d) => (
              <div
                key={d.day}
                className="min-w-[4px] max-w-[10px] flex-1 rounded-[2px]"
                title={`${d.day}: ${d.status || "no data"}`}
                style={{
                  background:
                    d.status === "up"
                      ? "var(--chart-up)"
                      : d.status === "down"
                        ? "var(--chart-down)"
                        : "var(--muted)",
                }}
              />
            ))}
          </div>
          <div className="mt-1.5 flex justify-between text-[11px] text-muted-foreground">
            <span>{historyLabel}</span>
            <span>today</span>
          </div>
        </>
      )}
    </li>
  );
}
