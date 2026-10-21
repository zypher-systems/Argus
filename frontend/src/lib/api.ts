import type {
  Alert,
  CheckNowResult,
  APIToken,
  AppSettings,
  AppUser,
  AuditEntry,
  BrandingSettings,
  ImportResult,
  Incident,
  InstanceInfo,
  LatencyPercentiles,
  MaintenanceWindow,
  Monitor,
  MonitorEvent,
  NotificationChannel,
  Organization,
  ProbeNode,
  PublicStatus,
  Realm,
  ResultsResponse,
  SeriesPoint,
  SLARow,
  StatusPage,
  StatusSubscriber,
  TestRunResult,
  Onboarding,
  QuickAddResult,
} from "./types";

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

/** Fired on window when a signed-in request is refused as not signed in. */
export const UNAUTHORIZED_EVENT = "argus:unauthorized";

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api${path}`, {
    credentials: "include",
    headers: init?.body ? { "Content-Type": "application/json" } : undefined,
    ...init,
  });
  if (!res.ok) {
    let message = `request failed (${res.status})`;
    try {
      const data = await res.json();
      if (data?.error) message = data.error;
    } catch {
      /* ignore */
    }
    // Any signed-in request answered 401 means the session is gone: signed
    // in elsewhere, or expired.
    // The app listens for this and returns to the sign-in page, where it
    // used to sit on its last data looking live. /auth/* reports its own.
    if (res.status === 401 && !path.startsWith("/auth/")) {
      window.dispatchEvent(new CustomEvent(UNAUTHORIZED_EVENT, { detail: message }));
    }
    throw new ApiError(res.status, message);
  }
  if (res.status === 204) return undefined as T;
  return (await res.json()) as T;
}

function put<T>(path: string, body: unknown): Promise<T> {
  return req<T>(path, { method: "PUT", body: JSON.stringify(body) });
}

function post<T>(path: string, body?: unknown): Promise<T> {
  return req<T>(path, {
    method: "POST",
    body: body === undefined ? undefined : JSON.stringify(body),
  });
}

function del<T>(path: string): Promise<T> {
  return req<T>(path, { method: "DELETE" });
}

export const api = {
  login: (email: string, password: string) =>
    post<{ ok: boolean }>("/auth/login", { email, password }),
  logout: () => post<{ ok: boolean }>("/auth/logout"),
  register: (body: { email: string; password: string; name?: string; org_name: string }) =>
    post<{ ok: boolean }>("/auth/register", body),
  hosted: () => req<{ hosted: boolean; registration: boolean }>("/auth/hosted"),
  me: () => req<AppUser>("/auth/me"),
  oidcStatus: () => req<{ enabled: boolean }>("/auth/oidc"),
  changePassword: (current: string, password: string) =>
    post<{ ok: boolean }>("/auth/password", { current, password }),

  monitors: {
    list: () => req<Monitor[]>("/monitors"),
    series: () => req<{ granularity: string; points: SeriesPoint[] }>("/monitors/series"),
    get: (id: number) => req<Monitor>(`/monitors/${id}`),
    create: (body: Partial<Monitor>) => post<Monitor>("/monitors", body),
    update: (id: number, body: Partial<Monitor>) => put<Monitor>(`/monitors/${id}`, body),
    remove: (id: number) => del<{ ok: boolean }>(`/monitors/${id}`),
    pause: (id: number) => post<{ enabled: boolean }>(`/monitors/${id}/pause`),
    checkNow: (id: number) => post<CheckNowResult>(`/monitors/${id}/check`),
    /** One monitor per pasted line: URL, host:port, or a host to ping. */
    quick: (text: string) => post<QuickAddResult>("/monitors/quick", { text }),
    /** Runs a transaction without saving it; monitor_id lets blank secrets use stored ones. */
    testRun: (body: Partial<Monitor> & { monitor_id?: number }) =>
      post<TestRunResult>("/monitors/test-run", body),
    mute: (id: number, minutes: number) => post<Monitor>(`/monitors/${id}/mute`, { minutes }),
    ack: (id: number, minutes = 0) => post<Monitor>(`/monitors/${id}/ack`, { minutes }),
    clone: (id: number) => post<Monitor>(`/monitors/${id}/clone`),
    bulk: (body: {
      ids: number[];
      action: "pause" | "resume" | "delete" | "add_tag" | "add_realm" | "attach_channel";
      tag?: string;
      realm_id?: number;
      channel_id?: number;
    }) => post<{ ok: boolean; updated: number }>("/monitors/bulk", body),
    results: (id: number, range: string) =>
      req<ResultsResponse>(`/monitors/${id}/results?range=${range}`),
    events: (id: number) => req<MonitorEvent[]>(`/monitors/${id}/events`),
    alerts: (id: number) => req<Alert[]>(`/monitors/${id}/alerts`),
    percentiles: (id: number, range = "24h") =>
      req<LatencyPercentiles>(`/monitors/${id}/percentiles?range=${range}`),
  },

  branding: () => req<BrandingSettings>("/branding"),

  settings: {
    get: () => req<AppSettings>("/settings"),
    put: (body: Partial<AppSettings>) => put<AppSettings>("/settings", body),
    smtpTest: (to: string) => post<{ ok: boolean }>("/settings/smtp-test", { to }),
  },

  channels: {
    list: () => req<NotificationChannel[]>("/channels"),
    create: (body: Partial<NotificationChannel>) =>
      post<NotificationChannel>("/channels", body),
    update: (id: number, body: Partial<NotificationChannel>) =>
      put<NotificationChannel>(`/channels/${id}`, body),
    remove: (id: number) => del<{ ok: boolean }>(`/channels/${id}`),
    test: (id: number) => post<{ ok: boolean }>(`/channels/${id}/test`),
  },

  reports: {
    sendNow: () => post<{ ok: boolean; month: string; recipients: number }>("/reports/test"),
  },

  status: {
    public: (slug: string) => req<PublicStatus>(`/status/${slug}`),
    byHost: () => req<PublicStatus>("/status-by-host"),
    list: () => req<StatusPage[]>("/status-pages"),
    create: (body: Partial<StatusPage>) => post<StatusPage>("/status-pages", body),
    update: (id: number, body: Partial<StatusPage>) =>
      put<StatusPage>(`/status-pages/${id}`, body),
    remove: (id: number) => del<{ ok: boolean }>(`/status-pages/${id}`),
    subscribe: (slug: string, email: string) =>
      post<{ status: string }>(`/status/${slug}/subscribe`, { email }),
    subscribers: (pageId: number) =>
      req<StatusSubscriber[]>(`/status-pages/${pageId}/subscribers`),
    addSubscriber: (pageId: number, body: { kind: string; dest: string }) =>
      post<StatusSubscriber>(`/status-pages/${pageId}/subscribers`, body),
    removeSubscriber: (pageId: number, id: number) =>
      del<{ ok: boolean }>(`/status-pages/${pageId}/subscribers/${id}`),
    confirm: (token: string) =>
      req<{ status: string }>(`/subscribe/confirm?token=${encodeURIComponent(token)}`),
    unsubscribe: (token: string) =>
      post<{ status: string }>("/subscribe/unsubscribe", { token }),
  },

  incidents: {
    list: (openOnly = false) =>
      req<Incident[]>(openOnly ? "/incidents?open=1" : "/incidents"),
    get: (id: number) => req<Incident>(`/incidents/${id}`),
    create: (body: Partial<Incident> & { message?: string }) =>
      post<Incident>("/incidents", body),
    update: (id: number, body: { status: string; message: string }) =>
      post<Incident>(`/incidents/${id}/updates`, body),
  },

  maintenance: {
    list: () => req<MaintenanceWindow[]>("/maintenance-windows"),
    create: (body: Partial<MaintenanceWindow> & { monitor_id: number | null }) =>
      post<MaintenanceWindow>("/maintenance-windows", body),
    remove: (id: number) => del<{ ok: boolean }>(`/maintenance-windows/${id}`),
  },

  tokens: {
    list: () => req<APIToken[]>("/tokens"),
    create: (name: string) => post<APIToken>("/tokens", { name }),
    remove: (id: number) => del<{ ok: boolean }>(`/tokens/${id}`),
  },

  users: {
    list: () => req<AppUser[]>("/users"),
    create: (body: { email: string; name?: string; role: string; password: string }) =>
      post<AppUser>("/users", body),
    update: (id: number, body: { name: string; role: string; disabled: boolean }) =>
      put<AppUser>(`/users/${id}`, body),
    remove: (id: number) => del<{ ok: boolean }>(`/users/${id}`),
  },

  probeNodes: {
    list: () => req<ProbeNode[]>("/probe-nodes"),
    create: (body: { name: string; region: string }) => post<ProbeNode>("/probe-nodes", body),
    remove: (id: number) => del<{ ok: boolean }>(`/probe-nodes/${id}`),
  },

  realms: {
    list: () => req<Realm[]>("/realms"),
    create: (name: string) => post<Realm>("/realms", { name }),
    update: (id: number, name: string) => put<Realm>(`/realms/${id}`, { name }),
    remove: (id: number) => del<{ ok: boolean }>(`/realms/${id}`),
  },

  sla: (range = "30d") => req<{ from: string; rows: SLARow[] }>(`/sla?range=${range}`),
  slaCsvUrl: (range = "30d") => `/api/sla.csv?range=${range}`,
  audit: (limit = 100) => req<AuditEntry[]>(`/audit?limit=${limit}`),
  instance: () => req<InstanceInfo>("/instance"),
  org: {
    get: () => req<Organization>("/org"),
    update: (body: { name?: string; slug?: string }) => put<Organization>("/org", body),
  },

  schedulePreview: (schedule: string, timezone: string) =>
    post<{ next: string[]; error: string }>("/schedule/preview", { schedule, timezone }),

  onboarding: {
    get: () => req<Onboarding>("/onboarding"),
    setDismissed: (dismissed: boolean) => put<Onboarding>("/onboarding", { dismissed }),
    statusPage: () => post<StatusPage>("/onboarding/status-page", {}),
  },

  export: () => req<unknown>("/export"),
  exportEncrypted: (passphrase: string) => post<unknown>("/export", { passphrase }),
  import: (body: { format: string; data?: unknown; passphrase?: string } | unknown) =>
    post<ImportResult>("/import", body),
};
