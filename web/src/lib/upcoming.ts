import type { Task } from "../gen/task/task_pb";
import { dayDiff, startOfDay, tsDate } from "./format";

// The Upcoming view's data shape: a rolling window of consecutive days, each
// carrying the LOCAL tasks due that day plus the synced items (calendar
// events, issues) dated that day. Synced items stay read-only "schedule"
// context — they render as event pills, not task rows, so the sources≠tasks
// quarantine holds: they are visible on the calendar surface but never counted
// or completable as tasks. Overdue local tasks get their own leading section
// (same set the Today view carries, repeated here so a week's planning starts
// from what slipped).

export interface UpcomingDay {
  /** Local midnight of the day. */
  date: Date;
  /** Local tasks due this day, soonest time first. */
  tasks: Task[];
  /** Synced items dated this day (event pills), soonest first. */
  events: Task[];
}

export interface UpcomingModel {
  /** Local tasks due before today, oldest first. Empty when `anchor` is not
   *  today's week (browsing the future needs no overdue reminder). */
  overdue: Task[];
  /** `count` consecutive days starting at `anchor`. */
  days: UpcomingDay[];
}

const dueCmp = (a: Task, b: Task): number => {
  const ad = tsDate(a.dueTime)?.getTime() ?? 0;
  const bd = tsDate(b.dueTime)?.getTime() ?? 0;
  if (ad !== bd) return ad - bd;
  return (tsDate(a.createTime)?.getTime() ?? 0) - (tsDate(b.createTime)?.getTime() ?? 0);
};

export function addDays(d: Date, n: number): Date {
  const out = new Date(d);
  out.setDate(out.getDate() + n);
  return out;
}

/** Monday-start week containing `d` (local midnight). */
export function startOfWeek(d: Date): Date {
  const day = startOfDay(d);
  const dow = (day.getDay() + 6) % 7; // Mon=0 … Sun=6
  return addDays(day, -dow);
}

export function dayKey(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

export function upcomingModel(
  tasks: Iterable<Task>,
  now: Date,
  anchor: Date,
  count: number,
  opts: { includeOverdue?: boolean } = {},
): UpcomingModel {
  const overdue: Task[] = [];
  const byDay = new Map<string, { tasks: Task[]; events: Task[] }>();
  const days: UpcomingDay[] = [];
  for (let i = 0; i < count; i++) {
    const date = startOfDay(addDays(anchor, i));
    const bucket = { tasks: [] as Task[], events: [] as Task[] };
    byDay.set(dayKey(date), bucket);
    days.push({ date, ...bucket });
  }

  for (const t of tasks) {
    const due = tsDate(t.dueTime);
    if (due === undefined) continue;
    const local = t.source === "";
    if (local && (opts.includeOverdue ?? true) && dayDiff(due, now) < 0) {
      overdue.push(t);
      continue;
    }
    const bucket = byDay.get(dayKey(due));
    if (!bucket) continue;
    (local ? bucket.tasks : bucket.events).push(t);
  }

  overdue.sort(dueCmp);
  for (const d of days) {
    d.tasks.sort(dueCmp);
    d.events.sort(dueCmp);
  }
  return { overdue, days };
}

/** The keyboard traversal order for the view: overdue rows then each day's
 *  task rows, exactly as rendered. Event pills are click-only (they aren't
 *  completable), so they stay out of the row order. */
export function upcomingFlatTasks(m: UpcomingModel): Task[] {
  return [...m.overdue, ...m.days.flatMap((d) => d.tasks)];
}
