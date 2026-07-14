// gcal web half: the calendar-event presenter plus a persistent day-rail
// panel. The panel docks on the right of the app and shows a single-day
// timeline, stepped with prev/next arrows. A task dragged onto it from
// anywhere (core rows are drag sources) gets a user_data.timebox on the shown
// day, at the dropped time; placed timeboxes can then be moved, resized,
// keyboard-nudged, or cleared. Default-exports the TaskdExtension the host
// imports.

import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { CSSProperties } from "react";
import type { ExtensionAPI, Task, TaskdExtension } from "@taskd/extension-api";
import { makeCalendarPresenter } from "./presenter";
import { DayColumn } from "./DayColumn";
import {
  DEFAULT_BOX_MIN,
  DEFAULT_PX_PER_MIN,
  GUTTER_W,
  HOUR_END,
  HOUR_START,
  MAX_PX_PER_MIN,
  MIN_PX_PER_MIN,
  MONO,
  addDays,
  atMinute,
  eventInterval,
  getTimebox,
  gridHeight,
  isAllDay,
  isGcal,
  loadZoom,
  minuteToY,
  minutesOfDay,
  sameDay,
  saveZoom,
  startOfDay,
  zoomIn,
  zoomOut,
  type Interval,
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

  // The visible day is anchored at "today + offset"; deriving from an offset
  // (rather than a pinned Date) keeps it correct across a midnight tick.
  const [offset, setOffset] = useState(0);
  const [zoom, setZoom] = useState<number>(loadZoom);

  useEffect(() => saveZoom(zoom), [zoom]);

  const today = startOfDay(now);
  const anchor = addDays(today, offset);
  // A one-element array: the all-day strip and hour body render per-day, and
  // keeping that shape means DayColumn and the strip stay day-count-agnostic.
  const days = [anchor];
  const isTodayInView = sameDay(anchor, today);

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
  const merged = useMemo(() => {
    const events = new Map<string, Task>();
    for (const t of fetched) events.set(t.id, t);
    for (const t of tasks) if (isGcal(t)) events.set(t.id, t);
    return [...events.values()];
  }, [fetched, tasks]);
  const timedEvents = merged.filter((t) => !isAllDay(t));
  const allDayEvents = merged.filter(isAllDay);
  const allDayForDay = (day: Date) =>
    allDayEvents.filter((t) => {
      const iv = eventInterval(t);
      return iv !== undefined && sameDay(iv.start, day);
    });
  // Timeboxes come from the live replica (only active tasks can be timeboxed).
  const timeboxed = tasks.filter((t) => !isGcal(t) && getTimebox(t) !== undefined);

  const openTask = (id: string) => api.ui.openTask(id);

  // Write user_data.timebox (merged, never clobbering other keys). No
  // expectedRevision: the timebox is a single-writer, user-owned field (sync
  // never touches user_data), so optimistic concurrency buys nothing here —
  // and rapid keyboard nudges would otherwise race the watch echo and 409.
  // Last-write-wins is correct; we re-read the freshest task so the merge
  // keeps any other user_data keys current.
  const writeTimebox = (task: Task, iv: Interval) => {
    const current = api.getTasks().find((t) => t.id === task.id) ?? task;
    const userData: Record<string, unknown> = {
      ...(current.userData ?? {}),
      timebox: { start: iv.start.toISOString(), end: iv.end.toISOString() },
    };
    api.store.update(task.id, { userData }).catch(() => {});
  };

  // Drop → create a default-length timebox on `day` at the snapped start.
  const createTimebox = (id: string, day: Date, startMin: number) => {
    const task = tasks.find((t) => t.id === id);
    if (!task) return;
    const start = atMinute(day, startMin);
    const end = new Date(start.getTime() + DEFAULT_BOX_MIN * 60_000);
    writeTimebox(task, { start, end });
  };

  // Clear a timebox: spread user_data and drop the timebox key (may leave {}).
  // Freshest-read + last-write-wins, same rationale as writeTimebox.
  const clearTimebox = (task: Task) => {
    const current = api.getTasks().find((t) => t.id === task.id) ?? task;
    const userData: Record<string, unknown> = { ...(current.userData ?? {}) };
    delete userData.timebox;
    api.store.update(task.id, { userData }).catch(() => {});
  };

  // ⌘/ctrl + wheel over the timeline zooms. Bound natively so it can be
  // non-passive (preventDefault the page zoom / scroll).
  const scrollRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const onWheel = (e: WheelEvent) => {
      if (!(e.ctrlKey || e.metaKey)) return;
      e.preventDefault();
      setZoom((z) => (e.deltaY < 0 ? zoomIn(z) : zoomOut(z)));
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  // The full-day grid overflows the panel, so choose a sensible initial scroll:
  // when today is in view, park "now" ~a third down the viewport; otherwise put
  // 08:00 at the top. This fires on mount and whenever the user jumps back to
  // today (scrollSignal bumps) — deliberately NOT on prev/next navigation (the
  // container is not remounted, so the browser preserves scrollTop) nor on zoom
  // (which keeps its own browser-preserved anchor). `now`/`zoom`/`isTodayInView`
  // are read fresh from the closure but intentionally omitted from the deps so a
  // minute tick or a zoom step never yanks the view.
  const [scrollSignal, setScrollSignal] = useState(0);
  const jumpToToday = () => {
    setOffset(0);
    setScrollSignal((s) => s + 1);
  };
  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const y = isTodayInView
      ? minuteToY(minutesOfDay(now), zoom) - el.clientHeight / 3
      : minuteToY(8 * 60, zoom);
    el.scrollTop = Math.max(0, y);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [scrollSignal]);

  const height = gridHeight(zoom);
  const stickyLeft: CSSProperties = {
    width: GUTTER_W,
    flexShrink: 0,
    position: "sticky",
    left: 0,
    zIndex: 4,
    background: "var(--surface)",
  };

  return (
    <div
      data-testid="calendar-rail"
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
      {/* Toolbar row: zoom */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          justifyContent: "flex-end",
          gap: 6,
          padding: "6px 10px",
          borderBottom: "1px solid var(--line)",
          flexShrink: 0,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 2 }} role="group" aria-label="Zoom">
          <ZoomButton label="−" title="Zoom out" onClick={() => setZoom(zoomOut)} disabled={zoom <= MIN_PX_PER_MIN + 1e-6} />
          <span
            style={{ fontFamily: MONO, fontSize: 10, color: "var(--faint)", minWidth: 30, textAlign: "center" }}
            title="Zoom level"
          >
            {Math.round((zoom / DEFAULT_PX_PER_MIN) * 100)}%
          </span>
          <ZoomButton label="+" title="Zoom in" onClick={() => setZoom(zoomIn)} disabled={zoom >= MAX_PX_PER_MIN - 1e-6} />
        </div>
      </div>

      {/* Navigation row: prev / title / today / next */}
      <div
        style={{
          display: "flex",
          alignItems: "center",
          gap: 6,
          padding: "6px 10px",
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
              color: isTodayInView ? "var(--accent)" : "var(--ink)",
            }}
          >
            {anchor.toLocaleDateString(undefined, { weekday: "long" })}
          </div>
          <div style={{ fontFamily: MONO, fontSize: 11, color: "var(--muted)" }}>
            {anchor.toLocaleDateString(undefined, { month: "short", day: "numeric" })}
          </div>
        </div>
        {!isTodayInView && (
          <button
            onClick={jumpToToday}
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
      <div
        ref={scrollRef}
        data-testid="cal-scroll"
        style={{
          flex: 1,
          minHeight: 0,
          overflowY: "auto",
          overflowX: "hidden",
          background: "var(--surface)",
        }}
      >
        <div style={{ width: "100%" }}>
          {/* Sticky header block: the all-day strip */}
          <div style={{ position: "sticky", top: 0, zIndex: 5, background: "var(--surface)" }}>
            <div style={{ display: "flex" }}>
              <div
                style={{
                  ...stickyLeft,
                  borderBottom: "1px solid var(--line)",
                  padding: "3px 4px",
                  boxSizing: "border-box",
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
              {days.map((d) => (
                <div
                  key={+d}
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
                    background: sameDay(d, today) ? "color-mix(in srgb, var(--accent) 6%, transparent)" : "transparent",
                  }}
                >
                  {allDayForDay(d).map((t) => (
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
              ))}
            </div>
          </div>

          {/* Hour body: label gutter + one drop-target column per day */}
          <div style={{ display: "flex" }}>
            <div style={{ ...stickyLeft, position: "sticky", height }}>
              {hours.map((h) => (
                <div
                  key={h}
                  style={{
                    position: "absolute",
                    top: minuteToY(h * 60, zoom),
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
            {days.map((d) => (
              <DayColumn
                key={+d}
                day={d}
                isToday={sameDay(d, today)}
                now={now}
                pxPerMin={zoom}
                timedEvents={timedEvents}
                timeboxed={timeboxed}
                onOpen={openTask}
                onSetTimebox={writeTimebox}
                onClearTimebox={clearTimebox}
                onCreateTimebox={createTimebox}
                readTaskId={api.dnd.readTaskId}
              />
            ))}
          </div>
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
        Drag a task here to timebox it · drag or ↑↓ to move · ⌘-scroll to zoom
      </div>
    </div>
  );
}

function NavButton({ label, title, onClick }: { label: string; title: string; onClick: () => void }) {
  return (
    <button
      onClick={onClick}
      title={title}
      aria-label={title}
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

function ZoomButton({
  label,
  title,
  onClick,
  disabled,
}: {
  label: string;
  title: string;
  onClick: () => void;
  disabled?: boolean;
}) {
  return (
    <button
      onClick={onClick}
      title={title}
      aria-label={title}
      disabled={disabled}
      style={{
        flexShrink: 0,
        width: 20,
        height: 20,
        display: "flex",
        alignItems: "center",
        justifyContent: "center",
        border: "1px solid var(--line)",
        borderRadius: 5,
        background: "var(--surface)",
        color: disabled ? "var(--faint)" : "var(--ink)",
        fontSize: 13,
        lineHeight: 1,
        cursor: disabled ? "default" : "pointer",
        opacity: disabled ? 0.5 : 1,
      }}
    >
      {label}
    </button>
  );
}

const extension: TaskdExtension = {
  name: "gcal",
  register(api) {
    api.registerPresenter(makeCalendarPresenter(api));
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
      icon: api.icon("calendar"),
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

    // @-mentions: typing "@" in a title field can tag a calendar event. The
    // ref is the event's task id, so the chip opens the event's detail (with
    // this extension's presenter section).
    api.registerMentionProvider({
      id: "gcal",
      title: "Calendar events",
      search: (query) => {
        const q = query.trim().toLowerCase();
        const now = Date.now();
        return api
          .getTasks()
          .filter((t) => isGcal(t) && (q === "" || t.title.toLowerCase().includes(q)))
          .map((t) => ({ t, iv: eventInterval(t) }))
          // Soonest upcoming first, then the most recent past events.
          .sort((a, b) => {
            const at = a.iv?.start.getTime() ?? Infinity;
            const bt = b.iv?.start.getTime() ?? Infinity;
            const aUp = at >= now;
            const bUp = bt >= now;
            if (aUp !== bUp) return aUp ? -1 : 1;
            return aUp ? at - bt : bt - at;
          })
          .slice(0, 6)
          .map(({ t, iv }) => ({
            title: t.title,
            ref: `task:${t.id}`,
            hint: iv
              ? iv.start.toLocaleDateString(undefined, { month: "short", day: "numeric" })
              : undefined,
          }));
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
