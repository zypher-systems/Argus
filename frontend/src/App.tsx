import { useEffect, useState } from "react";
import {
  BrowserRouter as Router,
  Navigate,
  Outlet,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";
import { AppLayout } from "./components/AppLayout";
import { PageErrorBoundary } from "./components/PageErrorBoundary";
import { api, UNAUTHORIZED_EVENT } from "./lib/api";
import { MonitorStoreProvider, useMonitorStore } from "./lib/store";
import { useSettings, productName } from "./lib/settings";
import { SettingsProvider } from "./lib/settings";
import { ThemeProvider } from "./lib/theme";
import { ToastProvider } from "./lib/toast";
import { updateFavicon } from "./lib/favicon";
import { Dashboard } from "./pages/Dashboard";
import { Login } from "./pages/Login";
import { MonitorDetail } from "./pages/MonitorDetail";
import { MonitorForm } from "./pages/MonitorForm";
import { Overview } from "./pages/Overview";
import { Realms } from "./pages/Realms";
import { Settings } from "./pages/Settings";
import { Incidents } from "./pages/Incidents";
import { StatusPages } from "./pages/StatusPages";
import { StatusPublic } from "./pages/StatusPublic";
import { SubscribeConfirm } from "./pages/SubscribeConfirm";
import { SubscribeUnsubscribe } from "./pages/SubscribeUnsubscribe";
import { SLA } from "./pages/SLA";
import { Audit } from "./pages/Audit";
import { AuthProvider } from "./lib/auth";
import type { AppUser } from "./lib/types";

function Favicon() {
  const { monitors } = useMonitorStore();
  const { settings } = useSettings();
  useEffect(() => {
    updateFavicon(monitors, productName(settings));
  }, [monitors, settings]);
  return null;
}

function ProtectedLayout() {
  const [state, setState] = useState<"checking" | "ok" | "denied">("checking");
  const [user, setUser] = useState<AppUser | null>(null);
  const location = useLocation();
  const { monitors, refresh } = useMonitorStore();

  useEffect(() => {
    let cancelled = false;
    const deny = () => {
      if (cancelled) return;
      setState("denied");
    };
    api
      .me()
      .then((u) => {
        if (cancelled) return;
        setUser(u);
        setState("ok");
        refresh();
      })
      .catch(() => deny());
    const onUnauthorized = () => deny();
    window.addEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    return () => {
      cancelled = true;
      window.removeEventListener(UNAUTHORIZED_EVENT, onUnauthorized);
    };
  }, [refresh]);

  if (state === "denied") {
    return <Navigate to="/login" replace state={{ from: location }} />;
  }
  if (state === "checking" && monitors === null) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <span className="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-primary" />
      </div>
    );
  }

  return (
    <AuthProvider user={user}>
      <AppLayout>
        <PageErrorBoundary resetKey={location.pathname}>
          <Outlet />
        </PageErrorBoundary>
      </AppLayout>
    </AuthProvider>
  );
}

function ProtectedShell() {
  return (
    <MonitorStoreProvider>
      <Favicon />
      <ProtectedLayout />
    </MonitorStoreProvider>
  );
}

function HostAwareApp() {
  const location = useLocation();
  const [hostPage, setHostPage] = useState<import("./lib/types").PublicStatus | false | null>(null);
  useEffect(() => {
    let cancelled = false;
    api.status
      .byHost()
      .then((p) => {
        if (!cancelled) setHostPage(p);
      })
      .catch(() => {
        if (!cancelled) setHostPage(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);
  if (hostPage === null) {
    return (
      <div className="flex min-h-screen items-center justify-center">
        <span className="h-8 w-8 animate-spin rounded-full border-2 border-border border-t-primary" />
      </div>
    );
  }
  if (hostPage && !location.pathname.startsWith("/subscribe")) {
    return <StatusPublic initial={hostPage} />;
  }
  return (
    <Routes>
      <Route path="/status/:slug" element={<StatusPublic />} />
      <Route path="/subscribe/confirm" element={<SubscribeConfirm />} />
      <Route path="/subscribe/unsubscribe" element={<SubscribeUnsubscribe />} />
      <Route path="/login" element={<Login />} />
      <Route element={<ProtectedShell />}>
        <Route path="/" element={<Overview />} />
        <Route path="/monitors" element={<Dashboard />} />
        <Route path="/environments" element={<Realms />} />
        {/* 2.4 and earlier called these Realms; keep old links working. */}
        <Route path="/realms" element={<Realms />} />
        <Route path="/monitors/new" element={<MonitorForm />} />
        <Route path="/monitors/:id" element={<MonitorDetail />} />
        <Route path="/monitors/:id/edit" element={<MonitorForm />} />
        <Route path="/incidents" element={<Incidents />} />
        <Route path="/status-pages" element={<StatusPages />} />
        <Route path="/sla" element={<SLA />} />
        <Route path="/audit" element={<Audit />} />
        <Route path="/settings" element={<Settings />} />
        <Route path="/settings/:section" element={<Settings />} />
      </Route>
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  );
}

export default function App() {
  return (
    <ThemeProvider>
      <ToastProvider>
        <Router>
          <SettingsProvider>
            <HostAwareApp />
          </SettingsProvider>
        </Router>
      </ToastProvider>
    </ThemeProvider>
  );
}
