import { useCallback, useEffect, useRef, useState } from "react";
import { clampWidth, type WidthBounds } from "../lib/layout";

// One reusable piece drives all three resizable panels (left sidebar, detail
// panel, extension panel wrappers): `useResizable` owns the live width +
// persistence + viewport re-clamp; `ResizeHandle` is the draggable separator.
// The pointer/keyboard math lives here once, never triplicated at call sites.

const KEY_STEP = 16; // px per ArrowLeft/ArrowRight press

// The live viewport width, so a stored preference re-clamps for display when
// the window resizes — without overwriting the preference (that only happens on
// an explicit drag/keyboard/double-click commit).
function useViewportWidth(): number {
  const [w, setW] = useState(() => (typeof window === "undefined" ? 1280 : window.innerWidth));
  useEffect(() => {
    const onResize = () => setW(window.innerWidth);
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);
  return w;
}

export interface Resizable {
  /** The display width: the stored preference clamped to the current viewport. */
  width: number;
  bounds: WidthBounds;
  /** Live drag update — clamps + updates the display, no persist. */
  onResize: (width: number) => void;
  /** Persist the final width (pointerup / keyboard / after a live drag). */
  onCommit: (width: number) => void;
  /** Reset to the panel's default and persist. */
  onReset: () => void;
}

/**
 * State + persistence for one resizable panel. `read`/`write` are the
 * lib/layout scalar helpers for this panel; `bounds` its clamp. The stored
 * preference is only rewritten on an explicit commit/reset — a passive window
 * resize re-clamps for display but leaves the preference intact.
 */
export function useResizable(config: {
  read: () => number;
  write: (width: number) => void;
  bounds: WidthBounds;
}): Resizable {
  const { read, write, bounds } = config;
  const [stored, setStored] = useState(read);
  const vw = useViewportWidth();
  const width = clampWidth(stored, bounds, vw);
  return {
    width,
    bounds,
    onResize: useCallback((w: number) => setStored(clampWidth(w, bounds, vw)), [bounds, vw]),
    onCommit: useCallback(
      (w: number) => {
        const c = clampWidth(w, bounds, vw);
        setStored(c);
        write(c);
      },
      [bounds, vw, write],
    ),
    onReset: useCallback(() => {
      setStored(bounds.default);
      write(bounds.default);
    }, [bounds, write]),
  };
}

// While dragging, suppress text selection and force the resize cursor
// document-wide so a fast pointer that outruns the slim handle still reads as a
// resize (and never selects the list underneath).
function setDragging(on: boolean): void {
  if (typeof document === "undefined") return;
  document.body.style.userSelect = on ? "none" : "";
  document.body.style.cursor = on ? "col-resize" : "";
}

/**
 * A slim (6px) draggable divider on one edge of a resizable panel. A handle on
 * the panel's right edge grows it as the pointer moves right; a left-edge handle
 * grows it as the pointer moves left. Double-click resets; ArrowLeft/ArrowRight
 * nudge by 16px when focused. Rendered as an ARIA vertical separator.
 */
export function ResizeHandle({
  edge,
  width,
  bounds,
  label,
  onResize,
  onCommit,
  onReset,
}: {
  edge: "left" | "right";
  width: number;
  bounds: WidthBounds;
  label: string;
  onResize: (width: number) => void;
  onCommit: (width: number) => void;
  onReset: () => void;
}) {
  // Drag origin + latest candidate width, tracked across pointer moves.
  const drag = useRef<{ startX: number; startWidth: number; width: number } | null>(null);
  // +1: pointer moving right grows the panel (right-edge handle).
  // -1: pointer moving left grows the panel (left-edge handle).
  const sign = edge === "right" ? 1 : -1;

  const onPointerDown = (e: React.PointerEvent) => {
    if (e.button !== 0) return;
    e.preventDefault();
    drag.current = { startX: e.clientX, startWidth: width, width };
    e.currentTarget.setPointerCapture(e.pointerId);
    setDragging(true);
  };
  const onPointerMove = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d) return;
    const next = d.startWidth + sign * (e.clientX - d.startX);
    d.width = next;
    onResize(next);
  };
  const endDrag = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d) return;
    drag.current = null;
    setDragging(false);
    if (e.currentTarget.hasPointerCapture(e.pointerId)) {
      e.currentTarget.releasePointerCapture(e.pointerId);
    }
    onCommit(d.width);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
      e.preventDefault();
      const dir = e.key === "ArrowRight" ? 1 : -1;
      onCommit(width + sign * dir * KEY_STEP);
    }
  };

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label={label}
      aria-valuemin={bounds.min}
      aria-valuemax={bounds.max}
      aria-valuenow={Math.round(width)}
      tabIndex={0}
      data-testid="resize-handle"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onKeyDown={onKeyDown}
      onDoubleClick={onReset}
      className={`group absolute top-0 z-20 h-full w-1.5 cursor-col-resize touch-none select-none ${
        edge === "right" ? "-right-[3px]" : "-left-[3px]"
      }`}
    >
      {/* A hairline that brightens on hover/focus — the visible affordance. */}
      <div className="pointer-events-none absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-transparent transition-colors group-hover:bg-accent/40 group-focus-visible:bg-accent/60" />
    </div>
  );
}
