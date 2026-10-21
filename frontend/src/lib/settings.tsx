import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { useLocation } from "react-router-dom";
import { api } from "./api";
import type { AppSettings } from "./types";
import { applyAccent } from "./utils";

interface SettingsCtx {
  settings: AppSettings | null;
  reload: () => Promise<void>;
}

const Ctx = createContext<SettingsCtx>({ settings: null, reload: async () => {} });

export function SettingsProvider({ children }: { children: ReactNode }) {
  const [settings, setSettings] = useState<AppSettings | null>(null);
  const location = useLocation();

  const reload = useCallback(async () => {
    try {
      setSettings(await api.settings.get());
      return;
    } catch {
      /* unauthenticated — fall through to public branding */
    }
    try {
      const branding = await api.branding();
      setSettings({
        smtp: {
          host: "",
          port: 587,
          username: "",
          password: "",
          from: "",
          tls_mode: "starttls",
          global_recipients: "",
        },
        general: { base_url: "", notify_max_per_hour: 0 },
        retention: { raw_days: 7, hourly_days: 90, daily_days: 365 },
        branding,
        smtp_has_password: false,
        reports: { monthly_enabled: false, recipients: "" },
        hosted: false,
        registration: false,
        channel_count: 0,
        oidc: {
          enabled: false,
          issuer: "",
          client_id: "",
          client_secret: "",
          default_role: "viewer",
        },
      });
    } catch {
      setSettings(null);
    }
  }, []);

  useEffect(() => {
    reload();
  }, [reload]);

  useEffect(() => {
    if (location.pathname.startsWith("/status/") || location.pathname.startsWith("/subscribe")) return;
    applyBranding(settings);
  }, [settings, location.pathname]);

  const value = useMemo(() => ({ settings, reload }), [settings, reload]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useSettings() {
  return useContext(Ctx);
}

export function productName(settings: AppSettings | null): string {
  const name = settings?.branding?.product_name?.trim();
  return name || "Argus";
}

export function accentColor(settings: AppSettings | null): string {
  return settings?.branding?.accent_color?.trim() ?? "";
}

export function logoUrl(settings: AppSettings | null): string | null {
  const raw = settings?.branding?.logo_url?.trim() ?? "";
  if (raw.startsWith("https://") || raw.startsWith("http://") || raw.startsWith("/")) {
    return raw;
  }
  return null;
}

export function loginMessage(settings: AppSettings | null): string {
  return settings?.branding?.login_message?.trim() || "Uptime monitoring, done right.";
}

export function applyBranding(settings: AppSettings | null) {
  applyAccent(document.documentElement, accentColor(settings));
}
