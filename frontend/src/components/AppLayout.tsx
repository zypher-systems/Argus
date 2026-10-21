import {
  Activity,
  BarChart3,
  Globe,
  Layers,
  LayoutDashboard,
  LogOut,
  MessageSquarePlus,
  Moon,
  Plus,
  ScrollText,
  SlidersHorizontal,
  Sun,
  TriangleAlert,
  Wifi,
  WifiOff,
  type LucideIcon,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { FeedbackDialog } from "./FeedbackDialog";
import { Link, NavLink, useLocation, useNavigate } from "react-router-dom";
import { api } from "../lib/api";
import { useMonitorStore } from "../lib/store";
import { canWrite, isAdmin, useAuth } from "../lib/auth";
import { logoUrl, productName, useSettings } from "../lib/settings";
import { useTheme } from "../lib/theme";
import { useToast } from "../lib/toast";
import { cn, visualStatus } from "../lib/utils";
import { Button } from "./ui/button";

type Item = {
  to: string;
  end?: boolean;
  label: string;
  Icon: LucideIcon;
  /** A plain count after the label. */
  count?: number;
  /** A count that needs attention, in the danger colour. */
  alert?: number;
};

export function AppLayout({ children }: { children: React.ReactNode }) {
  const { theme, toggle } = useTheme();
  const { connected, probeOk, monitors } = useMonitorStore();
  const { settings, reload } = useSettings();
  const { user } = useAuth();
  const writable = canWrite(user);
  // "Not connected" only once it has lasted: the socket opens a moment
  // after the page does, and a reconnect of a second is not worth a banner.
  const [feedback, setFeedback] = useState(false);
  const [offline, setOffline] = useState(false);
  useEffect(() => {
    if (connected) {
      setOffline(false);
      return;
    }
    const t = window.setTimeout(() => setOffline(true), 5000);
    return () => clearTimeout(t);
  }, [connected]);
  const { toast } = useToast();
  const navigate = useNavigate();
  const location = useLocation();
  const [product, setProduct] = useState("Argus");
  const [openIncidents, setOpenIncidents] = useState<number | null>(null);

  useEffect(() => {
    setProduct(productName(settings));
  }, [settings]);

  const downCount = useMemo(
    () => (monitors ?? []).filter((m) => visualStatus(m) === "down").length,
    [monitors],
  );

  // The Incidents badge is the open count. Outages open incidents on their
  // own, so it is re-read whenever the number of down monitors changes.
  useEffect(() => {
    let cancelled = false;
    api.incidents
      .list(true)
      .then((list) => !cancelled && setOpenIncidents(list.length))
      .catch(() => !cancelled && setOpenIncidents(null));
    return () => {
      cancelled = true;
    };
  }, [downCount, location.pathname]);

  const handleLogout = async () => {
    try {
      await fetch("/api/auth/logout", {
        method: "POST",
        credentials: "include",
      });
      // Drop the signed-in settings so the next person does not see them.
      await reload();
      navigate("/login", { replace: true });
    } catch {
      toast("error", "logout failed");
    }
  };

  const logo = logoUrl(settings);

  const items: Item[] = [
    { to: "/", end: true, label: "Overview", Icon: LayoutDashboard },
    { to: "/environments", label: "Environments", Icon: Layers },
    { to: "/monitors", label: "Monitors", Icon: Activity, count: monitors?.length },
    { to: "/incidents", label: "Incidents", Icon: TriangleAlert, alert: openIncidents ?? undefined },
    { to: "/status-pages", label: "Status pages", Icon: Globe },
  ];
  items.push({ to: "/sla", label: "SLA reports", Icon: BarChart3 });
  items.push({ to: "/audit", label: "Audit log", Icon: ScrollText });

  const inSettings = location.pathname.startsWith("/settings");

  return (
    <div className="min-h-screen md:grid md:grid-cols-[232px_minmax(0,1fr)]">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-3 focus:z-50 focus:bg-background focus:px-3 focus:py-2 focus:text-sm focus:outline-none focus:ring-2 focus:ring-ring"
      >
        Skip to content
      </a>

      <aside className="flex flex-col gap-5 border-b border-border bg-sidebar px-4 py-4 md:sticky md:top-0 md:h-screen md:gap-7 md:border-b-0 md:border-r md:py-5">
        <div className="flex items-center gap-2">
          <Link to="/" className="flex min-w-0 items-center gap-2.5 px-2 py-0.5">
            {logo ? (
              <img src={logo} alt="" className="h-7 w-7 object-contain" />
            ) : (
              <span className="flex h-7 w-7 items-center justify-center text-primary">
                <RadarIcon />
              </span>
            )}
            <span className="truncate text-xl font-semibold tracking-tight">{product}</span>
          </Link>
          {/* Phones have no room for the bottom block, so the essentials sit by the brand. */}
          <div className="ml-auto flex items-center gap-1 md:hidden">
            <ThemeButton theme={theme} toggle={toggle} />
            <Button
              variant={inSettings ? "outline" : "ghost"}
              size="icon"
              aria-label="Settings"
              onClick={() => navigate("/settings")}
            >
              <SlidersHorizontal size={16} />
            </Button>
            <Button variant="ghost" size="icon" aria-label="Log out" onClick={handleLogout}>
              <LogOut size={16} />
            </Button>
          </div>
        </div>

        <nav aria-label="Primary" className="-mx-1 flex gap-1 overflow-x-auto px-1 md:mx-0 md:flex-col md:gap-0.5 md:overflow-visible md:px-0">
          {items.map(({ to, end, label, Icon, count, alert }) => (
            <NavLink
              key={to}
              to={to}
              end={end}
              className={({ isActive }) =>
                cn(
                  "flex h-11 shrink-0 items-center gap-2.5 rounded-lg px-2.5 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring",
                  isActive
                    ? "bg-muted font-semibold text-foreground"
                    : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                )
              }
            >
              {({ isActive }) => (
                <>
                  <Icon size={16} className={cn("shrink-0", isActive && "text-primary")} />
                  <span className="whitespace-nowrap">{label}</span>
                  {count != null && (
                    <span className="ml-auto hidden font-mono text-xs text-muted-foreground md:inline">{count}</span>
                  )}
                  {alert != null && alert > 0 && (
                    <span className="ml-auto inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-danger px-1.5 text-[11px] font-semibold text-danger-foreground">
                      {alert}
                    </span>
                  )}
                </>
              )}
            </NavLink>
          ))}
        </nav>

        {writable && (
          <Button onClick={() => navigate("/monitors/new")} className="hidden md:inline-flex">
            <Plus size={16} />
            Add monitor
          </Button>
        )}

        <div className="mt-auto hidden flex-col gap-3 px-1 md:flex">
          <div
            role="status"
            aria-live="polite"
            className={cn(
              "flex items-center gap-2 text-xs font-medium",
              connected ? "text-success" : "text-warning",
            )}
          >
            {connected ? <Wifi size={12} aria-hidden="true" /> : <WifiOff size={12} aria-hidden="true" />}
            {connected ? "Live" : "Connecting…"}
          </div>
          <div className="flex items-center gap-1 border-t border-border pt-3">
            <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground" title={user?.email}>
              {user?.email ?? ""}
            </span>
            <ThemeButton theme={theme} toggle={toggle} />
            {isAdmin(user) && (
              // Administrators only: on a rebranded install, the people it
              // is run for should not be pointed at Zypher.
              <Button variant="ghost" size="icon" aria-label="Send feedback" title="Send feedback" onClick={() => setFeedback(true)}>
                <MessageSquarePlus size={16} />
              </Button>
            )}
            <Button
              variant={inSettings ? "outline" : "ghost"}
              size="icon"
              aria-label="Settings"
              onClick={() => navigate("/settings")}
            >
              <SlidersHorizontal size={16} />
            </Button>
            <Button variant="ghost" size="icon" aria-label="Log out" onClick={handleLogout}>
              <LogOut size={16} />
            </Button>
          </div>
        </div>
      </aside>
      <FeedbackDialog open={feedback} onClose={() => setFeedback(false)} />

      <div className="min-w-0">
        {probeOk === false && (
          <div className="border-b border-warning/40 bg-warning/15 px-4 py-2 text-center text-sm text-warning">
            Probe heartbeat is stale. Monitors are frozen at last-known status until the probe process recovers.
          </div>
        )}
        {offline && (
          // The sidebar says this too, but phones have no sidebar, and a
          // wall display showing old data must say so where it is seen.
          <div role="status" className="border-b border-warning/40 bg-warning/15 px-4 py-2 text-center text-sm text-warning">
            Not connected to Argus. What is shown may be out of date; reconnecting…
          </div>
        )}
        {/* Overview has the getting-started checklist, which asks the same. */}
        {writable && settings && (settings.channel_count ?? 0) === 0 && location.pathname !== "/" && (
          <div className="border-b border-border bg-muted/40 px-4 py-2 text-center text-sm">
            No notification channels yet.{" "}
            <Link to="/settings/alerts" className="font-medium text-primary hover:underline">
              Add email, Slack, Discord, or a webhook
            </Link>{" "}
            so Argus can page you.
          </div>
        )}
        <main id="main-content" className="mx-auto max-w-[1440px] px-4 py-6 md:px-10 md:py-7" tabIndex={-1}>
          {children}
        </main>
      </div>
    </div>
  );
}

function ThemeButton({ theme, toggle }: { theme: string; toggle: () => void }) {
  return (
    <Button
      variant="ghost"
      size="icon"
      aria-label={theme === "dark" ? "Switch to light theme" : "Switch to dark theme"}
      aria-pressed={theme === "dark"}
      onClick={toggle}
    >
      {theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
    </Button>
  );
}

function RadarIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
      <path d="M19.07 4.93A10 10 0 0 0 6.99 3.34" />
      <path d="M4 6h.01" />
      <path d="M2.29 9.62a10 10 0 1 0 19.42-3.34" />
      <path d="M16.24 7.76A6 6 0 0 0 8.23 6.77" />
      <path d="M12 10v2" />
      <path d="m16.24 16.24-2.12-4.24" />
    </svg>
  );
}
