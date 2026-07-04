// gcal web half: the calendar-event presenter plus a Calendar week view where
// events are laid out on an hour grid and unscheduled todos can be dragged in
// to timebox them. Default-exports the TaskdExtension the host imports.

import { useEffect, useState } from "react";
import type { ExtensionAPI, Task, TaskdExtension } from "@taskd/extension-api";
import { calendarPresenter } from "./presenter";
import { WeekGrid } from "./WeekGrid";
import { UnscheduledRail } from "./UnscheduledRail";
import {
  DEFAULT_BOX_MIN,
  MONO,
  dropMinutes,
  getTimebox,
  isGcal,
  weekDays,
} from "./util";

// The bit of the generated TaskService client the calendar needs. A calendar
// must show every event in the visible week, including past ones the source
// has marked completed — but the live replica holds only ACTIVE tasks. So
// events come from a direct server read (both completion states), while
// timeboxes and the unscheduled rail stay live from the replica.
type ListClient = {
  listTasks(req: { pageSize?: number }): Promise<{ tasks: Task[] }>;
};

function CalendarView({ api }: { api: ExtensionAPI }) {
  const tasks = api.hooks.useTasks();
  const now = api.hooks.useNow();
  const days = weekDays(now);

  // Calendar events fetched from the server (includes completed past events).
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
  const timeboxed = tasks.filter((t) => !isGcal(t) && getTimebox(t) !== undefined);
  const gridTasks = [...events.values(), ...timeboxed];

  // Rail = active todos that are neither calendar events nor timeboxed.
  const unscheduled = tasks.filter((t) => !isGcal(t) && getTimebox(t) === undefined);

  const openTask = (id: string) => api.ui.openTask(id);

  // Drop → write user_data.timebox (merged, never clobbering other keys).
  const dropTimebox = (id: string, day: Date, offsetY: number) => {
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

  // Clear a timebox: spread user_data and drop the timebox key (may be {}).
  const clearTimebox = (task: Task) => {
    const userData: Record<string, unknown> = { ...(task.userData ?? {}) };
    delete userData.timebox;
    api.store.update(task.id, { userData, expectedRevision: task.revision }).catch(() => {});
  };

  const range = `${days[0].toLocaleDateString(undefined, { month: "short", day: "numeric" })} – ${days[6].toLocaleDateString(undefined, { month: "short", day: "numeric" })}`;

  return (
    <div style={{ display: "flex", flexDirection: "column", height: "100%", minHeight: 0, background: "var(--bg)" }}>
      <div
        style={{
          display: "flex",
          alignItems: "baseline",
          gap: 10,
          padding: "10px 12px",
          borderBottom: "1px solid var(--line)",
        }}
      >
        <span style={{ fontSize: 15, fontWeight: 600, color: "var(--ink)" }}>This week</span>
        <span style={{ fontFamily: MONO, fontSize: 12, color: "var(--muted)" }}>{range}</span>
        <span style={{ marginLeft: "auto", fontSize: 11, color: "var(--faint)" }}>
          drag a todo onto the grid to timebox it
        </span>
      </div>
      <div style={{ display: "flex", flex: 1, minHeight: 0 }}>
        <WeekGrid
          days={days}
          now={now}
          tasks={gridTasks}
          onOpen={openTask}
          onDrop={dropTimebox}
          onClearTimebox={clearTimebox}
        />
        <UnscheduledRail tasks={unscheduled} onOpen={openTask} tsDate={api.format.tsDate} />
      </div>
    </div>
  );
}

const extension: TaskdExtension = {
  name: "gcal",
  register(api) {
    api.registerPresenter(calendarPresenter);
    api.registerView({ id: "calendar", title: "Calendar", Component: CalendarView });
  },
};

export default extension;
