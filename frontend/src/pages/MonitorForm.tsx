import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { Button } from "../components/ui/button";
import { Field, Input, Select, Textarea } from "../components/ui/controls";
import { FormSection } from "../components/ui/form-section";
import { TestRunPanel } from "../components/TestRunPanel";
import { ScheduleFields } from "../components/ScheduleFields";
import {
  TransactionEditor,
  emptyStep,
  secretsFromEditor,
  secretsToEditor,
  stepsFromEditor,
  stepsToEditor,
  type EditorSecret,
  type EditorStep,
} from "../components/TransactionEditor";
import { api, ApiError } from "../lib/api";
import { useMonitorStore } from "../lib/store";
import { useToast } from "../lib/toast";
import type { Monitor, MonitorConfig, NotificationChannel, ProbeNode, Realm } from "../lib/types";

const INTERVALS = [
  { value: 10, label: "10 seconds" },
  { value: 30, label: "30 seconds" },
  { value: 60, label: "1 minute" },
  { value: 120, label: "2 minutes" },
  { value: 300, label: "5 minutes" },
  { value: 600, label: "10 minutes" },
  { value: 1800, label: "30 minutes" },
  { value: 3600, label: "1 hour" },
  { value: 21600, label: "6 hours" },
  { value: 43200, label: "12 hours" },
  { value: 86400, label: "24 hours" },
];

const TYPES = [
  { value: "icmp", label: "ICMP ping" },
  { value: "http", label: "HTTP / HTTPS" },
  { value: "tcp", label: "TCP port" },
  { value: "dns", label: "DNS record" },
  { value: "cert", label: "TLS certificate" },
  { value: "domain", label: "Domain expiry" },
  { value: "smtp", label: "SMTP" },
  { value: "imap", label: "IMAP" },
  { value: "pop3", label: "POP3" },
  { value: "postgres", label: "PostgreSQL" },
  { value: "mysql", label: "MySQL" },
  { value: "redis", label: "Redis" },
  { value: "transaction", label: "Transaction (multi-step HTTP)" },
  { value: "push", label: "Push / heartbeat" },
  { value: "group", label: "Group" },
];

const DNS_RECORDS = ["A", "AAAA", "CNAME", "MX", "NS", "TXT"];
const MAIL_TYPES = ["smtp", "imap", "pop3"];
const DB_TYPES = ["postgres", "mysql", "redis"];
const isMail = (t: string) => MAIL_TYPES.includes(t);
const isDatabase = (t: string) => DB_TYPES.includes(t);
/** A check's own timeout default, so switching type does not carry a wrong one. */
const defaultTimeout = (t: string) => (t === "transaction" ? 30 : 10);

interface FormState {
  name: string;
  type: string;
  target: string;
  interval_seconds: number;
  retries: number;
  tags: string;
  escalate_minutes: number;
  escalate_recipients: string;
  method: string;
  expect_status: number;
  keyword: string;
  insecure_skip_verify: boolean;
  timeout_seconds: number;
  headers_text: string;
  body: string;
  json_path: string;
  json_expect: string;
  auth: string;
  auth_user: string;
  auth_password: string;
  auth_token: string;
  record: string;
  expect_ip: string;
  expect_value: string;
  warn_days: number;
  tls_mode: string;
  database: string;
  query: string;
  latency_threshold_ms: number;
  keyword_invert: boolean;
  proxy_url: string;
  grace_seconds: number;
  push_mode: "interval" | "schedule";
  schedule: string;
  timezone: string;
  resend_minutes: number;
  parent_id: number;
  depends_on: number;
  slo_target: number;
  probe_node_ids: number[];
  confirm_locations: number;
  realm_ids: number[];
  steps: EditorStep[];
  secrets: EditorSecret[];
}

const emptyForm = (): FormState => ({
  name: "",
  type: "icmp",
  target: "",
  interval_seconds: 60,
  retries: 3,
  tags: "",
  escalate_minutes: 0,
  escalate_recipients: "",
  method: "GET",
  expect_status: 0,
  keyword: "",
  insecure_skip_verify: false,
  timeout_seconds: 10,
  headers_text: "",
  body: "",
  json_path: "",
  json_expect: "",
  auth: "",
  auth_user: "",
  auth_password: "",
  auth_token: "",
  record: "A",
  expect_ip: "",
  expect_value: "",
  warn_days: 0,
  tls_mode: "none",
  database: "",
  query: "",
  latency_threshold_ms: 0,
  keyword_invert: false,
  proxy_url: "",
  grace_seconds: 0,
  push_mode: "interval",
  schedule: "",
  timezone: "",
  resend_minutes: 0,
  parent_id: 0,
  depends_on: 0,
  slo_target: 99.9,
  probe_node_ids: [],
  confirm_locations: 1,
  realm_ids: [],
  steps: [emptyStep()],
  secrets: [],
});

function headersFromText(text: string): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const idx = line.indexOf(":");
    if (idx <= 0) continue;
    const key = line.slice(0, idx).trim();
    const value = line.slice(idx + 1).trim();
    if (key) out[key] = value;
  }
  return Object.keys(out).length ? out : undefined;
}

function headersToText(headers?: Record<string, string>): string {
  if (!headers) return "";
  return Object.entries(headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n");
}

function targetHint(type: string): { label: string; hint: string; placeholder: string } {
  switch (type) {
    case "http":
      return {
        label: "URL",
        hint: "Full http or https URL. GET, HEAD, or POST; optional keyword, JSON path, and auth.",
        placeholder: "https://app.internal/health",
      };
    case "cert":
      return {
        label: "Host",
        hint: "HTTPS URL or host:port. Handshake only — no HTTP assertions.",
        placeholder: "example.com:443",
      };
    case "domain":
      return {
        label: "Domain",
        hint: "Registration expiry over RDAP. This is the domain itself, not its certificate.",
        placeholder: "example.com",
      };
    case "smtp":
      return { label: "Mail server", hint: "Host, or host:port. Default 25, or 465 with TLS.", placeholder: "mail.example.com" };
    case "imap":
      return { label: "Mail server", hint: "Host, or host:port. Default 143, or 993 with TLS.", placeholder: "imap.example.com" };
    case "pop3":
      return { label: "Mail server", hint: "Host, or host:port. Default 110, or 995 with TLS.", placeholder: "pop.example.com" };
    case "postgres":
      return { label: "Server", hint: "Host, or host:port. Default 5432.", placeholder: "db.internal:5432" };
    case "mysql":
      return { label: "Server", hint: "Host, or host:port. Default 3306.", placeholder: "db.internal:3306" };
    case "redis":
      return { label: "Server", hint: "Host, or host:port. Default 6379.", placeholder: "cache.internal:6379" };
    case "push":
      return {
        label: "Label",
        hint: "What this heartbeat is for (cron job, backup). Argus generates a push URL after save.",
        placeholder: "nightly backup",
      };
    case "group":
      return {
        label: "Label",
        hint: "A folder. Child monitors pick this group as their parent. Status is the worst child.",
        placeholder: "Production",
      };
    case "tcp":
      return { label: "Host and port", hint: "TCP connect check.", placeholder: "db.internal:5432" };
    case "dns":
      return { label: "Hostname", hint: "Looks up A or AAAA. Optional expected IP.", placeholder: "app.internal" };
    default:
      return {
        label: "IP address or hostname",
        hint: "ICMP echo (ping).",
        placeholder: "10.0.0.1 or host.example.com",
      };
  }
}

export function MonitorForm() {
  const { id } = useParams();
  const editing = id !== undefined;
  const monitorId = editing ? Number(id) : null;
  const navigate = useNavigate();
  const { toast } = useToast();
  const { refresh } = useMonitorStore();
  const [form, setForm] = useState<FormState>(emptyForm);
  const [loading, setLoading] = useState(editing);
  const [saving, setSaving] = useState(false);
  const [availableChannels, setAvailableChannels] = useState<NotificationChannel[]>([]);
  const [channelIDs, setChannelIDs] = useState<number[] | null>(null);
  const [allMonitors, setAllMonitors] = useState<Monitor[]>([]);
  const [probeNodes, setProbeNodes] = useState<ProbeNode[]>([]);
  const [realms, setRealms] = useState<Realm[]>([]);

  useEffect(() => {
    if (!editing) return;
    api.monitors
      .get(monitorId!)
      .then((m: Monitor) => {
        const cfg = m.config ?? {};
        setForm({
          name: m.name,
          type: m.type || "icmp",
          target: m.target,
          interval_seconds: m.interval_seconds,
          retries: m.retries,
          tags: m.tags,
          escalate_minutes: m.escalate_minutes,
          escalate_recipients: m.escalate_recipients,
          method: cfg.method || "GET",
          expect_status: cfg.expect_status ?? 0,
          keyword: cfg.keyword || "",
          insecure_skip_verify: !!cfg.insecure_skip_verify,
          timeout_seconds: cfg.timeout_seconds || 10,
          headers_text: headersToText(cfg.headers),
          body: cfg.body || "",
          json_path: cfg.json_path || "",
          json_expect: cfg.json_expect || "",
          auth: cfg.auth || "",
          auth_user: cfg.auth_user || "",
          auth_password: cfg.auth_password || "",
          auth_token: cfg.auth_token || "",
          record: cfg.record || "A",
          expect_ip: cfg.expect_ip || "",
          expect_value: cfg.expect_value || "",
          warn_days: cfg.warn_days || 0,
          tls_mode: cfg.tls_mode || "none",
          database: cfg.database || "",
          query: cfg.query || "",
          latency_threshold_ms: m.latency_threshold_ms || 0,
          keyword_invert: !!cfg.keyword_invert,
          proxy_url: cfg.proxy_url || "",
          grace_seconds: cfg.grace_seconds || 0,
          push_mode: cfg.schedule ? "schedule" : "interval",
          schedule: cfg.schedule || "",
          timezone: cfg.timezone || "",
          resend_minutes: m.resend_minutes || 0,
          parent_id: m.parent_id || 0,
          depends_on: m.depends_on || 0,
          slo_target: m.slo_target || 99.9,
          probe_node_ids: m.probe_node_ids ?? [],
          confirm_locations: m.confirm_locations ?? 1,
          realm_ids: m.realm_ids ?? [],
          steps: stepsToEditor(cfg.steps),
          secrets: secretsToEditor(cfg.secrets),
        });
        setChannelIDs(m.channel_ids ?? null);
      })
      .catch(() => toast("error", "failed to load monitor"))
      .finally(() => setLoading(false));
  }, [editing, monitorId, toast]);

  useEffect(() => {
    api.channels
      .list()
      .then(setAvailableChannels)
      .catch(() => setAvailableChannels([]));
    api.monitors
      .list()
      .then(setAllMonitors)
      .catch(() => setAllMonitors([]));
    api.probeNodes
      .list()
      .then(setProbeNodes)
      .catch(() => setProbeNodes([]));
    api.realms
      .list()
      .then(setRealms)
      .catch(() => setRealms([]));
  }, []);

  const hint = targetHint(form.type);
  const assertionsOpen =
    editing &&
    (form.expect_status !== 0 ||
      !!form.keyword ||
      !!form.json_path ||
      !!form.auth ||
      !!form.headers_text ||
      !!form.proxy_url ||
      form.insecure_skip_verify ||
      !!form.expect_ip ||
      form.grace_seconds > 0 ||
      form.latency_threshold_ms > 0 ||
      (form.type !== "icmp" &&
        form.type !== "group" &&
        form.type !== "push" &&
        form.timeout_seconds !== defaultTimeout(form.type)));
  const alertingOpen =
    editing && (form.escalate_minutes > 0 || form.resend_minutes > 0 || channelIDs !== null);
  const inventoryOpen =
    editing &&
    (!!form.tags.trim() || form.realm_ids.length > 0 || form.parent_id > 0 || form.depends_on > 0);
  const moreOpen = editing && (form.probe_node_ids.length > 0 || form.slo_target !== 99.9);
  // Where the check runs: the chosen locations, or this server alone.
  const locations = form.probe_node_ids.length ? [...new Set(form.probe_node_ids)] : [0];
  const localOnlyType = form.type === "push" || form.type === "group";
  const toggleLocation = (id: number) => {
    const next = locations.includes(id) ? locations.filter((x) => x !== id) : [...locations, id];
    if (next.length === 0) return; // somewhere has to check it
    const canonical = next.length === 1 && next[0] === 0 ? [] : next;
    setForm((f) => ({
      ...f,
      probe_node_ids: canonical,
      confirm_locations: Math.min(f.confirm_locations, next.length),
    }));
  };

  const set = <K extends keyof FormState>(key: K, value: FormState[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const configForType = (): MonitorConfig => {
    if (form.type === "http") {
      const cfg: MonitorConfig = {
        method: form.method,
        expect_status: form.expect_status,
        keyword: form.keyword,
        keyword_invert: form.keyword_invert,
        insecure_skip_verify: form.insecure_skip_verify,
        timeout_seconds: form.timeout_seconds,
        headers: headersFromText(form.headers_text),
        proxy_url: form.proxy_url || undefined,
      };
      if (form.method === "POST" && form.body) cfg.body = form.body;
      if (form.method !== "HEAD" && form.json_path) {
        cfg.json_path = form.json_path;
        cfg.json_expect = form.json_expect;
      }
      if (form.auth === "basic") {
        cfg.auth = "basic";
        cfg.auth_user = form.auth_user;
        cfg.auth_password = form.auth_password;
      } else if (form.auth === "bearer") {
        cfg.auth = "bearer";
        cfg.auth_token = form.auth_token;
      }
      return cfg;
    }
    if (form.type === "tcp") {
      return { timeout_seconds: form.timeout_seconds };
    }
    if (form.type === "dns") {
      const addressRecord = form.record === "A" || form.record === "AAAA";
      return {
        record: form.record,
        expect_ip: addressRecord ? form.expect_ip : "",
        expect_value: addressRecord ? "" : form.expect_value,
        timeout_seconds: form.timeout_seconds,
      };
    }
    if (form.type === "cert") {
      return {
        timeout_seconds: form.timeout_seconds,
        insecure_skip_verify: form.insecure_skip_verify,
        warn_days: form.warn_days || undefined,
      };
    }
    if (form.type === "domain") {
      return { timeout_seconds: form.timeout_seconds, warn_days: form.warn_days || undefined };
    }
    if (isMail(form.type)) {
      return {
        timeout_seconds: form.timeout_seconds,
        tls_mode: form.tls_mode,
        keyword: form.keyword,
        keyword_invert: form.keyword_invert,
        insecure_skip_verify: form.insecure_skip_verify,
      };
    }
    if (isDatabase(form.type)) {
      const cfg: MonitorConfig = {
        timeout_seconds: form.timeout_seconds,
        auth_user: form.auth_user,
        database: form.database || undefined,
        query: form.query || undefined,
        expect_value: form.expect_value || undefined,
        insecure_skip_verify: form.insecure_skip_verify,
      };
      if (form.auth_password) cfg.auth_password = form.auth_password;
      if (form.type === "redis") cfg.tls_mode = form.tls_mode === "tls" ? "tls" : "none";
      return cfg;
    }
    if (form.type === "push") {
      return form.push_mode === "schedule"
        ? { grace_seconds: form.grace_seconds, schedule: form.schedule.trim(), timezone: form.timezone || "UTC" }
        : { grace_seconds: form.grace_seconds };
    }
    if (form.type === "transaction") {
      return {
        timeout_seconds: form.timeout_seconds,
        insecure_skip_verify: form.insecure_skip_verify,
        steps: stepsFromEditor(form.steps),
        secrets: secretsFromEditor(form.secrets),
      };
    }
    return {};
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const body: Partial<Monitor> = {
        name: form.name,
        // A transaction's target is its first step; the server sets it.
        target: form.type === "transaction" ? "" : form.target,
        type: form.type,
        interval_seconds: form.interval_seconds,
        retries: form.retries,
        tags: form.tags,
        enabled: true,
        latency_threshold_ms: form.latency_threshold_ms || 0,
        resend_minutes: form.resend_minutes || 0,
        parent_id: form.parent_id || null,
        depends_on: form.depends_on || null,
        channel_ids: channelIDs,
        config: configForType(),
        escalate_minutes: form.escalate_minutes,
        escalate_recipients: form.escalate_recipients,
        slo_target: form.slo_target,
        probe_node_ids: localOnlyType ? [] : form.probe_node_ids,
        confirm_locations: localOnlyType ? 1 : Math.min(form.confirm_locations, locations.length),
        realm_ids: form.realm_ids,
      };
      const saved =
        editing ? await api.monitors.update(monitorId!, body) : await api.monitors.create(body);
      await refresh();
      toast("success", editing ? "monitor updated" : "monitor created");
      navigate(`/monitors/${saved.id}`, { replace: true });
    } catch (err) {
      // A lost session is handled once, for every page (see lib/api.ts).
      if (err instanceof ApiError && err.status === 401) return;
      toast("error", err instanceof ApiError ? err.message : "failed to save monitor");
    } finally {
      setSaving(false);
    }
  };

  if (loading) {
    return (
      <div className="flex justify-center py-24">
        <Loader2 size={24} className="animate-spin text-muted-foreground" />
      </div>
    );
  }

  return (
    <div className="animate-fade-in-up mx-auto max-w-2xl">
      <h1 className="font-brand mb-1 text-xl tracking-tight">
        {editing ? "Edit monitor" : "New monitor"}
      </h1>
      <p className="mb-5 text-sm text-muted-foreground">
        Name it, say what to check, and Argus starts watching. Everything below
        the basics is optional.
      </p>

      <form
        onSubmit={submit}
        aria-busy={saving}
        className="surface flex flex-col gap-5 p-6"
      >
        <Field label="Name">
          <Input
            value={form.name}
            onChange={(e) => set("name", e.target.value)}
            placeholder="Web server"
            required
            autoFocus
          />
        </Field>

        <Field label="Type">
          <Select
            value={form.type}
            onChange={(e) => {
              const next = e.target.value;
              setForm((f) => ({
                ...f,
                type: next,
                timeout_seconds:
                  f.timeout_seconds === defaultTimeout(f.type) ? defaultTimeout(next) : f.timeout_seconds,
              }));
            }}
          >
            {TYPES.map((t) => (
              <option key={t.value} value={t.value}>
                {t.label}
              </option>
            ))}
          </Select>
        </Field>

        {form.type !== "transaction" && (
          <Field label={hint.label} hint={hint.hint}>
            <Input
              value={form.target}
              onChange={(e) => set("target", e.target.value)}
              placeholder={hint.placeholder}
              required={form.type !== "group"}
            />
          </Field>
        )}

        {form.type === "push" && (
          <ScheduleFields
            mode={form.push_mode}
            schedule={form.schedule}
            timezone={form.timezone}
            grace={form.grace_seconds}
            onChange={(patch) => setForm((f) => ({ ...f, ...patch }))}
          />
        )}

        {form.type === "transaction" && (
            <>
              <TransactionEditor
                steps={form.steps}
                secrets={form.secrets}
                editing={editing}
                onSteps={(steps) => set("steps", steps)}
                onSecrets={(secrets) => set("secrets", secrets)}
              />
              <TestRunPanel
                run={() =>
                  api.monitors.testRun({
                    name: form.name,
                    type: "transaction",
                    interval_seconds: form.interval_seconds,
                    config: configForType(),
                    monitor_id: monitorId ?? undefined,
                  })
                }
              />
            </>
          )}

        <div className="grid grid-cols-2 gap-4">
          {form.type === "push" && form.push_mode === "schedule" ? (
            <div>
              <div className="text-xs font-medium text-muted-foreground">Check interval</div>
              <p className="mt-2 text-sm text-muted-foreground">Checked every minute against the schedule.</p>
            </div>
          ) : (
            <Field label="Check interval">
              <Select
                value={form.interval_seconds}
                onChange={(e) => set("interval_seconds", Number(e.target.value))}
              >
                {INTERVALS.map((i) => (
                  <option key={i.value} value={i.value}>
                    {i.label}
                  </option>
                ))}
              </Select>
            </Field>
          )}
          <Field label="Failures before alert" hint="Consecutive failed checks.">
            <Input
              type="number"
              min={1}
              max={10}
              value={form.retries}
              onChange={(e) => set("retries", Number(e.target.value))}
            />
          </Field>
        </div>

        <FormSection
          title="Assertions"
          hint="Optional checks on the response. Leave closed for a plain up/down ping."
          defaultOpen={assertionsOpen}
        >
        {form.type === "http" && (
          <div className="grid grid-cols-2 gap-4">
            <Field label="Method">
              <Select value={form.method} onChange={(e) => set("method", e.target.value)}>
                <option value="GET">GET</option>
                <option value="HEAD">HEAD</option>
                <option value="POST">POST</option>
              </Select>
            </Field>
            <Field label="Expected status" hint="0 = any 2xx or 3xx">
              <Input
                type="number"
                min={0}
                max={599}
                value={form.expect_status}
                onChange={(e) => set("expect_status", Number(e.target.value))}
              />
            </Field>
            {form.method === "POST" && (
              <Field label="Body (optional)" hint="Sent as the POST body. Default Content-Type is application/json." className="col-span-2">
                <Textarea
                  value={form.body}
                  onChange={(e) => set("body", e.target.value)}
                  placeholder='{"check":true}'
                />
              </Field>
            )}
            {form.method !== "HEAD" && (
              <>
                <Field label="JSON path (optional)" hint="Dotted object keys, e.g. status or data.health">
                  <Input
                    value={form.json_path}
                    onChange={(e) => set("json_path", e.target.value)}
                    placeholder="status"
                  />
                </Field>
                <Field label="JSON expect" hint="Leaf compared as a string. Required when a path is set.">
                  <Input
                    value={form.json_expect}
                    onChange={(e) => set("json_expect", e.target.value)}
                    placeholder="ok"
                    disabled={!form.json_path}
                  />
                </Field>
              </>
            )}
            <Field label="Keyword (optional)" className="col-span-2">
              <Input
                value={form.keyword}
                onChange={(e) => set("keyword", e.target.value)}
                placeholder="ok"
              />
            </Field>
            {form.keyword !== "" && (
              <label className="col-span-2 flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={form.keyword_invert}
                  onChange={(e) => set("keyword_invert", e.target.checked)}
                />
                Invert keyword (fail if the keyword is present)
              </label>
            )}
            <Field label="HTTP proxy (optional)" hint="http or https proxy URL for this check." className="col-span-2">
              <Input
                value={form.proxy_url}
                onChange={(e) => set("proxy_url", e.target.value)}
                placeholder="http://proxy.internal:3128"
              />
            </Field>
            <Field label="Auth" hint="Prefer this over an Authorization header.">
              <Select value={form.auth} onChange={(e) => set("auth", e.target.value)}>
                <option value="">None</option>
                <option value="basic">Basic</option>
                <option value="bearer">Bearer</option>
              </Select>
            </Field>
            {form.auth === "basic" && (
              <>
                <Field label="Username">
                  <Input
                    value={form.auth_user}
                    onChange={(e) => set("auth_user", e.target.value)}
                    autoComplete="off"
                  />
                </Field>
                <Field label="Password" className="col-span-2" hint={editing ? "Leave blank to keep the stored password." : undefined}>
                  <Input
                    type="password"
                    value={form.auth_password}
                    onChange={(e) => set("auth_password", e.target.value)}
                    autoComplete="off"
                    placeholder={editing ? "leave blank to keep" : ""}
                  />
                </Field>
              </>
            )}
            {form.auth === "bearer" && (
              <Field label="Token" className="col-span-2" hint={editing ? "Leave blank to keep the stored token." : undefined}>
                <Input
                  type="password"
                  value={form.auth_token}
                  onChange={(e) => set("auth_token", e.target.value)}
                  autoComplete="off"
                  placeholder={editing ? "leave blank to keep" : ""}
                />
              </Field>
            )}
            <Field label="Headers (optional)" hint="One Name: value per line. Do not set Authorization if Auth is used." className="col-span-2">
              <Textarea
                value={form.headers_text}
                onChange={(e) => set("headers_text", e.target.value)}
                placeholder="X-Request-Id: …"
              />
            </Field>
            <label className="col-span-2 flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.insecure_skip_verify}
                onChange={(e) => set("insecure_skip_verify", e.target.checked)}
              />
              Skip TLS certificate verify
            </label>
          </div>
        )}

        {form.type === "dns" && (
          <div className="grid grid-cols-2 gap-4">
            <Field label="Record">
              <Select value={form.record} onChange={(e) => set("record", e.target.value)}>
                {DNS_RECORDS.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </Select>
            </Field>
            {form.record === "A" || form.record === "AAAA" ? (
              <Field label="Expected IP (optional)">
                <Input
                  value={form.expect_ip}
                  onChange={(e) => set("expect_ip", e.target.value)}
                  placeholder="10.0.0.10"
                />
              </Field>
            ) : (
              <Field
                label="Expected value (optional)"
                hint={
                  form.record === "TXT"
                    ? "Matched as a substring, so a fragment of an SPF record works."
                    : form.record === "MX"
                      ? "Exact answer, for example: 10 mail.example.com"
                      : "Exact answer."
                }
              >
                <Input
                  value={form.expect_value}
                  onChange={(e) => set("expect_value", e.target.value)}
                  placeholder={form.record === "TXT" ? "v=spf1" : "mail.example.com"}
                />
              </Field>
            )}
          </div>
        )}

        {form.type === "domain" && (
          <Field
            label="Warn this many days before expiry"
            hint="Leave 0 for the default of 30 days. Argus alerts once per expiry date."
          >
            <Input
              type="number"
              min={0}
              max={365}
              value={form.warn_days}
              onChange={(e) => set("warn_days", Number(e.target.value))}
            />
          </Field>
        )}

        {isMail(form.type) && (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-4">
              <Field label="Connection">
                <Select value={form.tls_mode} onChange={(e) => set("tls_mode", e.target.value)}>
                  <option value="none">Plain</option>
                  <option value="starttls">STARTTLS</option>
                  <option value="tls">TLS (implicit)</option>
                </Select>
              </Field>
              <Field label="Greeting contains (optional)" hint="Checked against the server's banner.">
                <Input
                  value={form.keyword}
                  onChange={(e) => set("keyword", e.target.value)}
                  placeholder="Postfix"
                />
              </Field>
            </div>
            {form.tls_mode !== "none" && (
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={form.insecure_skip_verify}
                  onChange={(e) => set("insecure_skip_verify", e.target.checked)}
                />
                Skip TLS certificate verify (still reads expiry)
              </label>
            )}
          </div>
        )}

        {isDatabase(form.type) && (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-4">
              <Field label={form.type === "redis" ? "Username (optional)" : "Username"}>
                <Input
                  value={form.auth_user}
                  onChange={(e) => set("auth_user", e.target.value)}
                  autoComplete="off"
                  placeholder={form.type === "redis" ? "" : "argus_monitor"}
                />
              </Field>
              <Field
                label="Password"
                hint={editing ? "Leave blank to keep the stored password." : "Stored encrypted when ARGUS_SECRET_KEY is set."}
              >
                <Input
                  type="password"
                  value={form.auth_password}
                  onChange={(e) => set("auth_password", e.target.value)}
                  autoComplete="new-password"
                />
              </Field>
            </div>
            {form.type !== "redis" && (
              <Field label="Database">
                <Input
                  value={form.database}
                  onChange={(e) => set("database", e.target.value)}
                  placeholder="app"
                />
              </Field>
            )}
            <div className="grid grid-cols-2 gap-4">
              <Field
                label={form.type === "redis" ? "Command (optional)" : "Query (optional)"}
                hint={
                  form.type === "redis"
                    ? "A read command such as GET health or DBSIZE. Defaults to PING."
                    : "Returns one value. Defaults to SELECT 1."
                }
              >
                <Input
                  value={form.query}
                  onChange={(e) => set("query", e.target.value)}
                  placeholder={form.type === "redis" ? "GET health" : "SELECT 1"}
                />
              </Field>
              <Field label="Expected result (optional)">
                <Input
                  value={form.expect_value}
                  onChange={(e) => set("expect_value", e.target.value)}
                  placeholder="ok"
                />
              </Field>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.insecure_skip_verify}
                onChange={(e) => set("insecure_skip_verify", e.target.checked)}
              />
              {form.type === "redis" ? "Plain connection (no TLS)" : "Do not require TLS to the database"}
            </label>
          </div>
        )}

        {form.type === "cert" && (
          <div className="flex flex-col gap-4">
            <Field label="Warn this many days before expiry" hint="Leave 0 for the default of 14 days.">
              <Input
                type="number"
                min={0}
                max={365}
                value={form.warn_days}
                onChange={(e) => set("warn_days", Number(e.target.value))}
              />
            </Field>
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={form.insecure_skip_verify}
                onChange={(e) => set("insecure_skip_verify", e.target.checked)}
              />
              Skip TLS certificate verify (still reads expiry)
            </label>
          </div>
        )}

        {form.type === "push" && form.push_mode === "interval" && (
          <Field
            label="Grace (seconds)"
            hint="Extra time after the interval before a missed heartbeat is down. 0 = interval only."
          >
            <Input
              type="number"
              min={0}
              max={86400}
              value={form.grace_seconds}
              onChange={(e) => set("grace_seconds", Number(e.target.value))}
            />
          </Field>
        )}

        {form.type === "transaction" && (
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={form.insecure_skip_verify}
              onChange={(e) => set("insecure_skip_verify", e.target.checked)}
            />
            Skip TLS certificate verify on every step
          </label>
        )}

        {form.type !== "icmp" && form.type !== "group" && form.type !== "push" && (
          <Field
            label="Timeout (seconds)"
            hint={form.type === "transaction" ? "For the whole transaction, not each step." : undefined}
          >
            <Input
              type="number"
              min={1}
              max={120}
              value={form.timeout_seconds}
              onChange={(e) => set("timeout_seconds", Number(e.target.value))}
            />
          </Field>
        )}

        {form.type !== "group" && (
        <Field
          label="Latency threshold (ms)"
          hint="Alert as slow after the same number of consecutive successful checks above this. Empty = off. Timeouts are still down."
        >
          <Input
            type="number"
            min={0}
            max={600000}
            value={form.latency_threshold_ms || ""}
            onChange={(e) =>
              set("latency_threshold_ms", e.target.value === "" ? 0 : Number(e.target.value))
            }
            placeholder="off"
          />
        </Field>
        )}
        </FormSection>

        <FormSection
          title="Alerting"
          hint="Who gets paged, and when to escalate or remind."
          defaultOpen={alertingOpen}
        >
        <fieldset>
          <legend className="mb-1 text-sm font-medium">Notification channels</legend>
          <p className="mb-3 text-xs text-muted-foreground">
            Leave all checked to use the default set (including channels added later). Uncheck
            to attach a subset — e.g. latency alerts on the webserver only. Add Email, Slack,
            Discord, or webhook channels in Settings.
          </p>
          {availableChannels.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No named channels. Add Email, Slack, Discord, or a webhook in Settings.
            </p>
          ) : (
            <div className="flex flex-col gap-2">
              {availableChannels.map((ch) => {
                const checked = channelIDs === null || channelIDs.includes(ch.id);
                return (
                  <label key={ch.id} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => {
                        const allIds = availableChannels.map((c) => c.id);
                        const current = channelIDs ?? allIds;
                        const next = current.includes(ch.id)
                          ? current.filter((id) => id !== ch.id)
                          : [...current, ch.id];
                        const isDefault =
                          next.length === allIds.length && allIds.every((id) => next.includes(id));
                        setChannelIDs(isDefault ? null : next);
                      }}
                    />
                    <span className="font-medium">{ch.name}</span>
                    <span className="text-xs text-muted-foreground">{ch.type}</span>
                  </label>
                );
              })}
            </div>
          )}
        </fieldset>

        <div className="flex flex-col gap-4">
          <div className="text-xs font-semibold">Escalation</div>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Escalate after (minutes)" hint="0 disables. One secondary email list, fired once after this delay.">
              <Input
                type="number"
                min={0}
                max={10080}
                value={form.escalate_minutes}
                onChange={(e) => set("escalate_minutes", Number(e.target.value))}
              />
            </Field>
            <Field
              label="Resend every (minutes)"
              hint="0 disables. Re-sends a still-down reminder while the monitor stays down."
            >
              <Input
                type="number"
                min={0}
                max={10080}
                value={form.resend_minutes}
                onChange={(e) => set("resend_minutes", Number(e.target.value))}
              />
            </Field>
            <Field label="Escalation recipients" className="col-span-2">
              <Textarea
                value={form.escalate_recipients}
                onChange={(e) => set("escalate_recipients", e.target.value)}
                placeholder="manager@example.com"
                className="min-h-9 h-9"
              />
            </Field>
          </div>
        </div>
        </FormSection>

        <FormSection title="Inventory" hint="Tags, environments, and grouping. Filter-only — not required to start watching." defaultOpen={inventoryOpen}>
        {form.type !== "group" && (
          <div className="grid grid-cols-2 gap-4">
            <Field label="Group" hint="Optional parent group.">
              <Select
                value={String(form.parent_id)}
                onChange={(e) => set("parent_id", Number(e.target.value))}
              >
                <option value={0}>None</option>
                {allMonitors
                  .filter((m) => m.type === "group" && m.id !== monitorId)
                  .map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name}
                    </option>
                  ))}
              </Select>
            </Field>
            <Field label="Depends on" hint="Suppress alerts while this monitor is down.">
              <Select
                value={String(form.depends_on)}
                onChange={(e) => set("depends_on", Number(e.target.value))}
              >
                <option value={0}>None</option>
                {allMonitors
                  .filter((m) => m.type !== "group" && m.id !== monitorId)
                  .map((m) => (
                    <option key={m.id} value={m.id}>
                      {m.name}
                    </option>
                  ))}
              </Select>
            </Field>
          </div>
        )}

        <Field label="Tags (optional)" hint="Comma-separated, e.g. prod, edge. Used on the Monitors filter bar.">
          <Input
            value={form.tags}
            onChange={(e) => set("tags", e.target.value)}
            placeholder="prod, core"
          />
        </Field>

        <Field
          label="Environments"
          hint="A monitor can sit in more than one realm. Create realms on the Realms page."
        >
          {realms.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No realms yet. Create one on the Realms page, then assign it here.
            </p>
          ) : (
            <div className="flex flex-col gap-1.5 text-sm">
              {realms.map((r) => (
                <label key={r.id} className="flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={form.realm_ids.includes(r.id)}
                    onChange={() => {
                      const has = form.realm_ids.includes(r.id);
                      set(
                        "realm_ids",
                        has ? form.realm_ids.filter((id) => id !== r.id) : [...form.realm_ids, r.id],
                      );
                    }}
                  />
                  {r.name}
                </label>
              ))}
            </div>
          )}
        </Field>
        </FormSection>

        <FormSection title="SLO and probes" defaultOpen={moreOpen}>
        <Field label="SLO target (%)" hint="Used by the SLA report.">
          <Input
            type="number"
            min={50}
            max={100}
            step={0.001}
            value={form.slo_target}
            onChange={(e) => set("slo_target", Number(e.target.value))}
          />
        </Field>

        {probeNodes.length > 0 && !localOnlyType && (
          <div className="flex flex-col gap-4">
            <fieldset>
              <legend className="mb-1 text-sm font-medium">Check from</legend>
              <p className="mb-2 text-xs text-muted-foreground">
                Each location runs the check on its own schedule and counts its own failures.
              </p>
              <div className="flex flex-col gap-1.5 text-sm">
                <label className="flex items-center gap-2">
                  <input type="checkbox" checked={locations.includes(0)} onChange={() => toggleLocation(0)} />
                  This server
                </label>
                {probeNodes.map((n) => (
                  <label key={n.id} className="flex items-center gap-2">
                    <input
                      type="checkbox"
                      checked={locations.includes(n.id)}
                      onChange={() => toggleLocation(n.id)}
                    />
                    {n.name}
                    {n.region && <span className="text-muted-foreground">({n.region})</span>}
                  </label>
                ))}
              </div>
            </fieldset>
            {locations.length > 1 && (
              <Field
                label="Mark down when"
                hint="Requiring agreement stops one location's network trouble from paging you. A location that stops reporting drops out of the count."
              >
                <Select
                  value={Math.min(form.confirm_locations, locations.length)}
                  onChange={(e) => set("confirm_locations", Number(e.target.value))}
                >
                  {Array.from({ length: locations.length }, (_, i) => i + 1).map((n) => (
                    <option key={n} value={n}>
                      {n === 1
                        ? "Any one location confirms it"
                        : n === locations.length
                          ? `All ${n} locations agree`
                          : `${n} of ${locations.length} locations agree`}
                    </option>
                  ))}
                </Select>
              </Field>
            )}
          </div>
        )}
        </FormSection>

        <div className="sticky bottom-0 -mx-6 -mb-6 flex justify-end gap-2 border-t border-border bg-card/95 px-6 py-4 backdrop-blur">
          <Button type="button" variant="outline" onClick={() => navigate(-1)}>
            Cancel
          </Button>
          <Button type="submit" disabled={saving}>
            {saving && <Loader2 size={14} className="animate-spin" />}
            {editing ? "Save changes" : "Create monitor"}
          </Button>
        </div>
      </form>
    </div>
  );
}
