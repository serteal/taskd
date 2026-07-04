import { timestampDate } from "@bufbuild/protobuf/wkt";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

export function tsDate(ts?: Timestamp): Date | undefined {
  return ts ? timestampDate(ts) : undefined;
}

/** "due by end of day" — the same rule the CLI uses for date-only input. */
export function endOfDay(d: Date): Date {
  const out = new Date(d);
  out.setHours(23, 59, 59, 0);
  return out;
}

export function startOfDay(d: Date): Date {
  const out = new Date(d);
  out.setHours(0, 0, 0, 0);
  return out;
}

/** Whole days from `now`'s day to `d`'s day (negative = past). */
export function dayDiff(d: Date, now: Date): number {
  return Math.round((startOfDay(d).getTime() - startOfDay(now).getTime()) / 86_400_000);
}

export type DueTone = "overdue" | "today" | "soon" | "later";

const WEEKDAY = new Intl.DateTimeFormat(undefined, { weekday: "short" });
const MONTH_DAY = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });
const FULL_DATE = new Intl.DateTimeFormat(undefined, {
  year: "numeric",
  month: "short",
  day: "numeric",
});

export function humanDue(d: Date, now: Date): { text: string; tone: DueTone } {
  const diff = dayDiff(d, now);
  if (diff < 0) {
    return { text: diff === -1 ? "yesterday" : `${-diff}d ago`, tone: "overdue" };
  }
  if (diff === 0) return { text: "today", tone: "today" };
  if (diff === 1) return { text: "tomorrow", tone: "soon" };
  if (diff < 7) return { text: WEEKDAY.format(d), tone: "soon" };
  const sameYear = d.getFullYear() === now.getFullYear();
  return { text: (sameYear ? MONTH_DAY : FULL_DATE).format(d), tone: "later" };
}

/** Splits a label into its namespace convention parts: "project:home" →
 * {ns: "project", val: "home"}; plain labels have no ns. */
export function chipParts(label: string): { ns?: string; val: string } {
  const i = label.indexOf(":");
  if (i <= 0 || i === label.length - 1) return { val: label };
  return { ns: label.slice(0, i), val: label.slice(i + 1) };
}

/** Mono metadata timestamp, e.g. "2026-07-04 13:10". */
export function fmtStamp(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

/** Value for <input type="datetime-local">. */
export function toLocalInput(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}T${p(d.getHours())}:${p(d.getMinutes())}`;
}

export function shortId(id: string): string {
  return id.slice(0, 8).toLowerCase();
}
