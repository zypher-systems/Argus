import { useState } from "react";
import { useSearchParams } from "react-router-dom";
import { Loader2 } from "lucide-react";
import { Button } from "../components/ui/button";
import { api } from "../lib/api";

/**
 * Where the unsubscribe link in a status page email lands. It asks before it
 * acts: mail scanners open links to check them, and that must not end
 * someone's subscription.
 */
export function SubscribeUnsubscribe() {
  const [params] = useSearchParams();
  const token = params.get("token") || "";
  const [state, setState] = useState<"ask" | "working" | "done" | "gone">(token ? "ask" : "gone");

  const unsubscribe = async () => {
    setState("working");
    try {
      await api.status.unsubscribe(token);
      setState("done");
    } catch {
      setState("gone");
    }
  };

  return (
    <main className="flex min-h-screen flex-col items-center justify-center px-6 text-center">
      {(state === "ask" || state === "working") && (
        <>
          <h1 className="font-brand text-xl tracking-tight">Stop incident emails?</h1>
          <p className="mt-2 max-w-md text-sm text-muted-foreground">
            You will no longer get an email when this status page opens, updates, or resolves an
            incident. You can subscribe again from the status page at any time.
          </p>
          <Button className="mt-6" onClick={unsubscribe} disabled={state === "working"}>
            {state === "working" && <Loader2 size={14} className="animate-spin" />}
            Unsubscribe
          </Button>
        </>
      )}
      {state === "done" && (
        <>
          <h1 className="font-brand text-xl tracking-tight">You are unsubscribed</h1>
          <p className="mt-2 max-w-md text-sm text-muted-foreground">
            No more incident emails will be sent to this address from this status page.
          </p>
        </>
      )}
      {state === "gone" && (
        <>
          <h1 className="font-brand text-xl tracking-tight">Already unsubscribed</h1>
          <p className="mt-2 max-w-md text-sm text-muted-foreground">
            This link does not match a current subscription, so there is nothing to stop.
          </p>
        </>
      )}
    </main>
  );
}
