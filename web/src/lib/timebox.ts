import type { Task } from "../gen/task/task_pb";

// Core parse + format helpers for the user-owned timebox
// (user_data.timebox = { start, end } in RFC3339). Kept in core — independent
// of the gcal extension — so the task list can render a planned-time indicator
// and the "Plan today" action can write one without importing an extension.

export interface Timebox {
  start: Date;
  end: Date;
}

function parseISO(v: unknown): Date | undefined {
  if (typeof v !== "string") return undefined;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? undefined : d;
}

/** Reads user_data.timebox = { start, end }; undefined when absent/invalid. */
export function getTimebox(t: Task): Timebox | undefined {
  const ud = t.userData as Record<string, unknown> | undefined;
  const tb = ud?.timebox as Record<string, unknown> | undefined;
  if (!tb || typeof tb !== "object") return undefined;
  const start = parseISO(tb.start);
  const end = parseISO(tb.end);
  return start && end ? { start, end } : undefined;
}

const pad2 = (n: number) => String(n).padStart(2, "0");

/** Clock time, e.g. "14:00" (local). */
export function fmtClock(d: Date): string {
  return `${d.getHours()}:${pad2(d.getMinutes())}`;
}

/** Range, e.g. "14:00–15:00" (en dash). */
export function fmtTimeboxRange(tb: Timebox): string {
  return `${fmtClock(tb.start)}–${fmtClock(tb.end)}`;
}

/** The next round half-hour at/after `now` (:00 or :30); boundaries stay put. */
export function nextHalfHour(now: Date): Date {
  const d = new Date(now);
  d.setSeconds(0, 0);
  const m = d.getMinutes();
  if (m === 0 || m === 30) return d;
  d.setMinutes(m < 30 ? 30 : 60); // 60 rolls into the next hour at :00
  return d;
}

/** A default "plan today" interval: next half-hour → +60 min. */
export function planTodayInterval(now: Date): Timebox {
  const start = nextHalfHour(now);
  return { start, end: new Date(start.getTime() + 60 * 60_000) };
}
