import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api } from "../lib/api";
import { SUPPORT_EMAIL } from "../lib/support";
import { useToast } from "../lib/toast";
import { Button } from "./ui/button";
import { Field, Input, Select, Textarea } from "./ui/controls";
import { Dialog } from "./ui/dialog";

const KINDS = [
  { value: "bug", label: "Something is broken", tag: "bug" },
  { value: "feature", label: "An idea or a request", tag: "feature" },
  { value: "other", label: "Something else", tag: "feedback" },
] as const;

type Kind = (typeof KINDS)[number]["value"];

/**
 * Feedback goes by email, from the person's own mail app. Argus itself sends
 * nothing.
 * The message is drafted with what support asks for first (what kind of
 * report it is, which Argus, which install), and the person can read and
 * change all of it before sending.
 */
export function FeedbackDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { toast } = useToast();
  const [kind, setKind] = useState<Kind>("bug");
  const [subject, setSubject] = useState("");
  const [message, setMessage] = useState("");
  const [version, setVersion] = useState("");
  const [instanceId, setInstanceId] = useState("");

  useEffect(() => {
    if (!open) return;
    api
      .instance()
      .then((i) => {
        setVersion(i.version);
        setInstanceId(i.instance_id);
      })
      .catch(() => {
        /* the report is still worth sending without them */
      });
  }, [open]);

  const draft = useMemo(() => {
    const tag = KINDS.find((k) => k.value === kind)?.tag ?? "feedback";
    const details = [
      `Kind: ${tag}`,
      `Argus: ${version || "unknown"}`,
      instanceId && `Instance: ${instanceId}`,
      `Page: ${window.location.pathname}`,
      `Browser: ${navigator.userAgent}`,
    ].filter(Boolean);
    return {
      subject: `[Argus ${tag}] ${subject.trim()}`,
      body: `${message.trim()}\n\n--\n${details.join("\n")}\n`,
    };
  }, [kind, subject, message, version, instanceId]);

  const ready = subject.trim() !== "" && message.trim() !== "";

  const send = (e: FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    window.location.href = `mailto:${SUPPORT_EMAIL}?subject=${encodeURIComponent(draft.subject)}&body=${encodeURIComponent(draft.body)}`;
    onClose();
  };

  // For a browser with no mail app set up, where the link above does nothing.
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(`To: ${SUPPORT_EMAIL}\nSubject: ${draft.subject}\n\n${draft.body}`);
      toast("success", `Copied. Paste it into an email to ${SUPPORT_EMAIL}.`);
    } catch {
      toast("error", `Could not copy. Write to ${SUPPORT_EMAIL} directly.`);
    }
  };

  return (
    <Dialog open={open} onClose={onClose} title="Send feedback" className="max-w-lg">
      <form onSubmit={send} className="flex flex-col gap-4">
        <Field label="What is it?">
          <Select value={kind} onChange={(e) => setKind(e.target.value as Kind)}>
            {KINDS.map((k) => (
              <option key={k.value} value={k.value}>
                {k.label}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="In a few words">
          <Input value={subject} onChange={(e) => setSubject(e.target.value)} maxLength={120} required />
        </Field>
        <Field
          label={kind === "bug" ? "What happened, and what did you expect?" : "Tell us more"}
          hint="Leave out passwords and anything else you would not put in an email."
        >
          <Textarea value={message} onChange={(e) => setMessage(e.target.value)} rows={6} required />
        </Field>
        <p className="text-xs text-muted-foreground">
          This opens a draft in your email app, addressed to {SUPPORT_EMAIL}. Argus sends nothing itself. The draft
          adds your Argus version, this install's instance ID, the page you are on and your browser; you can edit or
          remove any of it before sending.
        </p>
        <div className="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="ghost" size="sm" onClick={copy} disabled={!ready}>
            Copy instead
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" size="sm" disabled={!ready}>
            Open email draft
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
