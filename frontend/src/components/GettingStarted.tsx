import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { Check, Loader2, Upload } from "lucide-react";
import { Button } from "./ui/button";
import { Select, Textarea } from "./ui/controls";
import { api, ApiError } from "../lib/api";
import { useAuth } from "../lib/auth";
import { useSettings } from "../lib/settings";
import { useMonitorStore } from "../lib/store";
import { useToast } from "../lib/toast";
import { cn } from "../lib/utils";
import type { NotificationChannel, Onboarding, QuickAddResult, StatusPage } from "../lib/types";

type StepKey = "watch" | "alerts" | "test" | "page";

// The first paste swaps the welcome page for the dashboard, which mounts a
// new checklist; this carries what was skipped across so the reasons stay
// on screen.
let lastQuickAdd: QuickAddResult | null = null;

// Hiding or showing the checklist in one place updates every copy on the page.
const CHANGED = "argus:onboarding-changed";

function Step({
  n,
  title,
  done,
  open,
  summary,
  children,
}: {
  n: number;
  title: string;
  done: boolean;
  open: boolean;
  summary?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <li className="flex gap-3 border-t border-border py-4 first:border-t-0">
      <span
        aria-hidden="true"
        className={cn(
          "mt-0.5 flex h-6 w-6 shrink-0 items-center justify-center rounded-full text-xs font-semibold",
          done ? "bg-success/15 text-success" : open ? "bg-primary text-primary-foreground" : "bg-muted text-muted-foreground",
        )}
      >
        {done ? <Check size={13} strokeWidth={3} /> : n}
      </span>
      <div className="min-w-0 flex-1">
        <h3 className={cn("text-sm font-medium", done && "text-muted-foreground")}>
          {title}
          <span className="sr-only">{done ? " (done)" : ""}</span>
        </h3>
        {done && summary && <div className="mt-0.5 text-xs text-muted-foreground">{summary}</div>}
        {open && <div className="mt-3">{children}</div>}
      </div>
    </li>
  );
}

/**
 * The getting-started checklist. Each step is done because the thing
 * exists, so it stays right however the work got done.
 */
export function GettingStarted({
  welcome = false,
  variant = "card",
}: {
  welcome?: boolean;
  /** "restore" renders only the way back to a hidden, unfinished checklist. */
  variant?: "card" | "restore";
}) {
  const { user } = useAuth();
  const { reload } = useSettings();
  const { refresh } = useMonitorStore();
  const { toast } = useToast();
  const [state, setState] = useState<Onboarding | null>(null);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState<StepKey | "import" | null>(null);
  const [added, setAddedState] = useState<QuickAddResult | null>(lastQuickAdd);
  const setAdded = (r: QuickAddResult | null) => {
    lastQuickAdd = r;
    setAddedState(r);
  };
  const [channelId, setChannelId] = useState<number | null>(null);
  const [testError, setTestError] = useState<string | null>(null);
  const [page, setPage] = useState<StatusPage | null>(null);
  const fileInput = useRef<HTMLInputElement>(null);

  const load = useCallback(async () => {
    try {
      const [o, ch] = await Promise.all([api.onboarding.get(), api.channels.list().catch(() => [])]);
      setState(o);
      setChannels(ch);
      setChannelId((cur) => cur ?? ch[0]?.id ?? null);
    } catch {
      setState(null);
    }
  }, []);

  useEffect(() => {
    load();
    window.addEventListener(CHANGED, load);
    return () => window.removeEventListener(CHANGED, load);
  }, [load]);

  if (!state) return null;

  const done: Record<StepKey, boolean> = {
    watch: state.monitors > 0,
    alerts: state.channels > 0,
    test: !!state.test_alert_at,
    page: state.status_pages > 0,
  };
  const order: StepKey[] = ["watch", "alerts", "test", "page"];
  const count = order.filter((k) => done[k]).length;
  const current = order.find((k) => !done[k]) ?? null;
  if (variant === "restore") {
    if (welcome || current === null || !state.dismissed_at) return null;
    return (
      <div className="mt-8 flex justify-center">
        <Button type="button" variant="ghost" size="sm" onClick={() => setHidden(false)}>
          Show getting started ({count} of 4 done)
        </Button>
      </div>
    );
  }
  if (!welcome && (current === null || state.dismissed_at)) return null;

  const fail = (err: unknown, fallback: string) =>
    toast("error", err instanceof ApiError ? err.message : fallback);

  const watch = async () => {
    setBusy("watch");
    try {
      const res = await api.monitors.quick(text);
      setAdded(res);
      if (res.created.length) {
        setText(res.skipped.map((s) => s.input).join("\n"));
        await refresh();
        await load();
      }
    } catch (err) {
      fail(err, "could not add those");
    } finally {
      setBusy(null);
    }
  };

  const importKuma = async (file: File) => {
    setBusy("import");
    try {
      const data = JSON.parse(await file.text()) as { monitorList?: unknown };
      const format = Array.isArray(data.monitorList) ? "kuma" : "argus";
      const res = await api.import({ format, data });
      toast("success", `imported ${res.imported} monitor${res.imported === 1 ? "" : "s"}`);
      await refresh();
      await load();
    } catch (err) {
      fail(err, "that file could not be imported");
    } finally {
      setBusy(null);
    }
  };

  const emailMe = async () => {
    if (!user?.email) return;
    setBusy("alerts");
    try {
      await api.channels.create({ name: "Email me", type: "email", dest: user.email });
      await reload();
      await load();
    } catch (err) {
      fail(err, "could not add the email channel");
    } finally {
      setBusy(null);
    }
  };

  const sendTest = async () => {
    if (channelId == null) return;
    setBusy("test");
    setTestError(null);
    try {
      await api.channels.test(channelId);
      toast("success", "test alert sent");
      await load();
    } catch (err) {
      setTestError(err instanceof ApiError ? err.message : "the test did not go through");
    } finally {
      setBusy(null);
    }
  };

  const publish = async () => {
    setBusy("page");
    try {
      setPage(await api.onboarding.statusPage());
      await load();
    } catch (err) {
      fail(err, "could not create the status page");
    } finally {
      setBusy(null);
    }
  };

  async function setHidden(hidden: boolean) {
    try {
      setState(await api.onboarding.setDismissed(hidden));
      window.dispatchEvent(new Event(CHANGED));
    } catch (err) {
      toast("error", err instanceof ApiError ? err.message : "could not change that");
    }
  }
  const hide = () => setHidden(true);

  return (
    <section className="surface mb-8 p-5" aria-labelledby="getting-started">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 id="getting-started" className={welcome ? "font-brand text-xl tracking-tight" : "text-sm font-semibold"}>
            {welcome ? "Welcome to Argus" : "Get started"}
          </h2>
          <p className="mt-1 text-sm text-muted-foreground">
            {welcome
              ? "Four steps from nothing watched to being paged when something breaks."
              : `${count} of 4 done.`}
          </p>
        </div>
        {!welcome && (
          <Button type="button" variant="ghost" size="sm" onClick={hide}>
            Hide
          </Button>
        )}
      </div>
      <div
        className="mt-3 h-1 overflow-hidden rounded-full bg-muted"
        role="progressbar"
        aria-label="Getting started"
        aria-valuemin={0}
        aria-valuemax={4}
        aria-valuenow={count}
      >
        <div className="h-full bg-primary transition-all" style={{ width: `${(count / 4) * 100}%` }} />
      </div>

      <ol className="mt-2">
        <Step
          n={1}
          title="Watch something"
          done={done.watch}
          open={current === "watch"}
          summary={
            <>
              {state.monitors} monitor{state.monitors === 1 ? "" : "s"}
              {added && added.skipped.length > 0 && (
                <ul className="mt-1 flex flex-col gap-0.5 text-warning">
                  {added.skipped.map((s) => (
                    <li key={s.line}>
                      Not added: line {s.line}, {s.input}: {s.reason}
                    </li>
                  ))}
                </ul>
              )}
            </>
          }
        >
          <label htmlFor="quick-add" className="text-xs text-muted-foreground">
            Paste URLs or hosts, one per line. A URL becomes an HTTP check, host:port a TCP check,
            and anything else is pinged.
          </label>
          <Textarea
            id="quick-add"
            value={text}
            onChange={(e) => setText(e.target.value)}
            placeholder={"https://shop.example.com\ndb.internal:5432\n10.0.0.1"}
            className="mt-1.5 min-h-24 font-mono text-xs"
          />
          {added && added.skipped.length > 0 && (
            <ul className="mt-2 flex flex-col gap-0.5 text-xs text-warning">
              {added.skipped.map((s) => (
                <li key={s.line}>
                  Line {s.line}, {s.input}: {s.reason}
                </li>
              ))}
            </ul>
          )}
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button type="button" size="sm" onClick={watch} disabled={!text.trim() || busy !== null}>
              {busy === "watch" && <Loader2 size={14} className="animate-spin" />}
              Start watching
            </Button>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={() => fileInput.current?.click()}
              disabled={busy !== null}
            >
              {busy === "import" ? <Loader2 size={14} className="animate-spin" /> : <Upload size={14} />}
              Import from Uptime Kuma
            </Button>
            <input
              ref={fileInput}
              type="file"
              accept="application/json"
              className="hidden"
              tabIndex={-1}
              aria-hidden="true"
              onChange={(e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (f) importKuma(f);
              }}
            />
            <Link to="/monitors/new" className="text-xs text-primary hover:underline">
              Or use the full form
            </Link>
          </div>
        </Step>

        <Step
          n={2}
          title="Choose where alerts go"
          done={done.alerts}
          open={current === "alerts"}
          summary={`${state.channels} channel${state.channels === 1 ? "" : "s"}`}
        >
          <p className="text-xs text-muted-foreground">
            Argus pages you through a channel: email, Slack, Discord, Teams, PagerDuty, Opsgenie,
            Telegram, ntfy, or a webhook.
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            {state.smtp_ready && user?.email ? (
              <Button type="button" size="sm" onClick={emailMe} disabled={busy !== null}>
                {busy === "alerts" && <Loader2 size={14} className="animate-spin" />}
                Email me at {user.email}
              </Button>
            ) : null}
            <Link
              to="/settings/alerts"
              className={cn(
                "inline-flex h-8 items-center rounded-lg px-3 text-xs font-medium",
                state.smtp_ready ? "border border-border hover:bg-muted" : "bg-primary text-primary-foreground hover:brightness-110",
              )}
            >
              Add a channel
            </Link>
          </div>
          {!state.smtp_ready && (
            <p className="mt-2 text-xs text-muted-foreground">
              Email needs a mail server first, which is also under Settings → Alerts.
            </p>
          )}
        </Step>

        <Step
          n={3}
          title="Send a test alert"
          done={done.test}
          open={current === "test"}
          summary="Delivered"
        >
          <p className="text-xs text-muted-foreground">
            So the first alert you get is not the first one that could have failed.
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            {channels.length > 1 && (
              <Select
                aria-label="Channel to test"
                value={channelId ?? ""}
                onChange={(e) => setChannelId(Number(e.target.value))}
                className="h-8 w-56 text-xs"
              >
                {channels.map((c) => (
                  <option key={c.id} value={c.id}>
                    {c.name} ({c.type})
                  </option>
                ))}
              </Select>
            )}
            <Button type="button" size="sm" onClick={sendTest} disabled={busy !== null || channelId == null}>
              {busy === "test" && <Loader2 size={14} className="animate-spin" />}
              Send a test{channels.length === 1 ? ` to ${channels[0].name}` : ""}
            </Button>
          </div>
          {testError && (
            <p role="alert" className="mt-2 text-xs text-danger">
              {testError}. Check the channel under{" "}
              <Link to="/settings/alerts" className="underline">
                Settings → Alerts
              </Link>
              .
            </p>
          )}
        </Step>

        <Step
          n={4}
          title="Publish a status page"
          done={done.page}
          open={current === "page"}
          summary={
            page ? (
              <>
                Published at{" "}
                <a href={`/status/${page.slug}`} target="_blank" rel="noreferrer" className="text-primary hover:underline">
                  /status/{page.slug}
                </a>
              </>
            ) : (
              <Link to="/status-pages" className="hover:underline">
                Manage status pages
              </Link>
            )
          }
        >
          <p className="text-xs text-muted-foreground">
            A public page with every monitor on it, names only; targets stay private. You can
            change what it shows afterwards.
          </p>
          <div className="mt-3">
            <Button type="button" size="sm" onClick={publish} disabled={busy !== null || state.monitors === 0}>
              {busy === "page" && <Loader2 size={14} className="animate-spin" />}
              Publish a status page
            </Button>
          </div>
        </Step>
      </ol>
    </section>
  );
}
