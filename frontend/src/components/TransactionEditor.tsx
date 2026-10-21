import { ArrowDown, ArrowUp, Plus, Trash2 } from "lucide-react";
import { Button } from "./ui/button";
import { Field, Input, Select, Textarea } from "./ui/controls";
import type { StepExtract, TransactionStep } from "../lib/types";

export const MAX_STEPS = 10;
const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE", "HEAD"];

/** A step as the editor holds it: headers as text, like the HTTP monitor. */
export interface EditorStep {
  name: string;
  method: string;
  url: string;
  headers_text: string;
  body: string;
  expect_status: number;
  keyword: string;
  extract: StepExtract[];
}

/** A secret row. `stored` means the server has a value we were not sent. */
export interface EditorSecret {
  name: string;
  value: string;
  stored: boolean;
}

export const emptyStep = (): EditorStep => ({
  name: "",
  method: "GET",
  url: "",
  headers_text: "",
  body: "",
  expect_status: 0,
  keyword: "",
  extract: [],
});

function headersFromText(text: string): Record<string, string> | undefined {
  const out: Record<string, string> = {};
  for (const line of text.split("\n")) {
    const idx = line.indexOf(":");
    if (idx <= 0) continue;
    const key = line.slice(0, idx).trim();
    if (key) out[key] = line.slice(idx + 1).trim();
  }
  return Object.keys(out).length ? out : undefined;
}

function headersToText(headers?: Record<string, string>): string {
  if (!headers) return "";
  return Object.entries(headers)
    .map(([k, v]) => `${k}: ${v}`)
    .join("\n");
}

export function stepsToEditor(steps?: TransactionStep[]): EditorStep[] {
  if (!steps?.length) return [emptyStep()];
  return steps.map((s) => ({
    name: s.name || "",
    method: s.method || "GET",
    url: s.url || "",
    headers_text: headersToText(s.headers),
    body: s.body || "",
    expect_status: s.expect_status ?? 0,
    keyword: s.keyword || "",
    extract: s.extract ?? [],
  }));
}

export function stepsFromEditor(steps: EditorStep[]): TransactionStep[] {
  return steps.map((s) => {
    const out: TransactionStep = { name: s.name.trim(), method: s.method, url: s.url.trim() };
    const headers = headersFromText(s.headers_text);
    if (headers) out.headers = headers;
    if (s.body && s.method !== "GET" && s.method !== "HEAD") out.body = s.body;
    if (s.expect_status) out.expect_status = s.expect_status;
    if (s.keyword.trim()) out.keyword = s.keyword.trim();
    const extract = s.extract.filter((x) => x.name.trim() || x.path.trim());
    if (extract.length) out.extract = extract;
    return out;
  });
}

export function secretsToEditor(secrets?: Record<string, string>): EditorSecret[] {
  return Object.keys(secrets ?? {})
    .sort()
    .map((name) => ({ name, value: "", stored: true }));
}

/** Blank values on stored secrets mean "keep"; the server fills them in. */
export function secretsFromEditor(rows: EditorSecret[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const r of rows) {
    const name = r.name.trim();
    if (name) out[name] = r.value;
  }
  return out;
}

function IconButton({
  label,
  onClick,
  disabled,
  children,
}: {
  label: string;
  onClick: () => void;
  disabled?: boolean;
  children: React.ReactNode;
}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      aria-label={label}
      title={label}
      onClick={onClick}
      disabled={disabled}
      className="h-8 w-8"
    >
      {children}
    </Button>
  );
}

function Ref({ children }: { children: React.ReactNode }) {
  return (
    <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs text-foreground">{children}</code>
  );
}

export function TransactionEditor({
  steps,
  secrets,
  editing,
  onSteps,
  onSecrets,
}: {
  steps: EditorStep[];
  secrets: EditorSecret[];
  editing: boolean;
  onSteps: (steps: EditorStep[]) => void;
  onSecrets: (secrets: EditorSecret[]) => void;
}) {
  const update = (i: number, patch: Partial<EditorStep>) =>
    onSteps(steps.map((s, j) => (j === i ? { ...s, ...patch } : s)));
  const move = (i: number, by: number) => {
    const next = [...steps];
    const [s] = next.splice(i, 1);
    next.splice(i + by, 0, s);
    onSteps(next);
  };
  const updateExtract = (i: number, k: number, patch: Partial<StepExtract>) =>
    update(i, { extract: steps[i].extract.map((x, j) => (j === k ? { ...x, ...patch } : x)) });

  // What each step can refer to: everything extracted before it.
  const availableAt = (i: number) =>
    steps
      .slice(0, i)
      .flatMap((s) => s.extract.map((x) => x.name.trim()))
      .filter(Boolean);
  const secretNames = secrets.map((s) => s.name.trim()).filter(Boolean);

  return (
    <div className="flex flex-col gap-4">
      <p className="text-sm text-muted-foreground">
        Steps run in order and share cookies, so a login carries into the steps after it. The
        first failing step fails the check and is named in the alert. Use <Ref>{"{{name}}"}</Ref>{" "}
        for a value an earlier step extracted and <Ref>{"{{secret.name}}"}</Ref> for a secret.
      </p>

      <ol className="flex flex-col gap-4" aria-label="Transaction steps">
        {steps.map((step, i) => {
          const vars = availableAt(i);
          const hasBody = step.method !== "GET" && step.method !== "HEAD";
          return (
            <li key={i} className="rounded-lg border border-border p-4">
              <div className="mb-3 flex items-center gap-2">
                <span className="text-xs font-semibold tabular-nums text-muted-foreground">
                  Step {i + 1}
                </span>
                <div className="ml-auto flex items-center">
                  <IconButton label={`Move step ${i + 1} up`} onClick={() => move(i, -1)} disabled={i === 0}>
                    <ArrowUp size={14} />
                  </IconButton>
                  <IconButton
                    label={`Move step ${i + 1} down`}
                    onClick={() => move(i, 1)}
                    disabled={i === steps.length - 1}
                  >
                    <ArrowDown size={14} />
                  </IconButton>
                  <IconButton
                    label={`Remove step ${i + 1}`}
                    onClick={() => onSteps(steps.filter((_, j) => j !== i))}
                    disabled={steps.length === 1}
                  >
                    <Trash2 size={14} />
                  </IconButton>
                </div>
              </div>

              <div className="grid grid-cols-1 gap-4 sm:grid-cols-[8rem_1fr]">
                <Field label="Step name" className="sm:col-span-2">
                  <Input
                    value={step.name}
                    onChange={(e) => update(i, { name: e.target.value })}
                    placeholder={i === 0 ? "Log in" : "List orders"}
                    maxLength={80}
                  />
                </Field>
                <Field label="Method">
                  <Select value={step.method} onChange={(e) => update(i, { method: e.target.value })}>
                    {METHODS.map((m) => (
                      <option key={m} value={m}>
                        {m}
                      </option>
                    ))}
                  </Select>
                </Field>
                <Field label="URL">
                  <Input
                    value={step.url}
                    onChange={(e) => update(i, { url: e.target.value })}
                    placeholder={i === 0 ? "https://app.example.com/api/login" : "https://app.example.com/api/orders"}
                    required
                  />
                </Field>
                <Field
                  label="Headers (optional)"
                  hint="One Name: value per line."
                  className="sm:col-span-2"
                >
                  <Textarea
                    value={step.headers_text}
                    onChange={(e) => update(i, { headers_text: e.target.value })}
                    placeholder={vars.length ? `Authorization: Bearer {{${vars[0]}}}` : "Accept: application/json"}
                    className="min-h-16 font-mono text-xs"
                  />
                </Field>
                {hasBody && (
                  <Field
                    label="Body (optional)"
                    hint="Content-Type defaults to JSON, form, or plain text from the body's shape."
                    className="sm:col-span-2"
                  >
                    <Textarea
                      value={step.body}
                      onChange={(e) => update(i, { body: e.target.value })}
                      placeholder={
                        secretNames.length
                          ? `{"user":"monitor","password":"{{secret.${secretNames[0]}}}"}`
                          : '{"user":"monitor","password":"{{secret.password}}"}'
                      }
                      className="font-mono text-xs"
                    />
                  </Field>
                )}
                <Field label="Expected status" hint="0 = any 2xx or 3xx">
                  <Input
                    type="number"
                    min={0}
                    max={599}
                    value={step.expect_status}
                    onChange={(e) => update(i, { expect_status: Number(e.target.value) })}
                  />
                </Field>
                <Field label="Response contains (optional)">
                  <Input
                    value={step.keyword}
                    onChange={(e) => update(i, { keyword: e.target.value })}
                    placeholder='"status":"ok"'
                  />
                </Field>
              </div>

              <fieldset className="mt-4">
                <legend className="text-xs font-medium text-muted-foreground">
                  Extract for later steps
                </legend>
                {step.extract.length > 0 && (
                  <div className="mt-2 flex flex-col gap-2">
                    {step.extract.map((x, k) => (
                      <div key={k} className="grid grid-cols-[1fr_6.5rem_1.4fr_auto] items-center gap-2">
                        <Input
                          aria-label={`Step ${i + 1} extract ${k + 1} name`}
                          value={x.name}
                          onChange={(e) => updateExtract(i, k, { name: e.target.value })}
                          placeholder="token"
                          className="font-mono text-xs"
                        />
                        <Select
                          aria-label={`Step ${i + 1} extract ${k + 1} source`}
                          value={x.from}
                          onChange={(e) => updateExtract(i, k, { from: e.target.value as StepExtract["from"] })}
                        >
                          <option value="json">JSON</option>
                          <option value="header">Header</option>
                        </Select>
                        <Input
                          aria-label={`Step ${i + 1} extract ${k + 1} ${x.from === "header" ? "header name" : "JSON path"}`}
                          value={x.path}
                          onChange={(e) => updateExtract(i, k, { path: e.target.value })}
                          placeholder={x.from === "header" ? "X-Session-Id" : "data.token"}
                          className="font-mono text-xs"
                        />
                        <IconButton
                          label={`Remove extract ${k + 1} from step ${i + 1}`}
                          onClick={() => update(i, { extract: step.extract.filter((_, j) => j !== k) })}
                        >
                          <Trash2 size={14} />
                        </IconButton>
                      </div>
                    ))}
                  </div>
                )}
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="mt-2"
                  onClick={() => update(i, { extract: [...step.extract, { name: "", from: "json", path: "" }] })}
                >
                  <Plus size={14} /> Extract a value
                </Button>
                <p className="mt-1 text-xs text-muted-foreground">
                  A JSON path is dotted keys, with numbers for array items: <Ref>data.items.0.id</Ref>
                </p>
              </fieldset>

              {vars.length > 0 && (
                <p className="mt-3 text-xs text-muted-foreground">
                  Available here:{" "}
                  {vars.map((v, k) => (
                    <span key={v}>
                      {k > 0 && " "}
                      <Ref>{`{{${v}}}`}</Ref>
                    </span>
                  ))}
                </p>
              )}
            </li>
          );
        })}
      </ol>

      <div>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => onSteps([...steps, emptyStep()])}
          disabled={steps.length >= MAX_STEPS}
        >
          <Plus size={14} /> Add step
        </Button>
        {steps.length >= MAX_STEPS && (
          <span className="ml-3 text-xs text-muted-foreground">
            {MAX_STEPS} steps is the limit.
          </span>
        )}
      </div>

      <fieldset className="rounded-lg border border-border p-4">
        <legend className="px-1 text-sm font-medium">Secrets</legend>
        <p className="mb-3 text-xs text-muted-foreground">
          Passwords and API keys, used as <Ref>{"{{secret.name}}"}</Ref>. Stored encrypted when
          ARGUS_SECRET_KEY is set, never shown again, and blanked out of check evidence.
        </p>
        {secrets.length > 0 && (
          <div className="mb-2 flex flex-col gap-2">
            {secrets.map((row, k) => (
              <div key={k} className="grid grid-cols-[1fr_1.4fr_auto] items-center gap-2">
                <Input
                  aria-label={`Secret ${k + 1} name`}
                  value={row.name}
                  onChange={(e) =>
                    onSecrets(secrets.map((r, j) => (j === k ? { ...r, name: e.target.value } : r)))
                  }
                  placeholder="password"
                  className="font-mono text-xs"
                  readOnly={row.stored}
                  title={row.stored ? "Remove and re-add a secret to rename it" : undefined}
                />
                <Input
                  aria-label={`Secret ${k + 1} value`}
                  type="password"
                  autoComplete="new-password"
                  value={row.value}
                  onChange={(e) =>
                    onSecrets(secrets.map((r, j) => (j === k ? { ...r, value: e.target.value } : r)))
                  }
                  placeholder={row.stored && editing ? "stored — leave blank to keep" : "value"}
                  required={!row.stored}
                />
                <IconButton
                  label={`Remove secret ${row.name || k + 1}`}
                  onClick={() => onSecrets(secrets.filter((_, j) => j !== k))}
                >
                  <Trash2 size={14} />
                </IconButton>
              </div>
            ))}
          </div>
        )}
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={() => onSecrets([...secrets, { name: "", value: "", stored: false }])}
        >
          <Plus size={14} /> Add secret
        </Button>
      </fieldset>
    </div>
  );
}
