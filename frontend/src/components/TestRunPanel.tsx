import { useState } from "react";
import { Loader2, Play } from "lucide-react";
import { Button } from "./ui/button";
import { StepList } from "./StepList";
import { ApiError } from "../lib/api";
import { formatLatency } from "../lib/utils";
import type { TestRunResult } from "../lib/types";

/** Runs the transaction as it stands in the editor, without saving it. */
export function TestRunPanel({ run }: { run: () => Promise<TestRunResult> }) {
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<TestRunResult | null>(null);
  const [problem, setProblem] = useState<string | null>(null);

  const start = async () => {
    setRunning(true);
    setProblem(null);
    try {
      setResult(await run());
    } catch (err) {
      setResult(null);
      setProblem(err instanceof ApiError ? err.message : "test run failed");
    } finally {
      setRunning(false);
    }
  };

  const steps = result?.evidence?.steps ?? [];
  return (
    <div className="rounded-lg border border-border p-4">
      <div className="flex flex-wrap items-center gap-3">
        <Button type="button" variant="outline" size="sm" onClick={start} disabled={running}>
          {running ? <Loader2 size={14} className="animate-spin" /> : <Play size={14} />}
          {running ? "Running…" : "Test run"}
        </Button>
        <span className="text-xs text-muted-foreground">
          Runs once from the Argus server, not remote probes. Nothing is saved.
        </span>
      </div>

      <div aria-live="polite" className="empty:hidden">
        {problem && <p className="mt-3 text-sm text-danger">{problem}</p>}
        {result && (
          <div className="mt-3 flex flex-col gap-3">
            <p className={result.success ? "text-sm font-medium text-success" : "text-sm font-medium text-danger"}>
              {result.success
                ? `All ${steps.length} step${steps.length === 1 ? "" : "s"} passed in ${formatLatency(result.latency_ms)}.`
                : result.error}
            </p>
            {steps.length > 0 && <StepList steps={steps} />}
            {!result.success && result.evidence?.body_snippet && (
              <details className="text-xs">
                <summary className="cursor-pointer text-muted-foreground">Response from the failing step</summary>
                <pre className="mt-2 max-h-48 overflow-auto whitespace-pre-wrap break-words rounded bg-muted p-2 font-mono text-muted-foreground">
                  {result.evidence.body_snippet}
                </pre>
              </details>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
