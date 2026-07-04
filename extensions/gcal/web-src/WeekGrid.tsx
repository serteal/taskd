// The week grid: a sticky day-header row, an all-day strip, and a scrolling
// body of hour rows with seven day columns. Calendar events render as filled
// blocks; user timeboxes as outlined blocks; today's column is tinted and
// carries a "now" line. Day columns are HTML5 drop targets for timeboxing.

import { useState } from "react";
import type { CSSProperties } from "react";
import type { Task } from "@taskd/extension-api";
import { EventBlock } from "./EventBlock";
import {
  GRID_HEIGHT,
  GUTTER_W,
  HEADER_H,
  HOUR_END,
  HOUR_START,
  MONO,
  PX_PER_MIN,
  assignLanes,
  blockRect,
  eventInterval,
  fmtRange,
  getTimebox,
  isAllDay,
  isGcal,
  minutesOfDay,
  sameDay,
} from "./util";

export interface WeekGridProps {
  days: Date[];
  now: Date;
  tasks: Task[];
  onOpen: (id: string) => void;
  onDrop: (id: string, day: Date, offsetY: number) => void;
  onClearTimebox: (task: Task) => void;
}

interface DayBlock {
  key: string;
  task: Task;
  top: number;
  height: number;
  title: string;
  time: string;
  variant: "event" | "timebox";
}

const hours = Array.from({ length: HOUR_END - HOUR_START + 1 }, (_, i) => HOUR_START + i);

export function WeekGrid({ days, now, tasks, onOpen, onDrop, onClearTimebox }: WeekGridProps) {
  // Partition once: timed calendar events, all-day calendar events, timeboxes.
  const timedEvents = tasks.filter((t) => isGcal(t) && !isAllDay(t));
  const allDayEvents = tasks.filter((t) => isGcal(t) && isAllDay(t));
  const timeboxed = tasks.filter((t) => getTimebox(t) !== undefined);

  const blocksForDay = (day: Date): DayBlock[] => {
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
  };

  return (
    <div style={{ flex: 1, minWidth: 0, overflowY: "auto", overflowX: "hidden", background: "var(--surface)" }}>
      {/* Day headers (sticky) */}
      <div style={{ display: "flex", position: "sticky", top: 0, zIndex: 3, background: "var(--surface)" }}>
        <div style={{ width: GUTTER_W, flexShrink: 0, borderBottom: "1px solid var(--line)" }} />
        {days.map((day) => {
          const today = sameDay(day, now);
          return (
            <div
              key={+day}
              style={{
                flex: 1,
                minWidth: 0,
                height: HEADER_H,
                boxSizing: "border-box",
                padding: "6px 8px",
                borderLeft: "1px solid var(--line)",
                borderBottom: "1px solid var(--line)",
                background: today ? "color-mix(in srgb, var(--accent) 8%, transparent)" : "transparent",
              }}
            >
              <div
                style={{
                  fontSize: 11,
                  textTransform: "uppercase",
                  letterSpacing: "0.08em",
                  color: today ? "var(--accent)" : "var(--muted)",
                }}
              >
                {day.toLocaleDateString(undefined, { weekday: "short" })}
              </div>
              <div
                style={{
                  fontFamily: MONO,
                  fontSize: 13,
                  fontWeight: today ? 600 : 400,
                  color: today ? "var(--accent)" : "var(--ink)",
                }}
              >
                {day.getDate()}
              </div>
            </div>
          );
        })}
      </div>

      {/* All-day strip (sticky, below headers) */}
      <div style={{ display: "flex", position: "sticky", top: HEADER_H, zIndex: 2, background: "var(--surface)" }}>
        <div
          style={{
            width: GUTTER_W,
            flexShrink: 0,
            borderBottom: "1px solid var(--line)",
            padding: "3px 4px",
            fontFamily: MONO,
            fontSize: 9,
            textTransform: "uppercase",
            letterSpacing: "0.06em",
            color: "var(--faint)",
            textAlign: "right",
          }}
        >
          all-day
        </div>
        {days.map((day) => {
          const chips = allDayEvents.filter((t) => {
            const iv = eventInterval(t);
            return iv && sameDay(iv.start, day);
          });
          const today = sameDay(day, now);
          return (
            <div
              key={+day}
              style={{
                flex: 1,
                minWidth: 0,
                minHeight: 22,
                boxSizing: "border-box",
                display: "flex",
                flexWrap: "wrap",
                gap: 3,
                padding: 3,
                borderLeft: "1px solid var(--line)",
                borderBottom: "1px solid var(--line)",
                background: today ? "color-mix(in srgb, var(--accent) 6%, transparent)" : "transparent",
              }}
            >
              {chips.map((t) => (
                <button
                  key={t.id}
                  onClick={() => onOpen(t.id)}
                  title={t.title}
                  style={{
                    maxWidth: "100%",
                    textAlign: "left",
                    border: "1px solid color-mix(in srgb, var(--accent) 42%, transparent)",
                    background: "color-mix(in srgb, var(--accent) 16%, var(--surface))",
                    color: "var(--ink)",
                    borderRadius: 4,
                    padding: "1px 6px",
                    fontSize: 11,
                    cursor: "pointer",
                    whiteSpace: "nowrap",
                    overflow: "hidden",
                    textOverflow: "ellipsis",
                  }}
                >
                  {t.title}
                </button>
              ))}
            </div>
          );
        })}
      </div>

      {/* Hour body */}
      <div style={{ display: "flex" }}>
        {/* Hour-label gutter */}
        <div style={{ width: GUTTER_W, flexShrink: 0, position: "relative", height: GRID_HEIGHT }}>
          {hours.map((h) => (
            <div
              key={h}
              style={{
                position: "absolute",
                top: (h - HOUR_START) * 60 * PX_PER_MIN,
                right: 6,
                transform: "translateY(-6px)",
                fontFamily: MONO,
                fontSize: 10,
                color: "var(--faint)",
              }}
            >
              {h}:00
            </div>
          ))}
        </div>

        {days.map((day) => (
          <DayColumn
            key={+day}
            day={day}
            now={now}
            blocks={blocksForDay(day)}
            onOpen={onOpen}
            onDrop={onDrop}
            onClearTimebox={onClearTimebox}
          />
        ))}
      </div>
    </div>
  );
}

interface DayColumnProps {
  day: Date;
  now: Date;
  blocks: DayBlock[];
  onOpen: (id: string) => void;
  onDrop: (id: string, day: Date, offsetY: number) => void;
  onClearTimebox: (task: Task) => void;
}

function DayColumn({ day, now, blocks, onOpen, onDrop, onClearTimebox }: DayColumnProps) {
  const [dragOver, setDragOver] = useState(false);
  const today = sameDay(day, now);
  const nowMin = minutesOfDay(now);
  const showNow = today && nowMin >= HOUR_START * 60 && nowMin <= HOUR_END * 60;

  const { placements, lanes } = assignLanes(blocks);

  const style: CSSProperties = {
    flex: 1,
    minWidth: 0,
    position: "relative",
    height: GRID_HEIGHT,
    borderLeft: "1px solid var(--line)",
    background: dragOver
      ? "color-mix(in srgb, var(--accent) 12%, transparent)"
      : today
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
        const id = e.dataTransfer.getData("text/plain");
        if (!id) return;
        const rect = e.currentTarget.getBoundingClientRect();
        onDrop(id, day, e.clientY - rect.top);
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

      {/* Now line */}
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

      {/* Blocks */}
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
