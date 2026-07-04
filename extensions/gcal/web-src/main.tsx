// gcal web half: the calendar-event presenter plus a persistent day-rail
// panel. The panel docks on the right of the app and shows one day's timeline;
// a task dragged onto it from anywhere (core rows are drag sources) gets a
// user_data.timebox on the day, at the dropped time. Default-exports the
// TaskdExtension the host imports.

import { useEffect, useState } from "react";
import type { ExtensionAPI, Task, TaskdExtension } from "@taskd/extension-api";
import { calendarPresenter } from "./presenter";
import { DayColumn } from "./DayColumn";
import {
  DEFAULT_BOX_MIN,
  GRID_HEIGHT,
  GUTTER_W,
  HOUR_END,
  HOUR_START,
  MONO,
  PX_PER_MIN,
  dropMinutes,
  eventInterval,
  getTimebox,
  isAllDay,
  isGcal,
  sameDay,
} from "./util";

const SANS = '"IBM Plex Sans", ui-sans-serif, system-ui, sans-serif';

// The bit of the generated TaskService client the rail needs. A timeline must
// show every event on the day, including past ones the source has marked
// completed — but the live replica holds only ACTIVE tasks. So events come
// from a direct server read (both completion states), while timeboxes stay
// live from the replica. pageSize 1000 covers many days, so one fetch is
// enough; we just filter to the shown day at render.
type ListClient = {
  listTasks(req: { pageSize?: number }): Promise<{ tasks: Task[] }>;
};

const hours = Array.from({ length: HOUR_END - HOUR_START + 1 }, (_, i) => HOUR_START + i);

function DayRail({ api }: { api: ExtensionAPI }) {
  const tasks = api.hooks.useTasks();
  const now = api.hooks.useNow();

  // The shown day is "today + offset"; deriving from an offset (rather than a
  // pinned Date) keeps it correct across a midnight tick of useNow().
  const [offset, setOffset] = useState(0);
  const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const day = new Date(today);
  day.setDate(today.getDate() + offset);
  const isToday = offset === 0;

  // Calendar events fetched once from the server (includes completed past ones
  // the active replica omits).
  const [fetched, setFetched] = useState<Task[]>([]);
  useEffect(() => {
    let alive = true;
    (api.client as ListClient)
      .listTasks({ pageSize: 1000 })
      .then((res) => {
        if (alive) setFetched(res.tasks.filter(isGcal));
      })
      .catch(() => {});
    return () => {
      alive = false;
    };
  }, [api]);

  // Merge fetched events with any live (active) ones from the replica, live
  // winning on id so a freshly-synced event reflects immediately.
  const events = new Map<string, Task>();
  for (const t of fetched) events.set(t.id, t);
  for (const t of tasks) if (isGcal(t)) events.set(t.id, t);
  const merged = [...events.values()];
  const timedEvents = merged.filter((t) => !isAllDay(t));
  const allDayEvents = merged.filter((t) => {
    if (!isAllDay(t)) return false;
    const iv = eventInterval(t);
    return iv !== undefined && sameDay(iv.start, day);
  });
  // Timeboxes come from the live replica (only active tasks can be timeboxed).
  const timeboxed = tasks.filter((t) => !isGcal(t) && getTimebox(t) !== undefined);

  const openTask = (id: string) => api.ui.openTask(id);

  // Drop → write user_data.timebox on the SHOWN day (merged, never clobbering
  // other keys). `id` comes from api.dnd.readTaskId; only tasks in the active
  // replica can be timeboxed, which is every draggable core row.
  const dropTimebox = (id: string, offsetY: number) => {
    const task = tasks.find((t) => t.id === id);
    if (!task) return;
    const start = new Date(day.getFullYear(), day.getMonth(), day.getDate(), 0, dropMinutes(offsetY));
    const end = new Date(start.getTime() + DEFAULT_BOX_MIN * 60_000);
    const userData: Record<string, unknown> = {
      ...(task.userData ?? {}),
      timebox: { start: start.toISOString(), end: end.toISOString() },
    };
    api.store.update(task.id, { userData, expectedRevision: task.revision }).catch(() => {});
  };

  // Clear a timebox: spread user_data and drop the timebox key (may leave {}).
  const clearTimebox = (task: Task) => {
    const userData: Record<string, unknown> = { ...(task.userData ?? {}) };
    delete userData.timebox;
    api.store.update(task.id, { userData, expectedRevision: task.revision }).catch(() => {});
  };

  return (
    <div
      style={{
        display: "flex",
        flexDirection: "column",
        height: "100%",
        minHeight: 0,
        background: "var(--bg)",
        borderLeft: "1px solid var(--line)",
        fontFamily: SANS,
      }}
    >
      {/* Day navigation header */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "8px 10px",
          borderBottom: "1px solid var(--line)",
          flexShrink: 0,
        }}
      >
        <NavButton label="‹" title="Previous day" onClick={() => setOffset((o) => o - 1)} />
        <div style={{ flex: 1, minWidth: 0, textAlign: "center" }}>
          <div
            style={{
              fontSize: 13,
              fontWeight: 600,
              lineHeight: 1.2,
              color: isToday ? "var(--accent)" : "var(--ink)",
            }}
          >
            {day.toLocaleDateString(undefined, { weekday: "long" })}
          </div>
          <div style={{ fontFamily: MONO, fontSize: 11, color: "var(--muted)" }}>
            {day.toLocaleDateString(undefined, { month: "short", day: "numeric" })}
          </div>
        </div>
        {!isToday && (
          <button
            onClick={() => setOffset(0)}
            title="Back to today"
            style={{
              flexShrink: 0,
              border: "1px solid var(--line)",
              borderRadius: 5,
              background: "var(--surface)",
              color: "var(--muted)",
              fontFamily: MONO,
              fontSize: 10,
              padding: "2px 6px",
              cursor: "pointer",
            }}
          >
            today
          </button>
        )}
        <NavButton label="›" title="Next day" onClick={() => setOffset((o) => o + 1)} />
      </div>

      {/* Scrolling timeline: all-day strip + hour body */}
      <div style={{ flex: 1, minHeight: 0, overflowY: "auto", overflowX: "hidden", background: "var(--surface)" }}>
        {/* All-day strip (sticky) */}
        <div style={{ display: "flex", position: "sticky", top: 0, zIndex: 2, background: "var(--surface)" }}>
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
          <div
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
              background: isToday ? "color-mix(in srgb, var(--accent) 6%, transparent)" : "transparent",
            }}
          >
            {allDayEvents.map((t) => (
              <button
                key={t.id}
                onClick={() => openTask(t.id)}
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
        </div>

        {/* Hour body: label gutter + the day column (drop target) */}
        <div style={{ display: "flex" }}>
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
          <DayColumn
            day={day}
            isToday={isToday}
            now={now}
            timedEvents={timedEvents}
            timeboxed={timeboxed}
            onOpen={openTask}
            onClearTimebox={clearTimebox}
            readTaskId={api.dnd.readTaskId}
            onDrop={dropTimebox}
          />
        </div>
      </div>

      {/* Discoverability hint */}
      <div
        style={{
          flexShrink: 0,
          padding: "5px 10px",
          borderTop: "1px solid var(--line)",
          fontSize: 10,
          color: "var(--faint)",
        }}
      >
        Drag a task here to timebox it
      </div>
    </div>
  );
}

function NavButton({ label, title, onClick }: { label: string; title: string; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      title={title}
      style={{
        flexShrink: 0,
        width: 22,
        height: 22,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        border: "1px solid var(--line)",
        borderRadius: 5,
        background: "var(--surface)",
        color: "var(--ink)",
        fontSize: 14,
        lineHeight: 1,
        cursor: "pointer",
      }}
    >
      {label}
    </button>
  );
}

const extension: TaskdExtension = {
  name: "gcal",
  register(api) {
    api.registerPresenter(calendarPresenter);
    api.registerPanel({
      id: "day",
      title: "Today",
      side: "right",
      width: 300,
      defaultOpen: true,
      Component: DayRail,
    });

    // A command in ⌘K: how many calendar events are on today.
    api.registerCommand({
      id: "today-events",
      title: "Calendar: today's events",
      group: "Calendar",
      icon: "📅",
      run: () => {
        const today = new Date();
        const n = api.getTasks().filter((t) => {
          if (!isGcal(t)) return false;
          const iv = eventInterval(t);
          return iv !== undefined && sameDay(iv.start, today);
        }).length;
        api.notify.toast({ message: `${n} event${n === 1 ? "" : "s"} on your calendar today` });
      },
    });

    // A quick-add token: typing "noon" schedules the task for today at 12:00.
    api.registerQuickAddToken({
      hint: "noon",
      match: (tok) => {
        if (tok.toLowerCase() !== "noon") return null;
        const d = new Date();
        d.setHours(12, 0, 0, 0);
        return { due: d };
      },
    });
  },
};

export default extension;
