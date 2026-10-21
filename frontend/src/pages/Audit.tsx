import { useEffect, useState } from "react";
import { api, ApiError } from "../lib/api";
import type { AuditEntry } from "../lib/types";
import { formatDateTime } from "../lib/utils";

export function Audit() {
  const [rows, setRows] = useState<AuditEntry[] | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .audit(200)
      .then(setRows)
      .catch((err) => {
        setError(err instanceof ApiError ? err.message : "failed to load audit log");
        setRows([]);
      });
  }, []);

  return (
    <div className="animate-fade-in-up">
      <h1 className="font-brand text-lg tracking-tight">Audit log</h1>
      <p className="mt-1 mb-6 text-sm text-muted-foreground">
        Mutating API calls from signed-in users. Probe heartbeats are not recorded.
      </p>
      {error && <p className="mb-4 text-sm text-danger">{error}</p>}
      <div className="surface overflow-x-auto">
        <table className="w-full text-left text-sm">
          <thead>
            <tr className="text-xs text-muted-foreground">
              <th className="py-2 font-medium">When</th>
              <th className="py-2 font-medium">Who</th>
              <th className="py-2 font-medium">Action</th>
              <th className="py-2 font-medium">IP</th>
            </tr>
          </thead>
          <tbody>
            {(rows ?? []).map((row) => (
              <tr key={row.id} className="border-t border-border">
                <td className="py-2 whitespace-nowrap">{formatDateTime(row.ts)}</td>
                <td className="py-2">{row.email || "—"}</td>
                <td className="py-2 font-mono text-xs">{row.action}</td>
                <td className="py-2 text-muted-foreground">{row.ip || "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
        {rows && rows.length === 0 && !error && (
          <p className="py-8 text-sm text-muted-foreground">No audit events yet.</p>
        )}
      </div>
    </div>
  );
}
