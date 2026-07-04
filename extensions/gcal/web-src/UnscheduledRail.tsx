// The rail of draggable, unscheduled todos beside the grid: active tasks that
// are neither calendar events nor already timeboxed. Dragging one onto a day
// column (see WeekGrid) timeboxes it. Each item shows its title and any due
// date.

import type { Task } from "@taskd/extension-api";
import { MONO } from "./util";

export interface UnscheduledRailProps {
  tasks: Task[];
  onOpen: (id: string) => void;
  tsDate: (ts?: { seconds: bigint; nanos: number }) => Date | undefined;
}

export function UnscheduledRail({ tasks, onOpen, tsDate }: UnscheduledRailProps) {
  return (
    <div
      style={{
        width: 220,
        flexShrink: 0,
        display: "flex",
        flexDirection: "column",
        minHeight: 0,
        borderLeft: "1px solid var(--line)",
        background: "var(--bg)",
      }}
    >
      <div
        style={{
          padding: "8px 10px",
          borderBottom: "1px solid var(--line)",
          fontFamily: MONO,
          fontSize: 10,
          textTransform: "uppercase",
          letterSpacing: "0.12em",
          color: "var(--faint)",
        }}
      >
        Unscheduled · {tasks.length}
      </div>
      <div style={{ flex: 1, overflowY: "auto", padding: 8, display: "flex", flexDirection: "column", gap: 6 }}>
        {tasks.length === 0 && (
          <div style={{ fontSize: 12, color: "var(--faint)", padding: "4px 2px", lineHeight: 1.4 }}>
            Nothing to schedule. Todos with no timebox appear here — drag them onto the grid.
          </div>
        )}
        {tasks.map((t) => {
          const due = tsDate(t.dueTime);
          return (
            <div
              key={t.id}
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData("text/plain", t.id);
                e.dataTransfer.effectAllowed = "move";
              }}
              onClick={() => onOpen(t.id)}
              title="Drag onto the grid to timebox"
              style={{
                border: "1px solid var(--line)",
                borderRadius: 6,
                background: "var(--surface)",
                padding: "6px 8px",
                cursor: "grab",
              }}
            >
              <div style={{ fontSize: 12, color: "var(--ink)", lineHeight: 1.3 }}>{t.title}</div>
              {due && (
                <div style={{ fontFamily: MONO, fontSize: 10, color: "var(--muted)", marginTop: 2 }}>
                  due {due.toLocaleDateString(undefined, { month: "short", day: "numeric" })}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}
