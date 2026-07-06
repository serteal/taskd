// A single day's hour column: faint gridlines, an optional "now" line, and
// positioned event/timebox blocks — and an HTML5 drop target that timeboxes a
// task dragged in from the core list. Timeboxes placed here can be moved
// (body drag), resized (top/bottom handles), and driven by keyboard when
// focused.
//
// Two distinct gestures, deliberately kept apart:
//   • CREATE — an HTML5 drop of a task dragged in from OUTSIDE the panel
//     (core rows are the drag sources; the id rides api.dnd on the
//     dataTransfer). Handled by onDrop below.
//   • MOVE / RESIZE — a pointer drag that starts ON an existing timebox block.
//     Pointer-based (not HTML5 DnD), so it never collides with the drop path
//     and gives a live snapped preview.

import { useEffect, useRef, useState } from "react";
import type { CSSProperties, KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent } from "react";
import type { Task } from "@taskd/extension-api";
import { EventBlock } from "./EventBlock";
import {
  HOUR_END,
  HOUR_START,
  RESIZE_SNAP_MIN,
  SNAP_MIN,
  assignLanes,
  blockRect,
  dropMinutes,
  eventInterval,
  fmtRange,
  getTimebox,
  gridHeight,
  minuteToY,
  minutesOfDay,
  movedTimebox,
  nudgeDuration,
  nudgeStart,
  resizedTimebox,
  resizedTimeboxStart,
  sameDay,
  type Interval,
} from "./util";

const hours = Array.from({ length: HOUR_END - HOUR_START + 1 }, (_, i) => HOUR_START + i);

interface DayBlock {
  key: string;
  task: Task;
  top: number;
  height: number;
  title: string;
  time: string;
  variant: "event" | "timebox";
  /** The source interval, carried so a move/resize can preserve duration. */
  iv: Interval;
}

// Timed events + timeboxes that fall on `day`, positioned in the visible band.
function blocksForDay(day: Date, timedEvents: Task[], timeboxed: Task[], pxPerMin: number): DayBlock[] {
  const out: DayBlock[] = [];
  for (const t of timedEvents) {
    const iv = eventInterval(t);
    if (!iv || !sameDay(iv.start, day)) continue;
    const rect = blockRect(iv.start, iv.end, pxPerMin);
    if (!rect) continue;
    out.push({ key: "ev:" + t.id, task: t, ...rect, title: t.title, time: fmtRange(iv.start, iv.end), variant: "event", iv });
  }
  for (const t of timeboxed) {
    const iv = getTimebox(t);
    if (!iv || !sameDay(iv.start, day)) continue;
    const rect = blockRect(iv.start, iv.end, pxPerMin);
    if (!rect) continue;
    out.push({ key: "tb:" + t.id, task: t, ...rect, title: t.title, time: fmtRange(iv.start, iv.end), variant: "timebox", iv });
  }
  return out;
}

interface DragState {
  key: string;
  task: Task;
  mode: "move" | "resize-start" | "resize-end";
  startClientY: number;
  origIv: Interval;
}

export interface DayColumnProps {
  day: Date;
  isToday: boolean;
  now: Date;
  pxPerMin: number;
  timedEvents: Task[];
  timeboxed: Task[];
  onOpen: (id: string) => void;
  /** Replace a timebox's interval (move, resize, keyboard nudge). */
  onSetTimebox: (task: Task, iv: Interval) => void;
  onClearTimebox: (task: Task) => void;
  /** Create a timebox from a drop: task id, the column's day, a snapped start
   *  minute-of-day. */
  onCreateTimebox: (id: string, day: Date, startMin: number) => void;
  /** Reads a dragged core task's id off the drop event (api.dnd.readTaskId). */
  readTaskId: (dt: DataTransfer) => string | null;
}

export function DayColumn({
  day,
  isToday,
  now,
  pxPerMin,
  timedEvents,
  timeboxed,
  onOpen,
  onSetTimebox,
  onClearTimebox,
  onCreateTimebox,
  readTaskId,
}: DayColumnProps) {
  const [dragOver, setDragOver] = useState(false);
  const [focusedKey, setFocusedKey] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const dragRef = useRef<DragState | null>(null);
  const previewRef = useRef<Interval | null>(null);
  const [preview, setPreviewState] = useState<Interval | null>(null);
  const setPreview = (iv: Interval | null) => {
    previewRef.current = iv;
    setPreviewState(iv);
  };

  const height = gridHeight(pxPerMin);
  const nowMin = minutesOfDay(now);
  const showNow = isToday && nowMin >= HOUR_START * 60 && nowMin <= HOUR_END * 60;

  // A live move/resize drag attaches window listeners imperatively (not via an
  // effect) so there is no gap in which a fast pointerup could be missed. Using
  // the window — not the block — keeps the drag alive past the block's edges.
  const cleanupRef = useRef<(() => void) | undefined>(undefined);
  useEffect(() => () => cleanupRef.current?.(), []); // tidy up on unmount

  const begin = (mode: "move" | "resize-start" | "resize-end", e: ReactPointerEvent, block: DayBlock) => {
    if (e.button !== 0) return; // primary button only
    e.preventDefault();
    cleanupRef.current?.(); // never stack two drags
    const origIv = block.iv;
    const startClientY = e.clientY;
    dragRef.current = { key: block.key, task: block.task, mode, startClientY, origIv };
    setPreview(origIv);
    setDragging(true);

    const onMove = (ev: PointerEvent) => {
      const delta = ev.clientY - startClientY;
      setPreview(
        mode === "move"
          ? movedTimebox(origIv, delta, pxPerMin)
          : mode === "resize-start"
            ? resizedTimeboxStart(origIv, delta, pxPerMin)
            : resizedTimebox(origIv, delta, pxPerMin),
      );
    };
    const onUp = () => {
      cleanupRef.current?.();
      const iv = previewRef.current;
      setDragging(false);
      dragRef.current = null;
      setPreview(null);
      if (!iv) return;
      const changed = iv.start.getTime() !== origIv.start.getTime() || iv.end.getTime() !== origIv.end.getTime();
      if (changed) onSetTimebox(block.task, iv);
      else if (mode === "move") onOpen(block.task.id); // a press without a drag = a click
    };
    const cleanup = () => {
      window.removeEventListener("pointermove", onMove);
      window.removeEventListener("pointerup", onUp);
      window.removeEventListener("pointercancel", onUp);
      cleanupRef.current = undefined;
    };
    cleanupRef.current = cleanup;
    window.addEventListener("pointermove", onMove);
    window.addEventListener("pointerup", onUp);
    window.addEventListener("pointercancel", onUp);
  };

  const onBlockKeyDown = (e: ReactKeyboardEvent, block: DayBlock) => {
    const iv = block.iv;
    switch (e.key) {
      case "ArrowUp":
      case "ArrowDown": {
        e.preventDefault();
        const dir = e.key === "ArrowUp" ? -1 : 1;
        const next = e.shiftKey ? nudgeDuration(iv, dir * RESIZE_SNAP_MIN) : nudgeStart(iv, dir * SNAP_MIN);
        onSetTimebox(block.task, next);
        break;
      }
      case "Delete":
      case "Backspace":
        e.preventDefault();
        onClearTimebox(block.task);
        break;
      case "Enter":
      case " ":
        e.preventDefault();
        onOpen(block.task.id);
        break;
    }
  };

  // Build blocks, swapping the dragged block's rect for its live preview so
  // lanes reflect where it currently sits.
  let dayBlocks = blocksForDay(day, timedEvents, timeboxed, pxPerMin);
  const d = dragRef.current;
  if (dragging && preview && d) {
    const r = blockRect(preview.start, preview.end, pxPerMin);
    if (r) {
      dayBlocks = dayBlocks.map((b) =>
        b.key === d.key ? { ...b, top: r.top, height: r.height, time: fmtRange(preview.start, preview.end), iv: preview } : b,
      );
    }
  }
  const { placements } = assignLanes(dayBlocks);

  const style: CSSProperties = {
    flex: 1,
    minWidth: 0,
    position: "relative",
    height,
    borderLeft: "1px solid var(--line)",
    background: dragOver
      ? "color-mix(in srgb, var(--accent) 12%, transparent)"
      : isToday
        ? "color-mix(in srgb, var(--accent) 5%, transparent)"
        : "transparent",
  };

  return (
    <div
      style={style}
      data-testid="cal-daycolumn"
      data-today={isToday}
      onDragOver={(e) => {
        e.preventDefault();
        e.dataTransfer.dropEffect = "move";
        if (!dragOver) setDragOver(true);
      }}
      onDragLeave={() => setDragOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setDragOver(false);
        const id = readTaskId(e.dataTransfer);
        if (!id) return;
        const rect = e.currentTarget.getBoundingClientRect();
        onCreateTimebox(id, day, dropMinutes(e.clientY - rect.top, pxPerMin));
      }}
    >
      {/* Hour gridlines */}
      {hours.map((h) => (
        <div
          key={h}
          style={{
            position: "absolute",
            left: 0,
            right: 0,
            top: minuteToY(h * 60, pxPerMin),
            borderTop: "1px solid var(--line)",
            opacity: 0.6,
          }}
        />
      ))}

      {/* Now line (today only) */}
      {showNow && (
        <div
          data-testid="cal-nowline"
          style={{
            position: "absolute",
            left: 0,
            right: 0,
            top: minuteToY(nowMin, pxPerMin),
            borderTop: "1.5px solid var(--warn)",
            // Sit above the gridlines (tree order) but below event/timebox
            // blocks (z-index >= 1) so the line stays visible over empty grid
            // yet never clips the title of an event starting right at "now".
            zIndex: 0,
          }}
        >
          <div
            style={{
              position: "absolute",
              left: -3,
              top: -3.5,
              width: 6,
              height: 6,
              borderRadius: "50%",
              background: "var(--warn)",
            }}
          />
        </div>
      )}

      {/* Event / timebox blocks */}
      {placements.map(({ block, lane, span, cols }) => {
        const unit = 100 / cols;
        const isTimebox = block.variant === "timebox";
        return (
          <EventBlock
            key={block.key}
            top={block.top}
            height={block.height}
            left={`calc(${lane * unit}% + 1px)`}
            width={`calc(${span * unit}% - 2px)`}
            expandWidth={`calc(${(cols - lane) * unit}% - 2px)`}
            title={block.title}
            time={block.time}
            variant={block.variant}
            onOpen={() => onOpen(block.task.id)}
            onRemove={isTimebox ? () => onClearTimebox(block.task) : undefined}
            interactive={isTimebox}
            selected={focusedKey === block.key}
            onPointerDownMove={isTimebox ? (e) => begin("move", e, block) : undefined}
            onPointerDownResizeTop={isTimebox ? (e) => begin("resize-start", e, block) : undefined}
            onPointerDownResize={isTimebox ? (e) => begin("resize-end", e, block) : undefined}
            onKeyDown={isTimebox ? (e) => onBlockKeyDown(e, block) : undefined}
            onFocus={isTimebox ? () => setFocusedKey(block.key) : undefined}
            onBlur={isTimebox ? () => setFocusedKey((k) => (k === block.key ? null : k)) : undefined}
          />
        );
      })}
    </div>
  );
}
