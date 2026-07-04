// A single positioned block in a day column: either a calendar event (filled,
// accent) or a user timebox (outlined, ink, with a ✕ to clear it). Purely
// presentational — all geometry is computed by the caller.

import type { CSSProperties } from "react";
import { MONO } from "./util";

export interface EventBlockProps {
  top: number;
  height: number;
  left: string; // e.g. "0%"
  width: string; // e.g. "50%"
  title: string;
  time: string;
  variant: "event" | "timebox";
  onClick: () => void;
  onRemove?: () => void; // present → renders the clear-timebox ✕
}

export function EventBlock({
  top,
  height,
  left,
  width,
  title,
  time,
  variant,
  onClick,
  onRemove,
}: EventBlockProps) {
  const isEvent = variant === "event";
  const h = Math.max(height, 14);
  const style: CSSProperties = {
    position: "absolute",
    top,
    height: h,
    left,
    width,
    boxSizing: "border-box",
    padding: "2px 5px",
    borderRadius: 5,
    overflow: "hidden",
    cursor: "pointer",
    color: "var(--ink)",
    background: isEvent
      ? "color-mix(in srgb, var(--accent) 16%, var(--surface))"
      : "color-mix(in srgb, var(--ink) 6%, var(--surface))",
    border: isEvent
      ? "1px solid color-mix(in srgb, var(--accent) 42%, transparent)"
      : "1px dashed var(--muted)",
    borderLeft: `3px solid ${isEvent ? "var(--accent)" : "var(--ink)"}`,
  };
  return (
    <div style={style} onClick={onClick} title={`${title} · ${time}`}>
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
    </div>
  );
}
