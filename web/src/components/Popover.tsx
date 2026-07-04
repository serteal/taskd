import { useEffect, useRef, useState } from "react";

// A small anchored popover: a caller-supplied trigger toggles a menu that
// closes on outside-click or Escape. Used by the new-task overlay, row hover
// actions, inline reschedule, and the bulk bar.
export function Popover({
  trigger,
  children,
  align = "left",
}: {
  trigger: (p: { open: boolean; toggle: () => void }) => React.ReactNode;
  children: (close: () => void) => React.ReactNode;
  align?: "left" | "right";
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  return (
    <div ref={ref} className="relative">
      {trigger({ open, toggle: () => setOpen((o) => !o) })}
      {open && (
        <div
          className={`absolute ${align === "right" ? "right-0" : "left-0"} top-full z-50 mt-1 min-w-[180px] rounded-lg border border-line bg-surface p-1 shadow-xl`}
          onMouseDown={(e) => e.stopPropagation()}
          onClick={(e) => e.stopPropagation()}
        >
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  );
}

export function MenuItem({
  onClick,
  children,
  hint,
}: {
  onClick: () => void;
  children: React.ReactNode;
  hint?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center justify-between gap-3 rounded-md px-2 py-1.5 text-left text-[13px] text-ink hover:bg-ink/[.05] dark:hover:bg-ink/[.08]"
    >
      <span>{children}</span>
      {hint && <span className="font-mono text-[11px] text-faint">{hint}</span>}
    </button>
  );
}

// A rounded pill trigger (the new-task overlay's control style).
export function PillButton({
  icon,
  label,
  active,
  tone,
  onClick,
  onClear,
}: {
  icon?: React.ReactNode;
  label: React.ReactNode;
  active?: boolean;
  tone?: "accent" | "warn";
  onClick: () => void;
  onClear?: () => void;
}) {
  const toneClass = active
    ? tone === "warn"
      ? "border-warn/50 text-warn"
      : "border-accent/50 text-accent"
    : "border-line text-ink hover:border-mute";
  return (
    <button
      type="button"
      onClick={onClick}
      className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[12.5px] ${toneClass}`}
    >
      {icon}
      <span className="capitalize">{label}</span>
      {onClear && (
        <span
          role="button"
          aria-label="Clear"
          onClick={(e) => {
            e.stopPropagation();
            onClear();
          }}
          className="ml-0.5 text-mute hover:text-warn"
        >
          ×
        </span>
      )}
    </button>
  );
}
