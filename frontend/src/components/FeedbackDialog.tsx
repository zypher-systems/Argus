import { useEffect, useMemo, useState, type FormEvent } from "react";
import { api } from "../lib/api";
import { GITHUB_ISSUES_NEW, SUPPORT_EMAIL } from "../lib/support";
import { useToast } from "../lib/toast";
import { Button } from "./ui/button";
import { Field, Input, Select, Textarea } from "./ui/controls";
import { Dialog } from "./ui/dialog";

const KINDS = [
  { value: "bug", label: "Something is broken" },
  { value: "feature", label: "An idea or a request" },
  { value: "other", label: "Something else" },
] as const;

type Kind = (typeof KINDS)[number]["value"];

function isIssue(kind: Kind) {
  return kind === "bug" || kind === "feature";
}

/**
 * A bug or a feature request opens a GitHub issue. Anything else is an email
 * draft to support. Argus itself sends nothing. The instance ID stays in the
 * email only, because a GitHub issue is public.
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

  const issue = isIssue(kind);

  const draft = useMemo(() => {
    const details = issue
      ? [`Argus: ${version || "unknown"}`, `Page: ${window.location.pathname}`, `Browser: ${navigator.userAgent}`]
      : [
          "Kind: feedback",
          `Argus: ${version || "unknown"}`,
          instanceId && `Instance: ${instanceId}`,
          `Page: ${window.location.pathname}`,
          `Browser: ${navigator.userAgent}`,
        ].filter(Boolean);
    const body = `${message.trim()}\n\n--\n${details.join("\n")}\n`;
    return {
      subject: issue ? subject.trim() : `[Argus feedback] ${subject.trim()}`,
      body,
    };
  }, [issue, subject, message, version, instanceId]);

  const ready = subject.trim() !== "" && message.trim() !== "";

  const send = (e: FormEvent) => {
    e.preventDefault();
    if (!ready) return;
    if (issue) {
      const url = new URL(GITHUB_ISSUES_NEW);
      url.searchParams.set("title", draft.subject);
      url.searchParams.set("body", draft.body);
      url.searchParams.set("labels", kind === "bug" ? "bug" : "enhancement");
      const opened = window.open(url.toString(), "_blank", "noopener,noreferrer");
      if (!opened) {
        toast("error", "The browser blocked the new tab. Use Copy instead.");
        return;
      }
      onClose();
      return;
    }
    window.location.href = `mailto:${SUPPORT_EMAIL}?subject=${encodeURIComponent(draft.subject)}&body=${encodeURIComponent(draft.body)}`;
    onClose();
  };

  const copy = async () => {
    const text = issue
      ? `Title: ${draft.subject}\n\n${draft.body}`
      : `To: ${SUPPORT_EMAIL}\nSubject: ${draft.subject}\n\n${draft.body}`;
    try {
      await navigator.clipboard.writeText(text);
      toast(
        "success",
        issue ? "Copied. Paste it into a GitHub issue." : `Copied. Paste it into an email to ${SUPPORT_EMAIL}.`,
      );
    } catch {
      toast(
        "error",
        issue
          ? "Could not copy. Open a GitHub issue and paste the report yourself."
          : `Could not copy. Write to ${SUPPORT_EMAIL} directly.`,
      );
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
          hint={
            issue
              ? "This will be public. Leave out passwords, tokens, and anything else you would not post."
              : "Leave out passwords and anything else you would not put in an email."
          }
        >
          <Textarea value={message} onChange={(e) => setMessage(e.target.value)} rows={6} required />
        </Field>
        <p className="text-xs text-muted-foreground">
          {issue
            ? "This opens a GitHub issue. Argus sends nothing itself. The draft adds the Argus version, the page you are on, and your browser. It does not include this install's instance ID. You can edit or remove any of it before you submit."
            : `This opens a draft in your email app, addressed to ${SUPPORT_EMAIL}. Argus sends nothing itself. The draft adds your Argus version, this install's instance ID, the page you are on and your browser; you can edit or remove any of it before sending.`}
        </p>
        <div className="flex flex-wrap justify-end gap-2">
          <Button type="button" variant="ghost" size="sm" onClick={copy} disabled={!ready}>
            Copy instead
          </Button>
          <Button type="button" variant="outline" size="sm" onClick={onClose}>
            Cancel
          </Button>
          <Button type="submit" size="sm" disabled={!ready}>
            {issue ? "Open GitHub issue" : "Open email draft"}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
