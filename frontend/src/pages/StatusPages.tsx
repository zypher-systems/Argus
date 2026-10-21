import { useCallback, useEffect, useState, type FormEvent } from "react";
import { ExternalLink, Loader2, Trash2 } from "lucide-react";
import { Button } from "../components/ui/button";
import { ConfirmButton } from "../components/ui/confirm-button";
import { Field, Input } from "../components/ui/controls";
import { Badge } from "../components/ui/badge";
import { api, ApiError } from "../lib/api";
import { useSettings } from "../lib/settings";
import { canWrite, useAuth } from "../lib/auth";
import { useToast } from "../lib/toast";
import { useMonitorStore } from "../lib/store";
import type { StatusPage, StatusSubscriber } from "../lib/types";

function slugify(name: string): string {
  return name
    .toLowerCase()
    .trim()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 40);
}

export function StatusPages() {
  // Viewers can see and open pages; creating and changing them is for writers.
  const writable = canWrite(useAuth().user);
  const { toast } = useToast();
  const { settings } = useSettings();
  const { monitors } = useMonitorStore();
  const origin = (settings?.general.base_url || window.location.origin).replace(/\/$/, "");

  const [pages, setPages] = useState<StatusPage[] | null>(null);
  const [editing, setEditing] = useState<number | null>(null);
  const [saving, setSaving] = useState(false);
  const [name, setName] = useState("");
  const [slug, setSlug] = useState("");
  const [slugTouched, setSlugTouched] = useState(false);
  const [headline, setHeadline] = useState("");
  const [branded, setBranded] = useState(false);
  const [published, setPublished] = useState(true);
  const [monitorIDs, setMonitorIDs] = useState<number[] | null>(null);
  const [customDomain, setCustomDomain] = useState("");
  const [hookDest, setHookDest] = useState("");
  const [subs, setSubs] = useState<StatusSubscriber[]>([]);

  const reset = () => {
    setEditing(null);
    setName("");
    setSlug("");
    setSlugTouched(false);
    setHeadline("");
    setBranded(false);
    setPublished(true);
    setMonitorIDs(null);
    setCustomDomain("");
    setHookDest("");
    setSubs([]);
  };

  const load = useCallback(async () => {
    try {
      setPages(await api.status.list());
    } catch {
      setPages([]);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const handleErr = (err: unknown, fallback: string) => {
    toast("error", err instanceof ApiError ? err.message : fallback);
  };

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    setSaving(true);
    try {
      const body = {
        name,
        slug,
        headline,
        branded,
        published,
        monitor_ids: monitorIDs,
        custom_domain: customDomain,
      };
      if (editing != null) {
        await api.status.update(editing, body);
        toast("success", "status page saved");
      } else {
        await api.status.create(body);
        toast("success", "status page created");
      }
      reset();
      await load();
    } catch (err) {
      handleErr(err, "failed to save status page");
    } finally {
      setSaving(false);
    }
  };

  const edit = (p: StatusPage) => {
    setEditing(p.id);
    setName(p.name);
    setSlug(p.slug);
    setSlugTouched(true);
    setHeadline(p.headline);
    setBranded(p.branded);
    setPublished(p.published);
    setMonitorIDs(p.monitor_ids);
    setCustomDomain(p.custom_domain || "");
    api.status.subscribers(p.id).then(setSubs).catch(() => setSubs([]));
  };

  const remove = async (id: number) => {
    try {
      await api.status.remove(id);
      if (editing === id) reset();
      await load();
    } catch (err) {
      handleErr(err, "failed to delete status page");
    }
  };

  const list = monitors ?? [];

  return (
    <div className="animate-fade-in-up mx-auto max-w-2xl">
      <h1 className="font-brand mb-2 text-lg tracking-tight">Status pages</h1>
      <p className="mb-8 text-sm text-muted-foreground">
        Create a page below, then open it at {origin}/status/&lt;slug&gt; — no login. The slug is
        the field you type (auto-filled from the name). Names only; targets stay private. Share that
        URL as the public demo. A custom hostname can be bound too; TLS stays on your reverse proxy.
      </p>

      {pages && pages.length > 0 ? (
        <ul className="mb-10">
          {pages.map((p) => (
            <li
              key={p.id}
              className="flex flex-wrap items-center gap-2 border-b border-border py-2.5 text-sm"
            >
              <span className="font-medium">{p.name}</span>
              <span className="text-xs text-muted-foreground">/{p.slug}</span>
              {!p.published && <Badge tone="muted">draft</Badge>}
              {p.branded && <Badge tone="primary">branded</Badge>}
              {p.custom_domain && <span className="text-xs text-muted-foreground">{p.custom_domain}</span>}
              {p.published && (
                <span className="flex items-center gap-2 text-xs text-muted-foreground">
                  <CopyLink label="badge" url={`${origin}/api/status/${p.slug}/badge.svg`} />
                  <CopyLink label="uptime badge" url={`${origin}/api/status/${p.slug}/uptime.svg`} />
                  <CopyLink label="RSS" url={`${origin}/api/status/${p.slug}/feed.xml`} />
                </span>
              )}
              <span className="ml-auto flex items-center gap-1">
                {p.published && (
                  <a
                    href={`${origin}/status/${p.slug}`}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex h-8 items-center gap-1 rounded-lg px-3 text-xs font-medium text-muted-foreground hover:bg-muted hover:text-foreground"
                  >
                    <ExternalLink size={12} />
                    Open
                  </a>
                )}
                {writable && (
                  <>
                    <Button type="button" variant="outline" size="sm" onClick={() => edit(p)}>
                      Edit
                    </Button>
                    <ConfirmButton label={`Delete status page ${p.name} and its subscribers`} onConfirm={() => remove(p.id)}>
                      <Trash2 size={14} />
                    </ConfirmButton>
                  </>
                )}
              </span>
            </li>
          ))}
        </ul>
      ) : (
        <p className="mb-10 text-sm text-muted-foreground">No status pages yet.</p>
      )}

      {writable && (
      <form onSubmit={submit} className="surface flex flex-col gap-4 p-5">
        <h2 className="text-sm font-semibold">{editing != null ? "Edit page" : "New page"}</h2>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Field label="Name">
            <Input
              value={name}
              onChange={(e) => {
                setName(e.target.value);
                if (!slugTouched) setSlug(slugify(e.target.value));
              }}
              placeholder="API status"
              required
            />
          </Field>
          <Field label="Slug" hint={`Public URL: /status/${slug || "…"}`}>
            <Input
              value={slug}
              onChange={(e) => {
                setSlugTouched(true);
                setSlug(e.target.value.toLowerCase());
              }}
              placeholder="api"
              required
            />
          </Field>
        </div>
        <Field label="Headline (optional)">
          <Input
            value={headline}
            onChange={(e) => setHeadline(e.target.value)}
            placeholder="We're tracking this in real time."
          />
        </Field>
        <label className="flex items-center gap-2 text-sm">
          <input type="checkbox" checked={published} onChange={(e) => setPublished(e.target.checked)} />
          Published
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={branded}
            onChange={(e) => setBranded(e.target.checked)}
          />
          Use custom branding (name, logo, accent)
        </label>
        <Field
          label="Custom domain"
          hint="CNAME this hostname at this Argus install. TLS terminates at nginx, Caddy, or Traefik."
        >
          <Input
            value={customDomain}
            onChange={(e) => setCustomDomain(e.target.value.toLowerCase())}
            placeholder="status.example.com"
          />
        </Field>

        <fieldset>
          <legend className="mb-2 text-sm font-medium">Monitors</legend>
          <p className="mb-3 text-xs text-muted-foreground">
            Leave all checked to include every enabled monitor. Uncheck to publish a subset.
          </p>
          {list.length === 0 ? (
            <p className="text-sm text-muted-foreground">Add monitors first.</p>
          ) : (
            <div className="flex flex-col gap-2">
              {list.map((m) => {
                const checked = monitorIDs === null || monitorIDs.includes(m.id);
                return (
                  <label key={m.id} className="flex items-center gap-2 text-sm">
                    <input
                      type="checkbox"
                      checked={checked}
                      onChange={() => {
                        const allIds = list.map((x) => x.id);
                        const current = monitorIDs ?? allIds;
                        const next = current.includes(m.id)
                          ? current.filter((id) => id !== m.id)
                          : [...current, m.id];
                        const isDefault =
                          next.length === allIds.length && allIds.every((id) => next.includes(id));
                        setMonitorIDs(isDefault ? null : next);
                      }}
                    />
                    <span className="font-medium">{m.name}</span>
                    <span className="text-xs text-muted-foreground">{m.type}</span>
                  </label>
                );
              })}
            </div>
          )}
        </fieldset>

        {editing != null && (
          <fieldset>
            <legend className="mb-2 text-sm font-medium">Webhook subscribers</legend>
            <p className="mb-3 text-xs text-muted-foreground">
              Email subscribers confirm from the public page. Add a webhook here for automation.
            </p>
            {subs.length === 0 ? (
              <p className="mb-3 text-sm text-muted-foreground">None yet.</p>
            ) : (
              <ul className="mb-3">
                {subs.map((sub) => (
                  <li key={sub.id} className="flex items-center gap-2 py-1 text-sm">
                    <span className="font-medium">{sub.kind}</span>
                    <span className="truncate text-muted-foreground">{sub.dest}</span>
                    {!sub.confirmed && <Badge tone="warning">unconfirmed</Badge>}
                    <button
                      type="button"
                      className="ml-auto text-xs text-muted-foreground hover:text-danger"
                      onClick={async () => {
                        try {
                          await api.status.removeSubscriber(editing, sub.id);
                          setSubs(await api.status.subscribers(editing));
                        } catch (err) {
                          handleErr(err, "failed to remove subscriber");
                        }
                      }}
                    >
                      Remove
                    </button>
                  </li>
                ))}
              </ul>
            )}
            <div className="flex flex-col gap-2 sm:flex-row">
              <Input
                value={hookDest}
                onChange={(e) => setHookDest(e.target.value)}
                placeholder="https://example.com/status-hook"
              />
              <Button
                type="button"
                variant="outline"
                onClick={async () => {
                  try {
                    await api.status.addSubscriber(editing, { kind: "webhook", dest: hookDest });
                    setHookDest("");
                    setSubs(await api.status.subscribers(editing));
                    toast("success", "webhook subscriber added");
                  } catch (err) {
                    handleErr(err, "failed to add webhook");
                  }
                }}
              >
                Add webhook
              </Button>
            </div>
          </fieldset>
        )}

        <div className="flex justify-end gap-2">
          {editing != null && (
            <Button type="button" variant="outline" onClick={reset}>
              Cancel
            </Button>
          )}
          <Button type="submit" disabled={saving}>
            {saving && <Loader2 size={14} className="animate-spin" />}
            {editing != null ? "Save page" : "Create page"}
          </Button>
        </div>
      </form>
      )}
    </div>
  );
}

/** Small copy-to-clipboard chip for an embeddable URL. */
function CopyLink({ label, url }: { label: string; url: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      title={url}
      className="rounded border border-border px-1.5 py-0.5 transition hover:bg-muted"
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(url);
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        } catch {
          window.prompt("Copy this URL", url);
        }
      }}
    >
      {copied ? "copied" : label}
    </button>
  );
}
