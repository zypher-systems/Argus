import { useCallback, useEffect, useState, type FormEvent } from "react";
import { NavLink, useParams } from "react-router-dom";
import { Loader2, Mail, Send, Trash2 } from "lucide-react";
import { Button } from "../components/ui/button";
import { ConfirmButton } from "../components/ui/confirm-button";
import { Badge } from "../components/ui/badge";
import { Dialog } from "../components/ui/dialog";
import { Field, Input, Select } from "../components/ui/controls";
import { api, ApiError } from "../lib/api";
import { useSettings } from "../lib/settings";
import { useToast } from "../lib/toast";
import {
  CHANNEL_EVENTS,
  CHANNEL_TYPES,
  type AppUser,
  type ChannelType,
  type MaintenanceWindow,
  type NotificationChannel,
  type OIDCSettings,
  type Organization,
  type ProbeNode,
  type SMTPSettings,
  type APIToken,
} from "../lib/types";
import { formatDateTime } from "../lib/utils";
import { isAdmin, useAuth } from "../lib/auth";

function Section({
  title,
  readOnly,
  changedBy = "An admin or operator",
  children,
}: {
  title: string;
  /** Shown but not changeable by this user's role. */
  readOnly?: boolean;
  /** Who can change it, for the note shown when this user cannot. */
  changedBy?: string;
  children: React.ReactNode;
}) {
  return (
    <section className="surface mb-5 p-6">
      <div className="mb-5 flex items-center gap-2">
        <h2 className="text-sm font-semibold">{title}</h2>
      </div>
      {readOnly && (
        <p className="-mt-3 mb-4 text-xs text-muted-foreground">
          View only. {changedBy} can change this.
        </p>
      )}
      <div>
        {readOnly ? (
          // A disabled fieldset disables every control inside it, so nothing
          // offers an action the server would refuse.
          <fieldset disabled className="m-0 min-w-0 border-0 p-0">
            {children}
          </fieldset>
        ) : (
          children
        )}
      </div>
    </section>
  );
}

function downloadJSON(data: unknown, filename: string) {
  const blob = new Blob([JSON.stringify(data, null, 2)], { type: "application/json" });
  const a = document.createElement("a");
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  a.click();
  URL.revokeObjectURL(a.href);
}

export function Settings() {
  const { settings, reload } = useSettings();
  const { toast } = useToast();
  const { user } = useAuth();
  const admin = isAdmin(user);
  // Viewers can read settings and change only their own password.
  const viewOnly = user?.role === "viewer";
  const { section: sectionParam } = useParams();
  const section =
    sectionParam === "access" ||
    sectionParam === "probes" ||
    sectionParam === "data"
      ? sectionParam
      : "alerts";
  const tabClass = ({ isActive }: { isActive: boolean }) =>
    `flex h-11 items-center rounded-lg px-3 text-sm transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring md:h-10 ${
      isActive
        ? "bg-muted font-semibold text-foreground"
        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground"
    }`;

  const [smtp, setSmtp] = useState({
    host: "",
    port: 587,
    username: "",
    password: "",
    from: "",
    tls_mode: "starttls" as SMTPSettings["tls_mode"],
    global_recipients: "",
  });
  const [smtpSaving, setSmtpSaving] = useState(false);
  const [testOpen, setTestOpen] = useState(false);
  const [testTo, setTestTo] = useState("");
  const [testing, setTesting] = useState(false);

  const emptyChannelDraft = {
    name: "",
    type: "webhook" as ChannelType,
    dest: "",
    events: CHANNEL_EVENTS.map((e) => e.value) as string[],
  };
  const [channelList, setChannelList] = useState<NotificationChannel[] | null>(null);
  const [channelDraft, setChannelDraft] = useState(emptyChannelDraft);
  const [editingChannel, setEditingChannel] = useState<number | null>(null);
  const [channelsSaving, setChannelsSaving] = useState(false);
  const [channelTesting, setChannelTesting] = useState<number | null>(null);
  const channelMeta =
    CHANNEL_TYPES.find((t) => t.value === channelDraft.type) ?? CHANNEL_TYPES[0];

  const [baseUrl, setBaseUrl] = useState("");
  const [notifyMax, setNotifyMax] = useState(0);
  const [tokens, setTokens] = useState<APIToken[] | null>(null);
  const [tokenName, setTokenName] = useState("");
  const [newToken, setNewToken] = useState<string | null>(null);
  const [importResult, setImportResult] = useState<string | null>(null);
  const [exportPassphrase, setExportPassphrase] = useState("");
  const [importPassphrase, setImportPassphrase] = useState("");
  const [branding, setBranding] = useState({
    product_name: "",
    logo_url: "",
    accent_color: "",
    login_message: "",
  });
  const [retention, setRetention] = useState({ raw_days: 7, hourly_days: 90, daily_days: 365 });
  const [reportsOn, setReportsOn] = useState(false);
  const [reportRecipients, setReportRecipients] = useState("");
  const [reportSending, setReportSending] = useState(false);

  const [org, setOrg] = useState<Organization | null>(null);
  const [orgName, setOrgName] = useState("");
  const [orgSaving, setOrgSaving] = useState(false);
  const [registrationOn, setRegistrationOn] = useState(false);
  const [pw, setPw] = useState({ current: "", next: "", confirm: "" });
  const [pwSaving, setPwSaving] = useState(false);

  const [windows, setWindows] = useState<MaintenanceWindow[] | null>(null);
  const [newWindow, setNewWindow] = useState({ starts_at: "", ends_at: "", note: "", public: false });

  useEffect(() => {
    if (!settings) return;
    setSmtp({
      host: settings.smtp.host,
      port: settings.smtp.port,
      username: settings.smtp.username,
      password: "",
      from: settings.smtp.from,
      tls_mode: settings.smtp.tls_mode,
      global_recipients: settings.smtp.global_recipients,
    });
    setBaseUrl(settings.general.base_url);
    setNotifyMax(settings.general.notify_max_per_hour || 0);
    setBranding(settings.branding);
    setRetention(settings.retention);
    setReportsOn(!!settings.reports?.monthly_enabled);
    setReportRecipients(settings.reports?.recipients ?? "");
    setRegistrationOn(!!settings.registration);
  }, [settings]);

  useEffect(() => {
    api.org
      .get()
      .then((o) => {
        setOrg(o);
        setOrgName(o.name);
      })
      .catch(() => setOrg(null));
  }, []);

  const loadWindows = useCallback(async () => {
    try {
      setWindows(await api.maintenance.list());
    } catch {
      setWindows([]);
    }
  }, []);

  useEffect(() => {
    loadWindows();
  }, [loadWindows]);

  const loadChannels = useCallback(async () => {
    try {
      setChannelList(await api.channels.list());
    } catch {
      setChannelList([]);
    }
  }, []);

  useEffect(() => {
    loadChannels();
    api.tokens
      .list()
      .then(setTokens)
      .catch(() => setTokens([]));
  }, [loadChannels]);

  const saveSmtp = async (e: FormEvent) => {
    e.preventDefault();
    setSmtpSaving(true);
    try {
      await api.settings.put({
        smtp: { ...smtp, password: smtp.password || "" },
      });
      await reload();
      toast("success", "SMTP settings saved");
    } catch (err) {
      handleErr(err, "failed to save SMTP settings");
    } finally {
      setSmtpSaving(false);
    }
  };

  const sendTest = async () => {
    setTesting(true);
    try {
      await api.settings.smtpTest(testTo);
      toast("success", "test email sent");
      setTestOpen(false);
      setTestTo("");
    } catch (err) {
      handleErr(err, "test email failed");
    } finally {
      setTesting(false);
    }
  };

  const toggleDraftEvent = (ev: string) => {
    setChannelDraft((d) => {
      const events = d.events.includes(ev) ? d.events.filter((x) => x !== ev) : [...d.events, ev];
      return { ...d, events };
    });
  };

  const saveChannel = async (e: FormEvent) => {
    e.preventDefault();
    setChannelsSaving(true);
    try {
      const body = {
        name: channelDraft.name,
        type: channelDraft.type,
        dest: channelDraft.dest,
        events: channelDraft.events,
      };
      if (editingChannel != null) {
        await api.channels.update(editingChannel, body);
        toast("success", "channel saved");
      } else {
        await api.channels.create(body);
        toast("success", "channel created");
      }
      setChannelDraft(emptyChannelDraft);
      setEditingChannel(null);
      await loadChannels();
    } catch (err) {
      handleErr(err, "failed to save channel");
    } finally {
      setChannelsSaving(false);
    }
  };

  const editChannel = (ch: NotificationChannel) => {
    setEditingChannel(ch.id);
    setChannelDraft({
      name: ch.name,
      type: ch.type,
      dest: ch.dest,
      events: ch.events ?? [],
    });
  };

  const deleteChannel = async (id: number) => {
    try {
      await api.channels.remove(id);
      if (editingChannel === id) {
        setEditingChannel(null);
        setChannelDraft(emptyChannelDraft);
      }
      await loadChannels();
    } catch (err) {
      handleErr(err, "failed to delete channel");
    }
  };

  const testChannel = async (id: number) => {
    setChannelTesting(id);
    try {
      await api.channels.test(id);
      toast("success", "test notification sent");
    } catch (err) {
      handleErr(err, "test failed");
    } finally {
      setChannelTesting(null);
    }
  };

  const saveReports = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api.settings.put({
        reports: { monthly_enabled: reportsOn, recipients: reportRecipients },
      });
      await reload();
      toast("success", "saved");
    } catch (err) {
      handleErr(err, "failed to save");
    }
  };

  const saveGeneral = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api.settings.put({ general: { base_url: baseUrl, notify_max_per_hour: notifyMax } });
      await reload();
      toast("success", "saved");
    } catch (err) {
      handleErr(err, "failed to save");
    }
  };

  const saveBranding = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api.settings.put({ branding });
      await reload();
      toast("success", "branding saved");
    } catch (err) {
      handleErr(err, "failed to save branding");
    }
  };

  const saveRetention = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api.settings.put({ retention });
      await reload();
      toast("success", "retention saved");
    } catch (err) {
      handleErr(err, "failed to save retention");
    }
  };

  const changePassword = async (e: FormEvent) => {
    e.preventDefault();
    if (pw.next.length < 8) {
      toast("error", "new password must be at least 8 characters");
      return;
    }
    if (pw.next !== pw.confirm) {
      toast("error", "new passwords do not match");
      return;
    }
    setPwSaving(true);
    try {
      await api.changePassword(pw.current, pw.next);
      setPw({ current: "", next: "", confirm: "" });
      toast("success", "password updated");
    } catch (err) {
      handleErr(err, "failed to change password");
    } finally {
      setPwSaving(false);
    }
  };

  const createWindow = async (e: FormEvent) => {
    e.preventDefault();
    try {
      await api.maintenance.create({
        monitor_id: null,
        starts_at: new Date(newWindow.starts_at).toISOString(),
        ends_at: new Date(newWindow.ends_at).toISOString(),
        note: newWindow.note,
        public: newWindow.public,
      });
      await loadWindows();
      setNewWindow({ starts_at: "", ends_at: "", note: "", public: false });
      toast("success", "maintenance window created");
    } catch (err) {
      handleErr(err, "failed to create maintenance window");
    }
  };

  const deleteWindow = async (id: number) => {
    try {
      await api.maintenance.remove(id);
      await loadWindows();
    } catch (err) {
      handleErr(err, "failed to delete");
    }
  };

  function handleErr(err: unknown, fallback: string) {
    if (err instanceof ApiError) {
      // A lost session is handled once, for every page (see lib/api.ts).
      if (err.status === 401) return;
      if (err.status === 403) {
        toast("error", err.message);
        return;
      }
      toast("error", err.message || fallback);
    } else {
      toast("error", fallback);
    }
  }

  const saveOrg = async (e: FormEvent) => {
    e.preventDefault();
    setOrgSaving(true);
    try {
      const updated = await api.org.update({ name: orgName });
      setOrg(updated);
      toast("success", "organization updated");
    } catch (err) {
      handleErr(err, "failed to update organization");
    } finally {
      setOrgSaving(false);
    }
  };

  const saveRegistration = async (on: boolean) => {
    setRegistrationOn(on);
    try {
      await api.settings.put({ registration: on });
      await reload();
      toast("success", on ? "registration open" : "registration closed");
    } catch (err) {
      setRegistrationOn(!on);
      handleErr(err, "failed to update registration");
    }
  };

  const toLocalInput = (iso: string | undefined) =>
    iso ? new Date(iso).toISOString().slice(0, 16) : "";

  return (
    <div className="animate-fade-in-up">
      <h1 className="font-brand mb-5 text-xl tracking-tight">Settings</h1>
      <div className="md:grid md:grid-cols-[13rem_minmax(0,1fr)] md:gap-8">
      <nav aria-label="Settings sections" className="-mx-1 mb-5 flex gap-1 overflow-x-auto px-1 md:sticky md:top-6 md:mx-0 md:mb-0 md:flex-col md:self-start md:overflow-visible md:px-0">
        {(
          [
            ["alerts", "Alerts"],
            ["access", "Access"],
            ["probes", "Probes"],
            ["data", "Data"],
          ] as const
        ).map(([id, label]) => (
          <NavLink
            key={id}
            to={`/settings/${id}`}
            className={() => tabClass({ isActive: section === id })}
            aria-current={section === id ? "page" : undefined}
          >
            {label}
          </NavLink>
        ))}
      </nav>
      <div className="flex min-w-0 max-w-3xl flex-col">

      {section === "access" && org && (
        <Section title="Organization">
          <form onSubmit={saveOrg} className="flex flex-col gap-4">
            <Field label="Name">
              <Input
                value={orgName}
                onChange={(e) => setOrgName(e.target.value)}
                disabled={!admin}
                required
              />
            </Field>
            <div className="text-sm text-muted-foreground">
              Slug <span className="font-mono text-foreground">{org.slug}</span>
              <span className="mx-2">·</span>
              Plan{" "}
              <Badge tone={org.plan === "pro" ? "primary" : "muted"}>{org.plan}</Badge>
            </div>
            {settings?.hosted && admin && (
              <Field label="Public registration">
                <Select
                  value={registrationOn ? "open" : "closed"}
                  onChange={(e) => saveRegistration(e.target.value === "open")}
                >
                  <option value="open">Open — new orgs can sign up</option>
                  <option value="closed">Closed</option>
                </Select>
              </Field>
            )}
            {admin && (
              <Button type="submit" size="sm" className="self-end" disabled={orgSaving}>
                {orgSaving && <Loader2 size={14} className="animate-spin" />}
                Save organization
              </Button>
            )}
          </form>
        </Section>
      )}

      {section === "access" && (
      <Section title="Account">
            {user?.has_password !== false ? (
              <form onSubmit={changePassword} className="flex flex-col gap-4">
          <Field label="Current password">
            <Input
              type="password"
              value={pw.current}
              onChange={(e) => setPw({ ...pw, current: e.target.value })}
              autoComplete="current-password"
            />
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="New password" hint="At least 8 characters.">
              <Input
                type="password"
                value={pw.next}
                onChange={(e) => setPw({ ...pw, next: e.target.value })}
                autoComplete="new-password"
              />
            </Field>
            <Field label="Confirm">
              <Input
                type="password"
                value={pw.confirm}
                onChange={(e) => setPw({ ...pw, confirm: e.target.value })}
                autoComplete="new-password"
              />
            </Field>
          </div>
          <div className="flex justify-end">
            <Button type="submit" disabled={pwSaving || !pw.current || !pw.next}>
              {pwSaving && <Loader2 size={14} className="animate-spin" />}
              Change password
            </Button>
          </div>
            </form>
            ) : (
              <p className="text-sm text-muted-foreground">
                This account signs in with SSO. Password login is not set.
              </p>
            )}
      </Section>
      )}

      {section === "access" && admin && (
        <UsersSection toast={toast} handleErr={handleErr} />
      )}

      {section === "access" && admin && (
        <Section title="Single sign-on (OIDC)">
          <OIDCForm settings={settings} reload={reload} toast={toast} handleErr={handleErr} />
        </Section>
      )}

      {section === "probes" && admin && (
        <Section title="Probe nodes">
          <ProbeNodesForm toast={toast} handleErr={handleErr} />
        </Section>
      )}

      {section === "alerts" && (
      <>
      <Section title="Email alerts (SMTP)" readOnly={!admin} changedBy="An admin">
        <form onSubmit={saveSmtp} className="flex flex-col gap-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <Field label="SMTP host" className="sm:col-span-2">
              <Input
                value={smtp.host}
                onChange={(e) => setSmtp({ ...smtp, host: e.target.value })}
                placeholder="smtp.example.com"
              />
            </Field>
            <Field label="Port">
              <Input
                type="number"
                value={smtp.port}
                onChange={(e) => setSmtp({ ...smtp, port: Number(e.target.value) })}
              />
            </Field>
          </div>
          <Field label="Encryption">
            <Select
              value={smtp.tls_mode}
              onChange={(e) =>
                setSmtp({
                  ...smtp,
                  tls_mode: e.target.value as SMTPSettings["tls_mode"],
                })
              }
            >
              <option value="starttls">STARTTLS (usually port 587)</option>
              <option value="ssl">SSL/TLS (usually port 465)</option>
              <option value="none">None (not recommended)</option>
            </Select>
          </Field>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Username">
              <Input
                value={smtp.username}
                onChange={(e) => setSmtp({ ...smtp, username: e.target.value })}
                autoComplete="off"
              />
            </Field>
            <Field
              label="Password"
              hint={settings?.smtp_has_password ? "a password is already stored — leave blank to keep it" : undefined}
            >
              <Input
                type="password"
                value={smtp.password}
                onChange={(e) => setSmtp({ ...smtp, password: e.target.value })}
                placeholder={settings?.smtp_has_password ? "••••••••" : ""}
                autoComplete="new-password"
              />
            </Field>
          </div>
          <Field
            label="From address"
            hint="Who receives mail is an Email channel below. This is only the SMTP transport."
          >
            <Input
              value={smtp.from}
              onChange={(e) => setSmtp({ ...smtp, from: e.target.value })}
              placeholder="argus@example.com"
            />
          </Field>
          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={() => setTestOpen(true)}>
              <Send size={14} />
              Send test email
            </Button>
            <Button type="submit" disabled={smtpSaving}>
              {smtpSaving && <Loader2 size={14} className="animate-spin" />}
              Save SMTP settings
            </Button>
          </div>
        </form>
      </Section>

      <Section title="Notification channels" readOnly={viewOnly}>
        <p className="mb-4 text-sm text-muted-foreground">
          Named destinations: email, Slack, Discord, Teams, PagerDuty, Opsgenie, Telegram,
          ntfy, or a webhook. Each channel chooses which events it gets. Attach them per
          monitor, or leave the default set. SMTP above is only the mail server.
        </p>
        {channelList && channelList.length > 0 ? (
          <ul className="mb-6">
            {channelList.map((ch) => (
              <li
                key={ch.id}
                className="flex flex-wrap items-center gap-2 border-b border-border py-2.5 text-sm"
              >
                <span className="font-medium">{ch.name}</span>
                <Badge tone="primary">{ch.type}</Badge>
                <span className="text-xs text-muted-foreground">
                  {(ch.events ?? []).length === 0
                    ? "muted"
                    : (ch.events ?? []).join(" · ")}
                </span>
                <span className="ml-auto flex items-center gap-1">
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    disabled={channelTesting !== null}
                    onClick={() => testChannel(ch.id)}
                  >
                    {channelTesting === ch.id && <Loader2 size={14} className="animate-spin" />}
                    Test
                  </Button>
                  <Button type="button" variant="outline" size="sm" onClick={() => editChannel(ch)}>
                    Edit
                  </Button>
                  <ConfirmButton label={`Delete channel ${ch.name}`} onConfirm={() => deleteChannel(ch.id)}>
                    <Trash2 size={14} />
                  </ConfirmButton>
                </span>
              </li>
            ))}
          </ul>
        ) : (
          <p className="mb-6 text-sm text-muted-foreground">No named channels yet.</p>
        )}
        <form onSubmit={saveChannel} className="flex flex-col gap-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Name">
              <Input
                value={channelDraft.name}
                onChange={(e) => setChannelDraft({ ...channelDraft, name: e.target.value })}
                placeholder="On-call Slack"
                required
              />
            </Field>
            <Field label="Type">
              <Select
                value={channelDraft.type}
                onChange={(e) =>
                  setChannelDraft({ ...channelDraft, type: e.target.value as ChannelType })
                }
              >
                {CHANNEL_TYPES.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </Select>
            </Field>
          </div>
          <Field label={channelMeta.destLabel} hint={channelMeta.hint}>
            <Input
              value={channelDraft.dest}
              onChange={(e) => setChannelDraft({ ...channelDraft, dest: e.target.value })}
              placeholder={channelMeta.placeholder}
              required
            />
          </Field>
          <fieldset>
            <legend className="mb-2 text-xs font-medium text-muted-foreground">Events</legend>
            <div className="flex flex-wrap gap-x-4 gap-y-2">
              {CHANNEL_EVENTS.map((ev) => (
                <label key={ev.value} className="flex items-center gap-2 text-sm">
                  <input
                    type="checkbox"
                    checked={channelDraft.events.includes(ev.value)}
                    onChange={() => toggleDraftEvent(ev.value)}
                  />
                  {ev.label}
                </label>
              ))}
            </div>
          </fieldset>
          <div className="flex justify-end gap-2">
            {editingChannel != null && (
              <Button
                type="button"
                variant="outline"
                onClick={() => {
                  setEditingChannel(null);
                  setChannelDraft(emptyChannelDraft);
                }}
              >
                Cancel
              </Button>
            )}
            <Button type="submit" disabled={channelsSaving}>
              {channelsSaving && <Loader2 size={14} className="animate-spin" />}
              {editingChannel != null ? "Save channel" : "Add channel"}
            </Button>
          </div>
        </form>
      </Section>

      <Section title="Monthly uptime report" readOnly={!admin} changedBy="An admin">
        <form onSubmit={saveReports} className="flex flex-col gap-4">
          <p className="text-sm text-muted-foreground">
            On the first of each month Argus emails an uptime summary for the month that just
            ended: every monitor, its uptime against its SLO target, and which ones missed.
            Uses the SMTP settings above.
          </p>
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={reportsOn}
              onChange={(e) => setReportsOn(e.target.checked)}
            />
            Email a monthly uptime report
          </label>
          <Field label="Recipients" hint="Comma-separated addresses.">
            <Input
              value={reportRecipients}
              onChange={(e) => setReportRecipients(e.target.value)}
              placeholder="ops@example.com, cto@example.com"
            />
          </Field>
          {settings?.reports?.last_sent_month && (
            <p className="text-xs text-muted-foreground">
              Last report covered {settings.reports.last_sent_month}.
            </p>
          )}
          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              disabled={reportSending || !reportRecipients.trim()}
              onClick={async () => {
                setReportSending(true);
                try {
                  const out = await api.reports.sendNow();
                  toast("success", `sent the ${out.month} report to ${out.recipients} recipient(s)`);
                } catch (err) {
                  handleErr(err, "failed to send the report");
                } finally {
                  setReportSending(false);
                }
              }}
            >
              {reportSending && <Loader2 size={14} className="animate-spin" />}
              Send last month now
            </Button>
            <Button type="submit">Save report settings</Button>
          </div>
        </form>
      </Section>

      <Section title="Notification rate limit" readOnly={!admin} changedBy="An admin">
        <form onSubmit={saveGeneral} className="flex flex-col gap-4">
          <Field
            label="Max alerts per hour"
            hint="Across all channels. 0 = unlimited."
          >
            <Input
              type="number"
              min={0}
              max={10000}
              value={notifyMax}
              onChange={(e) => setNotifyMax(Number(e.target.value))}
            />
          </Field>
          <div className="flex justify-end">
            <Button type="submit">Save</Button>
          </div>
        </form>
      </Section>
      </>
      )}

      {section === "data" && (
      <Section title="Base URL" readOnly={!admin} changedBy="An admin">
        <form onSubmit={saveGeneral} className="flex flex-col gap-4">
          <Field
            label="Base URL"
            hint="Used in alert email links, e.g. https://argus.example.com"
          >
            <Input
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder="http://argus.example.com"
            />
          </Field>
          <div className="flex justify-end">
            <Button type="submit">Save</Button>
          </div>
        </form>
      </Section>
      )}

      {section === "access" && (
      <Section title="API tokens" readOnly={viewOnly}>
        <p className="mb-3 text-xs text-muted-foreground">
          Bearer tokens for automation. Shown once at create. Header: Authorization: Bearer … or
          X-Api-Token.
        </p>
        <form
          className="mb-4 flex gap-2"
          onSubmit={async (e) => {
            e.preventDefault();
            try {
              const t = await api.tokens.create(tokenName);
              setNewToken(t.token ?? null);
              setTokenName("");
              setTokens(await api.tokens.list());
              toast("success", "token created — copy it now");
            } catch (err) {
              handleErr(err, "failed to create token");
            }
          }}
        >
          <Input
            value={tokenName}
            onChange={(e) => setTokenName(e.target.value)}
            placeholder="CI deploy"
            required
          />
          <Button type="submit">Create</Button>
        </form>
        {newToken && (
          <p className="mb-3 break-all rounded border border-border bg-muted/40 px-3 py-2 font-mono text-xs">
            {newToken}
          </p>
        )}
        <div className="flex flex-col gap-2">
          {(tokens ?? []).map((t) => (
            <div key={t.id} className="flex items-center justify-between gap-3 text-sm">
              <span>
                {t.name}{" "}
                <span className="text-xs text-muted-foreground">{t.token_prefix}…</span>
              </span>
              <ConfirmButton
                label={`Revoke token ${t.name}`}
                ask="Revoke?"
                className="border border-border text-foreground"
                onConfirm={async () => {
                  try {
                    await api.tokens.remove(t.id);
                    setTokens(await api.tokens.list());
                  } catch (err) {
                    handleErr(err, "failed to delete token");
                  }
                }}
              >
                Revoke
              </ConfirmButton>
            </div>
          ))}
        </div>
      </Section>
      )}

      {section === "data" && (
      <Section title="Import / export" readOnly={viewOnly}>
        <p className="mb-3 text-xs text-muted-foreground">
          Export Argus monitors and channels as JSON. Import an Argus export or an Uptime Kuma
          JSON backup. Docker, SSH, and other types Kuma has that Argus does not are skipped.
        </p>
        {isAdmin(user) ? (
          <div className="mb-4 flex flex-col gap-3">
            <p className="text-xs text-muted-foreground">
              An export is a full backup, so it contains every stored password, secret, and
              webhook in clear. Give it a passphrase to encrypt it; you will need the same
              passphrase to import it.
            </p>
            <div className="flex flex-wrap items-end gap-2">
              <Field label="Export passphrase" hint="At least 12 characters.">
                <Input
                  type="password"
                  autoComplete="new-password"
                  value={exportPassphrase}
                  onChange={(e) => setExportPassphrase(e.target.value)}
                  className="w-64"
                />
              </Field>
              <Button
                type="button"
                disabled={exportPassphrase.length < 12}
                onClick={async () => {
                  try {
                    downloadJSON(await api.exportEncrypted(exportPassphrase), "argus-export.encrypted.json");
                    setExportPassphrase("");
                  } catch (err) {
                    handleErr(err, "export failed");
                  }
                }}
              >
                Download encrypted export
              </Button>
              <Button
                type="button"
                variant="outline"
                onClick={async () => {
                  try {
                    downloadJSON(await api.export(), "argus-export.json");
                  } catch (err) {
                    handleErr(err, "export failed");
                  }
                }}
              >
                Download unencrypted
              </Button>
            </div>
          </div>
        ) : (
          <p className="mb-4 text-xs text-muted-foreground">
            Exports are limited to admins because they contain credentials.
          </p>
        )}
        <div className="flex flex-wrap items-end gap-2">
          <Field label="Import passphrase" hint="Only for encrypted exports.">
            <Input
              type="password"
              autoComplete="off"
              value={importPassphrase}
              onChange={(e) => setImportPassphrase(e.target.value)}
              className="w-64"
            />
          </Field>
          <label className="inline-flex h-9 cursor-pointer items-center">
            <span className="sr-only">Import JSON</span>
            <input
              type="file"
              accept="application/json"
              className="text-sm"
              onChange={async (e) => {
                const file = e.target.files?.[0];
                e.target.value = "";
                if (!file) return;
                try {
                  const text = await file.text();
                  const data = JSON.parse(text) as { monitorList?: unknown; format?: string };
                  const format = Array.isArray(data.monitorList) ? "kuma" : "argus";
                  if (data.format === "argus-encrypted" && !importPassphrase) {
                    toast("error", "this export is encrypted; enter its passphrase first");
                    return;
                  }
                  const res = await api.import({
                    format,
                    data,
                    passphrase: data.format === "argus-encrypted" ? importPassphrase : undefined,
                  });
                  setImportPassphrase("");
                  setImportResult(
                    `Imported ${res.imported}. Skipped ${res.skipped.length}.`,
                  );
                  toast("success", `imported ${res.imported} monitors`);
                } catch (err) {
                  handleErr(err, "import failed");
                }
              }}
            />
          </label>
        </div>
        {importResult && <p className="mt-2 text-xs text-muted-foreground">{importResult}</p>}
      </Section>
      )}

      {section === "access" && (
      <Section title="Custom branding" readOnly={!admin} changedBy="An admin">
        <form onSubmit={saveBranding} className="flex flex-col gap-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Field label="Product name">
              <Input
                value={branding.product_name}
                onChange={(e) => setBranding({ ...branding, product_name: e.target.value })}
                placeholder="Argus"
              />
            </Field>
            <Field
              label="Accent color"
              hint={
                branding.accent_color
                  ? "Used in both themes; check it reads well on light and dark."
                  : "Using the Argus accent, tuned for each theme."
              }
            >
              <div className="flex items-center gap-2">
                <Input
                  type="color"
                  aria-label="Accent color"
                  value={branding.accent_color || "#1f5bd8"}
                  onChange={(e) => setBranding({ ...branding, accent_color: e.target.value })}
                  className="h-9 cursor-pointer p-1"
                />
                {branding.accent_color && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => setBranding({ ...branding, accent_color: "" })}
                  >
                    Use default
                  </Button>
                )}
              </div>
            </Field>
          </div>
          <Field label="Logo URL">
            <Input
              value={branding.logo_url}
              onChange={(e) => setBranding({ ...branding, logo_url: e.target.value })}
              placeholder="https://example.com/logo.png"
            />
          </Field>
          <Field label="Login page message">
            <Input
              value={branding.login_message}
              onChange={(e) => setBranding({ ...branding, login_message: e.target.value })}
              placeholder="Acme Corp monitoring"
            />
          </Field>
          <div className="flex justify-end">
            <Button type="submit">Save branding</Button>
          </div>
        </form>
      </Section>
      )}

      {section === "data" && (
      <Section title="Data retention" readOnly={!admin} changedBy="An admin">
        <form onSubmit={saveRetention} className="flex flex-col gap-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <Field label="Raw results (days)" hint="1–30">
              <Input
                type="number"
                min={1}
                max={30}
                value={retention.raw_days}
                onChange={(e) => setRetention({ ...retention, raw_days: Number(e.target.value) })}
              />
            </Field>
            <Field label="Hourly rollups (days)" hint="7–365">
              <Input
                type="number"
                min={7}
                max={365}
                value={retention.hourly_days}
                onChange={(e) => setRetention({ ...retention, hourly_days: Number(e.target.value) })}
              />
            </Field>
            <Field label="Daily rollups (days)" hint="30–1825">
              <Input
                type="number"
                min={30}
                max={1825}
                value={retention.daily_days}
                onChange={(e) => setRetention({ ...retention, daily_days: Number(e.target.value) })}
              />
            </Field>
          </div>
          <div className="flex justify-end">
            <Button type="submit">Save retention</Button>
          </div>
        </form>
      </Section>
      )}

      {section === "alerts" && (
      <Section title="Maintenance windows" readOnly={viewOnly}>
        <form onSubmit={createWindow} className="mb-5 grid grid-cols-1 gap-4 sm:grid-cols-3">
          <Field label="Starts at">
            <Input
              type="datetime-local"
              value={newWindow.starts_at}
              onChange={(e) => setNewWindow({ ...newWindow, starts_at: e.target.value })}
              required
            />
          </Field>
          <Field label="Ends at">
            <Input
              type="datetime-local"
              value={newWindow.ends_at}
              onChange={(e) => setNewWindow({ ...newWindow, ends_at: e.target.value })}
              required
            />
          </Field>
          <Field label="Note">
            <Input
              value={newWindow.note}
              onChange={(e) => setNewWindow({ ...newWindow, note: e.target.value })}
              placeholder="router firmware upgrade"
            />
          </Field>
          <label className="flex items-center gap-2 text-sm sm:col-span-3">
            <input
              type="checkbox"
              checked={newWindow.public}
              onChange={(e) => setNewWindow({ ...newWindow, public: e.target.checked })}
            />
            Announce on status pages
          </label>
          <div className="sm:col-span-3 flex justify-end">
            <Button type="submit" size="sm">
              Add window
            </Button>
          </div>
        </form>

        {windows && windows.length > 0 ? (
          <ul className="flex flex-col gap-2">
            {windows.map((w) => (
              <li
                key={w.id}
                className="flex items-center gap-3 border-b border-border py-2.5 text-sm"
              >
                <Mail size={13} className="shrink-0 text-muted-foreground" />
                <span className="truncate">{w.note || "(no note)"}</span>
                {w.public && <Badge tone="primary">announced</Badge>}
                <span className="ml-auto shrink-0 text-xs tabular-nums text-muted-foreground">
                  {formatDateTime(w.starts_at)} → {formatDateTime(w.ends_at)}
                </span>
                <ConfirmButton label="Delete maintenance window" onConfirm={() => deleteWindow(w.id)}>
                  <Trash2 size={14} />
                </ConfirmButton>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-sm text-muted-foreground">No maintenance windows.</p>
        )}
      </Section>
      )}

      <Dialog open={testOpen} onClose={() => setTestOpen(false)} title="Send test email">
        <p className="mb-4 text-sm text-muted-foreground">
          Sends a test email through your configured SMTP server.
        </p>
        <Field label="Recipient">
          <Input
            type="email"
            value={testTo}
            onChange={(e) => setTestTo(e.target.value)}
            placeholder="you@example.com"
          />
        </Field>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="outline" onClick={() => setTestOpen(false)}>
            Cancel
          </Button>
          <Button onClick={sendTest} disabled={testing || !testTo.trim()}>
            {testing && <Loader2 size={14} className="animate-spin" />}
            Send
          </Button>
        </div>
      </Dialog>
      </div>
      </div>
    </div>
  );
}

function UsersSection({
  toast,
  handleErr,
}: {
  toast: (kind: "success" | "error" | "info", message: string) => void;
  handleErr: (err: unknown, fallback: string) => void;
}) {
  const [users, setUsers] = useState<AppUser[]>([]);
  const [draft, setDraft] = useState({ email: "", name: "", role: "operator", password: "" });
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    try {
      setUsers(await api.users.list());
    } catch {
      setUsers([]);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const create = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      await api.users.create(draft);
      toast("success", "user created");
      setDraft({ email: "", name: "", role: "operator", password: "" });
      await load();
    } catch (err) {
      handleErr(err, "failed to create user");
    } finally {
      setSaving(false);
    }
  };

  return (
    <Section title="Users">
      <ul className="mb-4 flex flex-col gap-2 text-sm">
        {users.map((u) => (
          <li key={u.id} className="flex flex-wrap items-center justify-between gap-2 border-b border-border py-2">
            <div>
              <div className="font-medium">{u.email}</div>
              <div className="text-xs text-muted-foreground">
                {u.role}
                {u.disabled ? " · disabled" : ""}
                {u.oidc ? " · SSO" : ""}
              </div>
            </div>
            <div className="flex gap-2">
              <Button
                size="sm"
                variant="outline"
                onClick={async () => {
                  try {
                    await api.users.update(u.id, {
                      name: u.name,
                      role: u.role,
                      disabled: !u.disabled,
                    });
                    await load();
                  } catch (err) {
                    handleErr(err, "failed to update user");
                  }
                }}
              >
                {u.disabled ? "Enable" : "Disable"}
              </Button>
              <ConfirmButton
                label={`Delete user ${u.email}`}
                onConfirm={async () => {
                  try {
                    await api.users.remove(u.id);
                    await load();
                  } catch (err) {
                    handleErr(err, "failed to delete user");
                  }
                }}
              >
                <Trash2 size={14} />
              </ConfirmButton>
            </div>
          </li>
        ))}
      </ul>
      <form onSubmit={create} className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <Field label="Email">
          <Input
            type="email"
            value={draft.email}
            onChange={(e) => setDraft({ ...draft, email: e.target.value })}
            required
          />
        </Field>
        <Field label="Name">
          <Input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} />
        </Field>
        <Field label="Role">
          <Select value={draft.role} onChange={(e) => setDraft({ ...draft, role: e.target.value })}>
            <option value="admin">Admin</option>
            <option value="operator">Operator</option>
            <option value="viewer">Viewer</option>
          </Select>
        </Field>
        <Field label="Password">
          <Input
            type="password"
            value={draft.password}
            onChange={(e) => setDraft({ ...draft, password: e.target.value })}
            required
            minLength={8}
          />
        </Field>
        <div className="sm:col-span-2 flex justify-end">
          <Button type="submit" size="sm" disabled={saving}>
            {saving && <Loader2 size={14} className="animate-spin" />}
            Add user
          </Button>
        </div>
      </form>
    </Section>
  );
}

function OIDCForm({
  settings,
  reload,
  toast,
  handleErr,
}: {
  settings: ReturnType<typeof useSettings>["settings"];
  reload: () => Promise<void>;
  toast: (kind: "success" | "error" | "info", message: string) => void;
  handleErr: (err: unknown, fallback: string) => void;
}) {
  const [oidc, setOidc] = useState<OIDCSettings>({
    enabled: false,
    issuer: "",
    client_id: "",
    client_secret: "",
    default_role: "viewer",
  });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!settings?.oidc) return;
    setOidc({
      enabled: settings.oidc.enabled,
      issuer: settings.oidc.issuer,
      client_id: settings.oidc.client_id,
      client_secret: "",
      default_role: settings.oidc.default_role || "viewer",
    });
  }, [settings]);

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      await api.settings.put({ oidc });
      await reload();
      toast("success", "OIDC settings saved");
    } catch (err) {
      handleErr(err, "failed to save OIDC");
    } finally {
      setSaving(false);
    }
  };

  return (
    <form onSubmit={save} className="flex flex-col gap-4">
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={oidc.enabled}
          onChange={(e) => setOidc({ ...oidc, enabled: e.target.checked })}
        />
        Enable SSO on the login page
      </label>
      <Field label="Issuer URL" hint="The OIDC discovery origin, e.g. https://login.example.com">
        <Input value={oidc.issuer} onChange={(e) => setOidc({ ...oidc, issuer: e.target.value })} />
      </Field>
      <Field label="Client ID">
        <Input value={oidc.client_id} onChange={(e) => setOidc({ ...oidc, client_id: e.target.value })} />
      </Field>
      <Field label="Client secret" hint={settings?.oidc_has_secret ? "Leave blank to keep the current secret." : ""}>
        <Input
          type="password"
          value={oidc.client_secret}
          onChange={(e) => setOidc({ ...oidc, client_secret: e.target.value })}
        />
      </Field>
      <Field label="Default role for new SSO users">
        <Select
          value={oidc.default_role}
          onChange={(e) => setOidc({ ...oidc, default_role: e.target.value })}
        >
          <option value="viewer">Viewer</option>
          <option value="operator">Operator</option>
          <option value="admin">Admin</option>
        </Select>
      </Field>
      <p className="text-xs text-muted-foreground">
        Redirect URI: <code>{(settings?.general.base_url || window.location.origin).replace(/\/$/, "")}/api/auth/oidc/callback</code>
      </p>
      <div className="flex justify-end">
        <Button type="submit" size="sm" disabled={saving}>
          {saving && <Loader2 size={14} className="animate-spin" />}
          Save SSO
        </Button>
      </div>
    </form>
  );
}

function ProbeNodesForm({
  toast,
  handleErr,
}: {
  toast: (kind: "success" | "error" | "info", message: string) => void;
  handleErr: (err: unknown, fallback: string) => void;
}) {
  const [nodes, setNodes] = useState<ProbeNode[]>([]);
  const [draft, setDraft] = useState({ name: "", region: "" });
  const [createdToken, setCreatedToken] = useState<string | null>(null);

  const load = useCallback(async () => {
    try {
      setNodes(await api.probeNodes.list());
    } catch {
      setNodes([]);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        Run another probe in a second region with <code>ARGUS_API_URL</code> and{" "}
        <code>ARGUS_PROBE_TOKEN</code>. Leave monitors unassigned to keep checking from the local probe.
      </p>
      {createdToken && (
        <p className="text-xs font-mono break-all border border-border px-3 py-2">
          Token (shown once): {createdToken}
        </p>
      )}
      <ul className="flex flex-col gap-2 text-sm">
        {nodes.map((n) => (
          <li key={n.id} className="flex items-center justify-between gap-2 border-b border-border py-2">
            <div>
              <div className="font-medium">{n.name}</div>
              <div className="text-xs text-muted-foreground">
                {n.region || "no region"} · last seen {n.last_seen ? formatDateTime(n.last_seen) : "never"}
              </div>
            </div>
            <ConfirmButton
              label={`Delete probe node ${n.name}`}
              onConfirm={async () => {
                try {
                  await api.probeNodes.remove(n.id);
                  await load();
                } catch (err) {
                  handleErr(err, "failed to delete probe node");
                }
              }}
            >
              <Trash2 size={14} />
            </ConfirmButton>
          </li>
        ))}
      </ul>
      <form
        className="grid grid-cols-1 gap-3 sm:grid-cols-2"
        onSubmit={async (e) => {
          e.preventDefault();
          try {
            const n = await api.probeNodes.create(draft);
            setCreatedToken(n.token ?? null);
            setDraft({ name: "", region: "" });
            toast("success", "probe node created");
            await load();
          } catch (err) {
            handleErr(err, "failed to create probe node");
          }
        }}
      >
        <Field label="Name">
          <Input value={draft.name} onChange={(e) => setDraft({ ...draft, name: e.target.value })} required />
        </Field>
        <Field label="Region">
          <Input
            value={draft.region}
            onChange={(e) => setDraft({ ...draft, region: e.target.value })}
            placeholder="us-east"
          />
        </Field>
        <div className="sm:col-span-2 flex justify-end">
          <Button type="submit" size="sm">
            Add probe node
          </Button>
        </div>
      </form>
    </div>
  );
}
