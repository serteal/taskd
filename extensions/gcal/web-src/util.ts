// Shared time math, task classification, and style tokens for the gcal web
// half. Kept framework-free so the components stay declarative.

import type { Task } from "@taskd/extension-api";

// The visible hour band of the week grid and its vertical scale.
export const HOUR_START = 7; // 07:00
export const HOUR_END = 21; //  21:00
export const PX_PER_MIN = 0.9; // 54px per hour
export const DAY_MINUTES = (HOUR_END - HOUR_START) * 60;
export const GRID_HEIGHT = DAY_MINUTES * PX_PER_MIN;
export const GUTTER_W = 54; // hour-label column width, px
export const HEADER_H = 46; // day-header row height, px (sticky offset base)
export const SNAP_MIN = 30; // drop snapping granularity
export const DEFAULT_BOX_MIN = 60; // default timebox duration on drop

// The host fonts, referenced by name per the extension contract (Tailwind's
// --font-* tokens are not part of it).
export const MONO = '"IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, monospace';

export interface Interval {
  start: Date;
  end: Date;
}

export function parseISO(v: unknown): Date | undefined {
  if (typeof v !== "string") return undefined;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? undefined : d;
}

export function isGcal(t: Task): boolean {
  return t.source.startsWith("gcal");
}

export function isAllDay(t: Task): boolean {
  return (t.externalData ?? {}).all_day === true;
}

// A gcal event's [start, end); end falls back to +1h when the feed omits it.
export function eventInterval(t: Task): Interval | undefined {
  const d = t.externalData ?? {};
  const start = parseISO(d.start);
  if (!start) return undefined;
  const end = parseISO(d.end) ?? new Date(start.getTime() + 60 * 60_000);
  return { start, end };
}

// The user-owned timebox, if any: user_data.timebox = { start, end } RFC3339.
export function getTimebox(t: Task): Interval | undefined {
  const ud = t.userData as Record<string, unknown> | undefined;
  const tb = ud?.timebox as Record<string, unknown> | undefined;
  if (!tb || typeof tb !== "object") return undefined;
  const start = parseISO(tb.start);
  const end = parseISO(tb.end);
  if (!start || !end) return undefined;
  return { start, end };
}

const pad2 = (n: number) => String(n).padStart(2, "0");

export function fmtTime(d: Date): string {
  return `${d.getHours()}:${pad2(d.getMinutes())}`;
}

export function fmtRange(a: Date, b: Date): string {
  return `${fmtTime(a)}–${fmtTime(b)}`; // en dash
}

export function fmtFullDate(d: Date): string {
  return d.toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" });
}

export function sameDay(a: Date, b: Date): boolean {
  return (
    a.getFullYear() === b.getFullYear() &&
    a.getMonth() === b.getMonth() &&
    a.getDate() === b.getDate()
  );
}

export function minutesOfDay(d: Date): number {
  return d.getHours() * 60 + d.getMinutes();
}

// The seven days (Mon–Sun) of the week containing `now`, each at local midnight.
export function weekDays(now: Date): Date[] {
  const monday = new Date(now.getFullYear(), now.getMonth(), now.getDate());
  const offset = (now.getDay() + 6) % 7; // days since Monday (Mon=0 … Sun=6)
  monday.setDate(monday.getDate() - offset);
  return Array.from({ length: 7 }, (_, i) => {
    const d = new Date(monday);
    d.setDate(monday.getDate() + i);
    return d;
  });
}

// Position an interval within the visible band of the day it starts in,
// clamped to [HOUR_START, HOUR_END]. Returns null when it lies fully outside.
// Intervals that run past midnight (e.g. an all-day event's next-midnight end)
// clamp to the day's end.
export function blockRect(start: Date, end: Date): { top: number; height: number } | null {
  const s = minutesOfDay(start);
  let e = minutesOfDay(end);
  if (!sameDay(start, end) || e <= s) e = 24 * 60;
  const top = Math.max(s, HOUR_START * 60);
  const bottom = Math.min(e, HOUR_END * 60);
  if (bottom <= top) return null;
  return {
    top: (top - HOUR_START * 60) * PX_PER_MIN,
    height: (bottom - top) * PX_PER_MIN,
  };
}

// Convert a drop's pixel offset within a day column into a snapped
// start-minute-of-day, clamped so a default-length box stays in the band.
export function dropMinutes(offsetY: number): number {
  const raw = HOUR_START * 60 + offsetY / PX_PER_MIN;
  const snapped = Math.round(raw / SNAP_MIN) * SNAP_MIN;
  return Math.min(Math.max(snapped, HOUR_START * 60), HOUR_END * 60 - DEFAULT_BOX_MIN);
}

// Greedy side-by-side lane assignment for overlapping blocks in one column.
// Returns each block's lane index and the total lane count (column divisor).
export function assignLanes<T extends { top: number; height: number }>(
  blocks: T[],
): { placements: { block: T; lane: number }[]; lanes: number } {
  const sorted = [...blocks].sort((a, b) => a.top - b.top || a.height - b.height);
  const laneEnds: number[] = []; // bottom px of the last block in each lane
  const placements = sorted.map((block) => {
    let lane = laneEnds.findIndex((end) => end <= block.top + 0.5);
    if (lane === -1) {
      lane = laneEnds.length;
      laneEnds.push(0);
    }
    laneEnds[lane] = block.top + block.height;
    return { block, lane };
  });
  return { placements, lanes: Math.max(1, laneEnds.length) };
}
