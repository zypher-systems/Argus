export interface CheckEvidence {
  kind: string;
  at: string;
  success: boolean;
  error?: string;
  status_code?: number;
  body_snippet?: string;
  content_type?: string;
  tls_error?: string;
  dns_answers?: string[];
  remote_addr?: string;
  curl?: string;
  /** Each step a transaction ran, up to and including the one that failed. */
  steps?: StepEvidence[];
}

export interface StepEvidence {
  name: string;
  status_code?: number;
  latency_ms: number;
  error?: string;
}

export interface StepExtract {
  name: string;
  from: "json" | "header";
  path: string;
}

export interface TransactionStep {
  name: string;
  method: string;
  url: string;
  headers?: Record<string, string>;
  body?: string;
  expect_status?: number;
  keyword?: string;
  extract?: StepExtract[];
}

/** The getting-started checklist; each step is read from the data. */
export interface Onboarding {
  monitors: number;
  channels: number;
  status_pages: number;
  test_alert_at: string | null;
  dismissed_at: string | null;
  smtp_ready: boolean;
}

export interface QuickAddResult {
  created: Monitor[];
  skipped: { line: number; input: string; reason: string }[];
}

export interface TestRunResult {
  success: boolean;
  latency_ms: number;
  error: string;
  evidence: CheckEvidence;
}

export interface CheckNowResult {
  success: boolean;
  latency_ms: number | null;
  error: string;
  evidence: CheckEvidence | null;
  applied: boolean;
}

export interface MonitorState {
  status: "up" | "down" | "pending";
  last_latency_ms: number | null;
  last_check_at: string | null;
  last_change_at: string | null;
  consecutive_failures: number;
  down_since: string | null;
  escalated_at: string | null;
  uptime_24h: number | null;
  tls_expires_at: string | null;
  /** When a scheduled heartbeat is next due. */
  next_ping_at?: string | null;
  /** Domain registration expiry, for domain monitors. */
  expires_at?: string | null;
  slow: boolean;
  slow_since: string | null;
  stale: boolean;
  muted_until?: string | null;
  acked_at?: string | null;
  acked_until?: string | null;
  last_failure_evidence?: CheckEvidence | null;
  last_failure_at?: string | null;
}

export interface MonitorConfig {
  method?: string;
  expect_status?: number;
  keyword?: string;
  insecure_skip_verify?: boolean;
  timeout_seconds?: number;
  headers?: Record<string, string>;
  body?: string;
  json_path?: string;
  json_expect?: string;
  auth?: string;
  auth_user?: string;
  auth_password?: string;
  auth_token?: string;
  record?: string;
  expect_value?: string;
  warn_days?: number;
  tls_mode?: string;
  database?: string;
  query?: string;
  expect_ip?: string;
  keyword_invert?: boolean;
  proxy_url?: string;
  push_token?: string;
  grace_seconds?: number;
  /** Cron expression for a push monitor that pings at set times. */
  schedule?: string;
  timezone?: string;
  steps?: TransactionStep[];
  /** Names come back with empty values; an empty value on save keeps the stored one. */
  secrets?: Record<string, string>;
}

export interface Monitor {
  id: number;
  name: string;
  type: string;
  target: string;
  interval_seconds: number;
  retries: number;
  escalate_minutes: number;
  escalate_recipients: string;
  enabled: boolean;
  tags: string;
  latency_threshold_ms: number;
  resend_minutes: number;
  parent_id: number | null;
  depends_on: number | null;
  realm_ids: number[] | null;
  channel_ids: number[] | null;
  slo_target: number;
  probe_node_ids: number[] | null;
  /** How many locations must see it down before it is down. */
  confirm_locations?: number;
  config: MonitorConfig;
  created_at: string;
  state: MonitorState;
  regions?: ProbeRegion[];
}

export interface Realm {
  id: number;
  name: string;
  created_at: string;
}

export interface RawPoint {
  ts: string;
  latency_ms: number | null;
  success: boolean;
}

export interface RollupPoint {
  ts: string;
  total: number;
  failures: number;
  avg_latency: number | null;
  max_latency: number | null;
}

export interface ResultsResponse {
  granularity: "raw" | "hour" | "day";
  points: RawPoint[] | RollupPoint[];
}

export interface SeriesPoint {
  monitor_id: number;
  ts: string;
  avg_latency: number | null;
  max_latency: number | null;
  failures: number;
  total: number;
}

export interface MonitorEvent {
  id: number;
  monitor_id: number;
  ts: string;
  type: string;
  detail: string;
}

export interface Alert {
  id: number;
  monitor_id: number;
  type: string;
  status: string;
  recipients: string;
  error: string;
  created_at: string;
}

export interface SMTPSettings {
  host: string;
  port: number;
  username: string;
  password: string;
  from: string;
  tls_mode: "none" | "starttls" | "ssl";
  global_recipients: string;
}

export type ChannelType =
  | "email"
  | "slack"
  | "discord"
  | "teams"
  | "pagerduty"
  | "opsgenie"
  | "telegram"
  | "ntfy"
  | "webhook";

/** What each channel type calls its destination, and how to get one. */
export const CHANNEL_TYPES: {
  value: ChannelType;
  label: string;
  destLabel: string;
  hint: string;
  placeholder: string;
}[] = [
  {
    value: "email",
    label: "Email",
    destLabel: "Recipients",
    hint: "Comma-separated addresses. Uses the SMTP settings above.",
    placeholder: "oncall@example.com, you@example.com",
  },
  {
    value: "slack",
    label: "Slack",
    destLabel: "Webhook URL",
    hint: "Slack: Apps → Incoming Webhooks → Add to workspace.",
    placeholder: "https://hooks.slack.com/services/…",
  },
  {
    value: "discord",
    label: "Discord",
    destLabel: "Webhook URL",
    hint: "Channel settings → Integrations → Webhooks.",
    placeholder: "https://discord.com/api/webhooks/…",
  },
  {
    value: "teams",
    label: "Microsoft Teams",
    destLabel: "Webhook URL",
    hint: "Teams channel → Connectors or a Power Automate flow that posts to the channel.",
    placeholder: "https://…webhook.office.com/webhookb2/…",
  },
  {
    value: "pagerduty",
    label: "PagerDuty",
    destLabel: "Integration key",
    hint: "Service → Integrations → Events API v2. A recovery resolves the incident the outage opened. Add @eu for the EU region.",
    placeholder: "R0ABCDE1234567890FGHIJKLMNOPQRST",
  },
  {
    value: "opsgenie",
    label: "Opsgenie",
    destLabel: "API key",
    hint: "Teams → Integrations → API. A recovery closes the alert by alias. Add @eu for the EU region.",
    placeholder: "00000000-0000-0000-0000-000000000000",
  },
  {
    value: "telegram",
    label: "Telegram",
    destLabel: "Bot token and chat id",
    hint: "From @BotFather, then the chat id the bot posts to, separated by a colon.",
    placeholder: "123456:ABC-DEF1234ghIkl:-1001234567890",
  },
  {
    value: "ntfy",
    label: "ntfy",
    destLabel: "Topic URL",
    hint: "Your topic URL. For a protected topic use https://user:password@host/topic.",
    placeholder: "https://ntfy.sh/argus-alerts",
  },
  {
    value: "webhook",
    label: "Generic webhook",
    destLabel: "URL",
    hint: "POST JSON: event, message, monitor, at, detail.",
    placeholder: "https://hooks.example.com/argus",
  },
];

export const CHANNEL_EVENTS = [
  { value: "down", label: "Down" },
  { value: "recovery", label: "Recovery" },
  { value: "slow", label: "Slow" },
  { value: "cert", label: "Cert expiry" },
  { value: "domain", label: "Domain expiry" },
  { value: "escalation", label: "Escalation" },
  { value: "probe_down", label: "Probe down" },
  { value: "probe_up", label: "Probe recovered" },
  { value: "resend", label: "Still down (resend)" },
] as const;

export interface NotificationChannel {
  id: number;
  name: string;
  type: ChannelType;
  dest: string;
  events: string[];
  created_at: string;
}

export interface GeneralSettings {
  base_url: string;
  notify_max_per_hour: number;
}

export interface RetentionSettings {
  raw_days: number;
  hourly_days: number;
  daily_days: number;
}

export interface BrandingSettings {
  product_name: string;
  logo_url: string;
  accent_color: string;
  login_message: string;
}

export interface ReportSettings {
  monthly_enabled: boolean;
  recipients: string;
  last_sent_month?: string;
}

export interface AppSettings {
  smtp: SMTPSettings;
  general: GeneralSettings;
  retention: RetentionSettings;
  branding: BrandingSettings;
  oidc: OIDCSettings;
  reports?: ReportSettings;
  smtp_has_password: boolean;
  oidc_has_secret?: boolean;
  hosted?: boolean;
  registration?: boolean;
  channel_count?: number;
}

export interface Organization {
  id: number;
  name: string;
  slug: string;
  plan: "community" | "pro";
  created_at: string;
}

export interface ProbeRegion {
  node_id: number;
  name: string;
  region: string;
  status: string;
  last_latency_ms: number | null;
  last_check_at: string | null;
  last_error: string;
  /** This location's failures in a row, counted against the monitor's retries. */
  consecutive_failures?: number;
}

export interface ProbeNode {
  id: number;
  name: string;
  region: string;
  token?: string;
  last_seen: string | null;
  enabled: boolean;
  created_at: string;
}

export interface AppUser {
  id: number;
  email: string;
  name: string;
  role: "admin" | "operator" | "viewer";
  disabled: boolean;
  created_at: string;
  has_password: boolean;
  oidc?: boolean;
  org_id?: number;
}

export interface OIDCSettings {
  enabled: boolean;
  issuer: string;
  client_id: string;
  client_secret: string;
  default_role: string;
}

export interface SLARow {
  monitor_id: number;
  name: string;
  slo_target: number;
  uptime_pct: number | null;
  total_checks: number;
  failed_checks: number;
  within_slo: boolean;
}

export interface AuditEntry {
  id: number;
  ts: string;
  user_id: number | null;
  email: string;
  action: string;
  resource: string;
  resource_id: number | null;
  detail: unknown;
  ip: string;
}

export interface InstanceInfo {
  instance_id: string;
  version: string;
}

export interface StatusPage {
  id: number;
  name: string;
  slug: string;
  headline: string;
  branded: boolean;
  published: boolean;
  monitor_ids: number[] | null;
  custom_domain: string;
  created_at: string;
}

export interface PublicStatusDay {
  day: string;
  status: string;
}

export interface PublicStatusMonitor {
  name: string;
  type: string;
  status: "up" | "down" | "pending";
  slow: boolean;
  uptime_90d: number | null;
  days: PublicStatusDay[];
}

export interface PublicStatusIncidentUpdate {
  status: string;
  message: string;
  created_at: string;
}

export interface PublicStatusIncident {
  id: number;
  title: string;
  status: string;
  impact: string;
  monitor: string;
  opened_at: string;
  resolved_at: string | null;
  updates: PublicStatusIncidentUpdate[];
}

export interface PublicStatus {
  name: string;
  slug: string;
  headline: string;
  overall: "operational" | "degraded" | "down";
  custom_domain?: string;
  branding?: {
    product_name: string;
    logo_url: string;
    accent_color: string;
  };
  monitors: PublicStatusMonitor[];
  incidents: PublicStatusIncident[];
  maintenance?: PublicMaintenance[];
}

export interface IncidentUpdate {
  id: number;
  incident_id: number;
  status: string;
  message: string;
  created_at: string;
}

export interface Incident {
  id: number;
  monitor_id: number | null;
  monitor_name?: string;
  status: string;
  title: string;
  impact: string;
  public: boolean;
  auto_opened: boolean;
  opened_at: string;
  resolved_at: string | null;
  updates?: IncidentUpdate[];
}

export interface StatusSubscriber {
  id: number;
  status_page_id: number;
  kind: "email" | "webhook";
  dest: string;
  confirmed: boolean;
  created_at: string;
}

export interface PublicMaintenance {
  starts_at: string;
  ends_at: string;
  note: string;
  monitor?: string;
  active: boolean;
}

export interface MaintenanceWindow {
  id: number;
  monitor_id: number | null;
  starts_at: string;
  ends_at: string;
  note: string;
  /** Announced on status pages that list the monitor. */
  public: boolean;
  created_at: string;
}

export interface APIToken {
  id: number;
  name: string;
  token_prefix: string;
  token?: string;
  created_at: string;
  last_used_at: string | null;
}

export interface LatencyPercentiles {
  p50: number | null;
  p95: number | null;
  p99: number | null;
  samples: number;
}

export interface ImportResult {
  imported: number;
  skipped: { name: string; reason: string }[];
}

export interface HealthStatus {
  status: string;
  probe?: { ok: boolean; last_seen?: string };
}

export interface WSMessage {
  type: "result";
  monitor_id: number;
  ts: string;
  latency_ms: number | null;
  success: boolean;
  status: "up" | "down" | "pending";
  slow?: boolean;
  event?: string;
  tls_expires_at?: string | null;
}
