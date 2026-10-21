import {
  createContext,
  useCallback,
  useContext,
  useMemo,
  useState,
  type ReactNode,
} from "react";

export interface Toast {
  id: number;
  kind: "success" | "error" | "info";
  message: string;
}

interface ToastCtx {
  toast: (kind: Toast["kind"], message: string) => void;
}

const Ctx = createContext<ToastCtx>({ toast: () => {} });

let nextId = 1;

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);

  const toast = useCallback((kind: Toast["kind"], message: string) => {
    const id = nextId++;
    setToasts((t) => [...t, { id, kind, message }]);
    setTimeout(() => {
      setToasts((t) => t.filter((x) => x.id !== id));
    }, 5000);
  }, []);

  const value = useMemo(() => ({ toast }), [toast]);

  return (
    <Ctx.Provider value={value}>
      {children}
      <div className="pointer-events-none fixed right-4 top-4 z-[100] flex w-full max-w-sm flex-col gap-2">
        {toasts.map((t) => (
          <div
            key={t.id}
            className={toastClasses(t.kind)}
            role="status"
          >
            <span className="text-sm font-medium">{t.message}</span>
            <button
              type="button"
              aria-label="Dismiss notification"
              className="ml-auto shrink-0 rounded p-1 opacity-60 transition hover:opacity-100"
              onClick={() => setToasts((x) => x.filter((y) => y.id !== t.id))}
            >
              <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round">
                <path d="M18 6 6 18M6 6l12 12" />
              </svg>
            </button>
          </div>
        ))}
      </div>
    </Ctx.Provider>
  );
}

function toastClasses(kind: Toast["kind"]) {
  const base =
    "pointer-events-auto animate-fade-in-up flex items-start gap-3 border p-3 backdrop-blur";
  if (kind === "success")
    return `${base} border-success/30 bg-[color-mix(in_srgb,var(--success)_12%,var(--card))] text-foreground`;
  if (kind === "error")
    return `${base} border-danger/30 bg-[color-mix(in_srgb,var(--danger)_12%,var(--card))] text-foreground`;
  return `${base} border-border bg-background text-foreground`;
}

export function useToast() {
  return useContext(Ctx);
}
