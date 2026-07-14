import { useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { useSnapshot, useStore } from "../lib/hooks";
import { dayDiff, startOfDay, tsDate, isEndOfDay } from "../lib/format";
import { registry } from "../lib/extensions";
import { subtaskCounts } from "../lib/nest";
import type { PromotedSection } from "../lib/views";
import { addDays, dayKey, startOfWeek, type UpcomingModel } from "../lib/upcoming";
import { completeTask, rescheduleMany } from "../lib/actions";
import { TaskRow } from "./TaskRow";
import { Popover } from "./Popover";
import { ScheduleMenu } from "./pickers";
import { Icon } from "./icons";

// The Upcoming view: a schedule, not a filter. A week strip up top (‹ › steps
// whole weeks, Today snaps back), then one section per day — synced items
// (calendar events…) as read-only pills, local tasks as ordinary rows, and a
// per-day "+ Add task" that opens the new-task modal prefilled with that day.
// Overdue local tasks lead the current week so planning starts from what
// slipped. App computes the model (lib/upcoming) and derives its keyboard
// order from the same structure, so j/k matches what's on screen.

const WEEKDAY_SHORT = new Intl.DateTimeFormat(undefined, { weekday: "short" });
const WEEKDAY_LONG = new Intl.DateTimeFormat(undefined, { weekday: "long" });
const DAY_MONTH = new Intl.DateTimeFormat(undefined, { day: "numeric", month: "short" });
const MONTH_YEAR = new Intl.DateTimeFormat(undefined, { month: "long", year: "numeric" });

const p2 = (n: number) => String(n).padStart(2, "0");
const clock = (d: Date) => `${p2(d.getHours())}:${p2(d.getMinutes())}`;

function dayHeading(date: Date, now: Date): string {
  const diff = dayDiff(date, now);
  const parts = [DAY_MONTH.format(date)];
  if (diff === 0) parts.push("Today");
  if (diff === 1) parts.push("Tomorrow");
  parts.push(WEEKDAY_LONG.format(date));
  return parts.join(" · ");
}

export function UpcomingView({
  model,
  now,
  weekOffset,
  onWeekOffset,
  selectedId,
  bulkSelected,
  editingId,
  extraSections,
  onSelect,
  onActivate,
  onOpen,
  onStartEdit,
  onRename,
  onEndEdit,
  onContextMenu,
  onAddTask,
}: {
  model: UpcomingModel;
  now: Date;
  /** Whole weeks away from the current week (0 = this week). */
  weekOffset: number;
  onWeekOffset: (n: number) => void;
  selectedId: string | null;
  bulkSelected: Set<string>;
  editingId: string | null;
  /** Promoted filter sections, rendered after the day list. */
  extraSections?: PromotedSection[];
  onSelect: (id: string) => void;
  onActivate: (id: string, mods: { meta: boolean; shift: boolean }) => void;
  /** Open a task's detail (event pills use this — they aren't rows). */
  onOpen: (id: string) => void;
  onStartEdit: (id: string) => void;
  onRename: (id: string, title: string) => void;
  onEndEdit: () => void;
  onContextMenu?: (id: string, x: number, y: number) => void;
  /** Open the new-task modal prefilled with this day. */
  onAddTask: (day: Date) => void;
}) {
  const store = useStore();
  const snap = useSnapshot();
  const [leaving, setLeaving] = useState<Set<string>>(new Set());

  const today = startOfDay(now);
  const weekStart = addDays(startOfWeek(now), weekOffset * 7);
  const strip = Array.from({ length: 7 }, (_, i) => addDays(weekStart, i));
  const counts = subtaskCounts(snap.tasks.values());

  const complete = (t: Task) => {
    if (t.recurrence !== "") {
      completeTask(store, t);
      return;
    }
    setLeaving((s) => new Set(s).add(t.id));
    setTimeout(() => {
      completeTask(store, t);
      setLeaving((s) => {
        const n = new Set(s);
        n.delete(t.id);
        return n;
      });
    }, 250);
  };

  const row = (t: Task) => (
    <TaskRow
      key={t.id}
      task={t}
      now={now}
      selected={t.id === selectedId}
      bulkSelected={bulkSelected.has(t.id)}
      pulsing={snap.pulses.has(t.id)}
      checked={leaving.has(t.id)}
      editing={t.id === editingId}
      subtaskCount={counts.get(t.id) ?? 0}
      onToggle={() => complete(t)}
      onActivate={(mods) => onActivate(t.id, mods)}
      onSelect={() => onSelect(t.id)}
      onStartEdit={() => onStartEdit(t.id)}
      onRename={(title) => onRename(t.id, title)}
      onEndEdit={onEndEdit}
      onContextMenu={onContextMenu ? (x, y) => onContextMenu(t.id, x, y) : undefined}
    />
  );

  const scrollToDay = (d: Date) => {
    document
      .querySelector(`[data-upcoming-day="${dayKey(d)}"]`)
      ?.scrollIntoView({ block: "start", behavior: "smooth" });
  };

  return (
    <div data-testid="upcoming-view">
      {/* Month + week navigation + the 7-day strip. */}
      <div className="sticky top-0 z-20 border-b border-line bg-paper px-3 pb-2 pt-1">
        <div className="flex items-center gap-2">
          <span data-testid="upcoming-month" className="text-[13.5px] font-semibold">
            {MONTH_YEAR.format(weekStart)}
          </span>
          <div className="ml-auto flex items-center gap-1">
            <button
              onClick={() => onWeekOffset(weekOffset - 1)}
              aria-label="Previous week"
              className="rounded border border-line p-1 text-mute hover:border-mute hover:text-ink"
            >
              <Icon name="chevron-right" size={12} className="rotate-180" />
            </button>
            <button
              onClick={() => onWeekOffset(0)}
              disabled={weekOffset === 0}
              className="rounded border border-line px-2 py-0.5 text-[12px] text-mute hover:border-mute hover:text-ink disabled:cursor-default disabled:opacity-40"
            >
              Today
            </button>
            <button
              onClick={() => onWeekOffset(weekOffset + 1)}
              aria-label="Next week"
              className="rounded border border-line p-1 text-mute hover:border-mute hover:text-ink"
            >
              <Icon name="chevron-right" size={12} />
            </button>
          </div>
        </div>
        <div className="mt-1.5 grid grid-cols-7 gap-1">
          {strip.map((d) => {
            const past = d.getTime() < today.getTime();
            const isToday = d.getTime() === today.getTime();
            const listed = model.days.some((x) => x.date.getTime() === d.getTime());
            return (
              <button
                key={dayKey(d)}
                onClick={() => listed && scrollToDay(d)}
                disabled={!listed}
                data-testid="week-strip-day"
                className={`flex items-center justify-center gap-1.5 rounded-md px-1 py-1 text-[12px] ${
                  past && !isToday ? "text-faint" : "text-mute"
                } ${listed ? "hover:bg-ink/[.05]" : "cursor-default"}`}
              >
                {WEEKDAY_SHORT.format(d)}
                <span
                  className={
                    isToday
                      ? "rounded bg-accent px-1 font-mono text-[11.5px] font-semibold text-white"
                      : "font-mono text-[11.5px]"
                  }
                >
                  {d.getDate()}
                </span>
              </button>
            );
          })}
        </div>
      </div>

      {/* Overdue leads the current week — the same set Today carries. */}
      {model.overdue.length > 0 && (
        <section data-testid="upcoming-overdue">
          <h2 className="sticky top-[72px] z-10 flex items-baseline gap-2 border-b border-line bg-paper px-3 pb-1 pt-3 font-mono text-[10.5px] font-medium uppercase tracking-[0.14em] text-warn">
            Overdue
            <span className="text-faint">{model.overdue.length}</span>
            <span className="ml-auto self-center normal-case tracking-normal">
              <Popover
                align="right"
                trigger={({ toggle }) => (
                  <button
                    onClick={toggle}
                    aria-label="Reschedule overdue tasks"
                    className="rounded border border-warn/40 px-1.5 py-px text-[10px] font-medium text-warn hover:bg-warn/10"
                  >
                    Reschedule
                  </button>
                )}
              >
                {(close) => (
                  <ScheduleMenu
                    now={now}
                    onChange={(d) => rescheduleMany(store, model.overdue, d)}
                    close={close}
                  />
                )}
              </Popover>
            </span>
          </h2>
          {model.overdue.map(row)}
        </section>
      )}

      {model.days.map((d) => (
        <section key={dayKey(d.date)} data-upcoming-day={dayKey(d.date)} data-testid="upcoming-day">
          <h2
            className={`sticky top-[72px] z-10 border-b border-line bg-paper px-3 pb-1 pt-3 text-[12px] font-semibold ${
              dayDiff(d.date, now) === 0 ? "text-accent" : "text-ink"
            }`}
          >
            {dayHeading(d.date, now)}
          </h2>

          {/* Synced items: schedule context, not tasks — quiet pills. */}
          {d.events.length > 0 && (
            <div className="space-y-1 px-3 pt-1.5">
              {d.events.map((ev) => {
                const meta = registry.presenterFor(ev)?.rowMeta?.(ev) ?? {};
                const due = tsDate(ev.dueTime);
                const time = meta.timeText ?? (due && !isEndOfDay(due) ? clock(due) : undefined);
                return (
                  <button
                    key={ev.id}
                    data-testid="upcoming-event"
                    onClick={() => onOpen(ev.id)}
                    title={ev.source}
                    className="flex w-full items-center gap-2 rounded-md border border-line bg-surface px-2.5 py-1 text-left text-[12.5px] text-mute hover:border-mute hover:text-ink"
                  >
                    <span className="h-[14px] w-[3px] shrink-0 rounded-full bg-accent/70" aria-hidden />
                    {meta.icon != null && <span className="flex shrink-0 items-center">{meta.icon}</span>}
                    <span className="min-w-0 flex-1 truncate">{ev.title}</span>
                    {time && <span className="shrink-0 font-mono text-[11px] text-faint">{time}</span>}
                  </button>
                );
              })}
            </div>
          )}

          {d.tasks.map(row)}

          <button
            onClick={() => onAddTask(d.date)}
            data-testid="upcoming-add-task"
            className="flex w-full items-center gap-2 px-3 py-[7px] text-[12.5px] text-faint hover:text-accent"
          >
            <Icon name="plus" size={13} />
            Add task
          </button>
        </section>
      ))}

      {/* Promoted filter sections (saved filters with showIn: upcoming). */}
      {(extraSections ?? []).map((s) => (
        <section key={`promoted-${s.filter.id}`} data-testid="promoted-section" data-filter-name={s.filter.name}>
          <h2 className="sticky top-[72px] z-10 flex items-baseline gap-2 border-b border-line bg-paper px-3 pb-1 pt-3 font-mono text-[10.5px] font-medium uppercase tracking-[0.14em] text-mute">
            {s.filter.name}
            <span className="text-faint">{s.tasks.length}</span>
          </h2>
          {s.tasks.map(row)}
        </section>
      ))}
    </div>
  );
}
