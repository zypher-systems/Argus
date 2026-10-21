import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "../../lib/utils";

/**
 * A button for something that cannot be undone: the first press asks, the
 * second within a few seconds does it. Moving away or waiting cancels.
 * Deleting a user, a channel or a status page with its subscribers used to
 * take one press of an icon.
 */
export function ConfirmButton({
  label,
  ask = "Delete?",
  onConfirm,
  children,
  className,
  disabled,
}: {
  /** What the button does, for screen readers and the tooltip. */
  label: string;
  /** What it shows while waiting for the second press. */
  ask?: string;
  onConfirm: () => void | Promise<void>;
  children: ReactNode;
  className?: string;
  disabled?: boolean;
}) {
  const [armed, setArmed] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  const disarm = () => {
    if (timer.current !== undefined) clearTimeout(timer.current);
    timer.current = undefined;
    setArmed(false);
  };
  useEffect(() => () => clearTimeout(timer.current), []);

  return (
    <button
      type="button"
      disabled={disabled}
      title={label}
      aria-label={armed ? `${label}: press again to confirm` : label}
      onBlur={disarm}
      onClick={() => {
        if (!armed) {
          setArmed(true);
          timer.current = window.setTimeout(disarm, 4000);
          return;
        }
        disarm();
        void onConfirm();
      }}
      className={cn(
        "inline-flex h-9 shrink-0 items-center justify-center gap-1.5 rounded-lg px-2.5 text-[13px] font-medium transition focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50",
        armed
          ? "bg-danger text-danger-foreground"
          : "text-muted-foreground hover:bg-danger/10 hover:text-danger",
        className,
      )}
    >
      {armed ? <span aria-live="polite">{ask}</span> : children}
    </button>
  );
}
