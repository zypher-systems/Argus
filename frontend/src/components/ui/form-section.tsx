import { useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "../../lib/utils";

export function FormSection({
  title,
  hint,
  defaultOpen = false,
  children,
}: {
  title: string;
  hint?: string;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(defaultOpen);
  return (
    <fieldset className="rounded-[var(--radius)] border border-border bg-muted/30 px-4 py-3">
      <legend className="w-full">
        <button
          type="button"
          className="flex w-full items-center justify-between gap-3 rounded py-1 text-left text-sm font-semibold focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={open}
          onClick={() => setOpen((v) => !v)}
        >
          {title}
          <ChevronDown
            size={16}
            className={cn("shrink-0 text-muted-foreground transition", open && "rotate-180")}
          />
        </button>
      </legend>
      {open && (
        <div className="mt-3 flex flex-col gap-5">
          {hint && <p className="-mt-2 text-xs text-muted-foreground">{hint}</p>}
          {children}
        </div>
      )}
    </fieldset>
  );
}
