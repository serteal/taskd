// A single day's hour column: faint gridlines, an optional "now" line, and
// positioned event/timebox blocks — and, crucially, an HTML5 drop target that
// timeboxes a task dragged in from the core list. Extracted from the old week
// grid; the day-rail panel shows exactly one of these.

import { useState } from "react";
import type { CSSProperties } from "react";
import type { Task } from "@taskd/extension-api";
import { EventBlock } from "./EventBlock";
import {
  GRID_HEIGHT,
  HOUR_END,
  HOUR_START,
  PX_PER_MIN,
  assignLanes,
  blockRect,
  eventInterval,
  fmtRange,
  getTimebox,
  minutesOfDay,
  sameDay,
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
}

// Timed events + timeboxes that fall on `day`, positioned in the visible band.
function blocksForDay(day: Date, timedEvents: Task[], timeboxed: Task[]): DayBlock[] {
  const out: DayBlock[] = [];
  for (const t of timedEvents) {
    const iv = eventInterval(t);
    if (!iv || !sameDay(iv.start, day)) continue;
    const rect = blockRect(iv.start, iv.end);
    if (!rect) continue;
    out.push({
      key: "ev:" + t.id,
      task: t,
      ...rect,
      title: t.title,
      time: fmtRange(iv.start, iv.end),
      variant: "event",
    });
  }
  for (const t of timeboxed) {
    const iv = getTimebox(t);
    if (!iv || !sameDay(iv.start, day)) continue;
    const rect = blockRect(iv.start, iv.end);
    if (!rect) continue;
    out.push({
      key: "tb:" + t.id,
      task: t,
      ...rect,
      title: t.title,
      time: fmtRange(iv.start, iv.end),
      variant: "timebox",
    });
  }
  return out;
}

export interface DayColumnProps {
  day: Date;
  isToday: boolean;
  now: Date;
  timedEvents: Task[];
  timeboxed: Task[];
  onOpen: (id: string) => void;
  onClearTimebox: (task: Task) => void;
  /** Reads a dragged core task's id off the drop event (api.dnd.readTaskId). */
  readTaskId: (dt: DataTransfer) => string | null;
  /** Called with the dropped task id and the cursor's y within the column. */
  onDrop: (id: string, offsetY: number) => void;
}

export function DayColumn({
  day,
  isToday,
  now,
  timedEvents,
  timeboxed,
  onOpen,
  onClearTimebox,
  readTaskId,
  onDrop,
}: DayColumnProps) {
  const [dragOver, setDragOver] = useState(false);
  const nowMin = minutesOfDay(now);
  const showNow = isToday && nowMin >= HOUR_START * 60 && nowMin <= HOUR_END * 60;

  const { placements, lanes } = assignLanes(blocksForDay(day, timedEvents, timeboxed));

  const style: CSSProperties = {
    flex: 1,
    minWidth: 0,
    position: "relative",
    height: GRID_HEIGHT,
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
        onDrop(id, e.clientY - rect.top);
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
            top: (h - HOUR_START) * 60 * PX_PER_MIN,
            borderTop: "1px solid var(--line)",
            opacity: 0.6,
          }}
        />
      ))}

      {/* Now line (today only) */}
      {showNow && (
        <div
          style={{
            position: "absolute",
            left: 0,
            right: 0,
            top: (nowMin - HOUR_START * 60) * PX_PER_MIN,
            borderTop: "1.5px solid var(--warn)",
            zIndex: 4,
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
      {placements.map(({ block, lane }) => {
        const width = 100 / lanes;
        return (
          <EventBlock
            key={block.key}
            top={block.top}
            height={block.height}
            left={`calc(${lane * width}% + 1px)`}
            width={`calc(${width}% - 2px)`}
            title={block.title}
            time={block.time}
            variant={block.variant}
            onClick={() => onOpen(block.task.id)}
            onRemove={block.variant === "timebox" ? () => onClearTimebox(block.task) : undefined}
          />
        );
      })}
    </div>
  );
}
