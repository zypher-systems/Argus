import { useEffect, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { api } from "../lib/api";

export function SubscribeConfirm() {
  const [params] = useSearchParams();
  const token = params.get("token") || "";
  const [state, setState] = useState<"working" | "ok" | "err">("working");

  useEffect(() => {
    if (!token) {
      setState("err");
      return;
    }
    let cancelled = false;
    api.status
      .confirm(token)
      .then(() => {
        if (!cancelled) setState("ok");
      })
      .catch(() => {
        if (!cancelled) setState("err");
      });
    return () => {
      cancelled = true;
    };
  }, [token]);

  return (
    <div className="flex min-h-screen flex-col items-center justify-center px-6 text-center">
      {state === "working" && <p className="text-sm text-muted-foreground">Confirming…</p>}
      {state === "ok" && (
        <>
          <h1 className="font-brand text-xl tracking-tight">Subscription confirmed</h1>
          <p className="mt-2 max-w-md text-sm text-muted-foreground">
            You will get an email when this status page opens or updates an incident.
          </p>
        </>
      )}
      {state === "err" && (
        <>
          <h1 className="font-brand text-xl tracking-tight">Link expired</h1>
          <p className="mt-2 max-w-md text-sm text-muted-foreground">
            That confirm link is invalid or already used. Subscribe again from the status page.
          </p>
        </>
      )}
    </div>
  );
}
