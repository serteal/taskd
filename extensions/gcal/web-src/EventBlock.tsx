// A single positioned block in a day column: either a calendar event (filled,
// accent, read-only) or a user timebox (outlined, ink). Timeboxes are
// interactive — a body drag to move, top/bottom handles to resize, a ✕ to
// clear, and full keyboard control when focused. Geometry
// (top/height/left/width) is computed by the caller; this component only
// wires it to the DOM.

import { useState } from "react";
import type { CSSProperties, KeyboardEvent, PointerEvent } from "react";
import { MONO } from "./util";

export interface EventBlockProps {
  top: number;
  height: number;
  left: string; // e.g. "0%"
  width: string; // e.g. "50%"
  /** Width to use while hovered — lets a narrow overlapped block expand to
   *  reveal its text (falls back to `width`). */
  expandWidth?: string;
  title: string;
  time: string;
  variant: "event" | "timebox";
  /** Open the task's detail (events: click; timeboxes: Enter/Space). */
  onOpen: () => void;
  onRemove?: () => void; // present → renders the clear-timebox ✕ (timeboxes)
  // Timebox interaction (all optional; wired only for timeboxes).
  interactive?: boolean;
  selected?: boolean;
  onPointerDownMove?: (e: PointerEvent) => void;
  onPointerDownResize?: (e: PointerEvent) => void; // bottom edge (end time)
  onPointerDownResizeTop?: (e: PointerEvent) => void; // top edge (start time)
  onKeyDown?: (e: KeyboardEvent) => void;
  onFocus?: () => void;
  onBlur?: () => void;
}

export function EventBlock({
  top,
  height,
  left,
  width,
  expandWidth,
  title,
  time,
  variant,
  onOpen,
  onRemove,
  interactive,
  selected,
  onPointerDownMove,
  onPointerDownResize,
  onPointerDownResizeTop,
  onKeyDown,
  onFocus,
  onBlur,
}: EventBlockProps) {
  const [hover, setHover] = useState(false);
  const isEvent = variant === "event";
  const h = Math.max(height, 14);
  const raised = hover || selected;
  const style: CSSProperties = {
    position: "absolute",
    top,
    height: h,
    left,
    width: raised && expandWidth ? expandWidth : width,
    boxSizing: "border-box",
    padding: "2px 5px",
    borderRadius: 5,
    overflow: "hidden",
    cursor: interactive ? "grab" : "pointer",
    color: "var(--ink)",
    touchAction: interactive ? "none" : undefined,
    zIndex: selected ? 3 : hover ? 2 : 1,
    boxShadow: raised ? "0 2px 8px color-mix(in srgb, var(--ink) 22%, transparent)" : undefined,
    outline: selected ? "2px solid var(--accent)" : "none",
    outlineOffset: -1,
    background: isEvent
      ? "color-mix(in srgb, var(--accent) 16%, var(--surface))"
      : "color-mix(in srgb, var(--ink) 6%, var(--surface))",
    border: isEvent
      ? "1px solid color-mix(in srgb, var(--accent) 42%, transparent)"
      : "1px dashed var(--muted)",
    borderLeft: `3px solid ${isEvent ? "var(--accent)" : "var(--ink)"}`,
  };
  return (
    <div
      style={style}
      data-testid={isEvent ? "cal-event" : "cal-timebox"}
      data-title={title}
      tabIndex={interactive ? 0 : undefined}
      role={interactive ? "button" : undefined}
      aria-label={interactive ? `Timebox ${title}, ${time}` : undefined}
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
      onClick={interactive ? undefined : onOpen}
      onPointerDown={onPointerDownMove}
      onKeyDown={onKeyDown}
      onFocus={onFocus}
      onBlur={onBlur}
      title={`${title} · ${time}`}
    >
      <div style={{ display: "flex", alignItems: "flex-start", justifyContent: "space-between", gap: 3 }}>
        <span
          style={{
            fontSize: 11,
            fontWeight: 500,
            lineHeight: 1.25,
            whiteSpace: "nowrap",
            overflow: "hidden",
            textOverflow: "ellipsis",
          }}
        >
          {title}
        </span>
        {onRemove && (
          <button
            onClick={(e) => {
              e.stopPropagation();
              onRemove();
            }}
            onPointerDown={(e) => e.stopPropagation()}
            aria-label="Remove timebox"
            style={{
              flexShrink: 0,
              lineHeight: 1,
              padding: 0,
              margin: 0,
              border: "none",
              background: "transparent",
              color: "var(--muted)",
              cursor: "pointer",
              fontSize: 11,
            }}
          >
            ✕
          </button>
        )}
      </div>
      {h >= 28 && (
        <div style={{ fontFamily: MONO, fontSize: 10, color: "var(--muted)", marginTop: 1 }}>{time}</div>
      )}
      {interactive && onPointerDownResizeTop && (
        <div
          data-testid="cal-timebox-resize-top"
          aria-hidden="true"
          onPointerDown={(e) => {
            e.stopPropagation();
            onPointerDownResizeTop(e);
          }}
          style={{
            position: "absolute",
            left: 0,
            right: 0,
            top: 0,
            height: 7,
            cursor: "ns-resize",
            // A subtle grip when the block is roomy enough to show it.
            borderTop: raised ? "2px solid color-mix(in srgb, var(--ink) 45%, transparent)" : "none",
          }}
        />
      )}
      {interactive && onPointerDownResize && (
        <div
          data-testid="cal-timebox-resize"
          aria-hidden="true"
          onPointerDown={(e) => {
            e.stopPropagation();
            onPointerDownResize(e);
          }}
          style={{
            position: "absolute",
            left: 0,
            right: 0,
            bottom: 0,
            height: 7,
            cursor: "ns-resize",
            // A subtle grip when the block is roomy enough to show it.
            borderBottom: raised ? "2px solid color-mix(in srgb, var(--ink) 45%, transparent)" : "none",
          }}
        />
      )}
    </div>
  );
}
