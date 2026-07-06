import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

// A small anchored popover: a caller-supplied trigger toggles a menu that
// closes on outside-click or Escape. Used by the new-task overlay, row hover
// actions, inline reschedule, the bulk bar, and the header's View menu.
//
// Rendered in a portal (like ContextMenu) rather than inline: a trigger near
// the bottom edge of a modal (e.g. the New task overlay's Inbox/project pill)
// would otherwise have its dropdown clipped by the modal's own
// `overflow-hidden`, since an inline `position: absolute` child can't escape
// an ancestor's clip box. `position: fixed` + a portal sidesteps that
// entirely; a layout effect then measures the dropdown once mounted and
// flips it above the trigger when there isn't room below.
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
  const anchorRef = useRef<HTMLDivElement>(null); // the trigger's wrapper — also the position anchor
  const contentRef = useRef<HTMLDivElement>(null); // the portaled dropdown
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null);

  // Measure the anchor + dropdown once mounted and place it: below the
  // trigger by default, flipped above when there isn't room below (the exact
  // clamp-into-viewport technique ContextMenu.tsx already uses for the
  // right-click menu).
  useLayoutEffect(() => {
    if (!open) {
      setPos(null);
      return;
    }
    const anchor = anchorRef.current;
    const content = contentRef.current;
    if (!anchor || !content) return;
    const a = anchor.getBoundingClientRect();
    const c = content.getBoundingClientRect();
    const pad = 6;
    const gap = 4;
    const fitsBelow = a.bottom + gap + c.height <= window.innerHeight - pad;
    const top = fitsBelow ? a.bottom + gap : Math.max(pad, a.top - gap - c.height);
    let left = align === "right" ? a.right - c.width : a.left;
    left = Math.max(pad, Math.min(left, window.innerWidth - c.width - pad));
    setPos({ left, top });
  }, [open, align]);

  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node;
      // The dropdown is portaled out of anchorRef's subtree, so it needs its
      // own containment check — otherwise every click inside it would read
      // as "outside" and close it immediately.
      if (anchorRef.current?.contains(t) || contentRef.current?.contains(t)) return;
      setOpen(false);
    };
    // Capture phase + stopPropagation: a popover nested in a modal (New task,
    // FilterBuilder) must swallow its own Escape before it reaches the
    // modal's onKeyDown, which would otherwise also close the whole dialog.
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        setOpen(false);
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey, true);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey, true);
    };
  }, [open]);

  return (
    <div ref={anchorRef} className="relative">
      {trigger({ open, toggle: () => setOpen((o) => !o) })}
      {open &&
        createPortal(
          <div
            ref={contentRef}
            style={{
              position: "fixed",
              left: pos?.left ?? -9999,
              top: pos?.top ?? -9999,
              // Hide the un-positioned first pass (before the layout effect
              // measures and places it) instead of flashing it at 0,0.
              visibility: pos ? "visible" : "hidden",
            }}
            className="z-50 min-w-[180px] rounded-lg border border-line bg-surface p-1 shadow-xl"
            onMouseDown={(e) => e.stopPropagation()}
            onClick={(e) => e.stopPropagation()}
          >
            {children(() => setOpen(false))}
          </div>,
          document.body,
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
