// Shared time math, task classification, and style tokens for the gcal web
// half. Kept framework-free so the components stay declarative — every helper
// here is pure and unit-tested (see util.test.ts). Geometry takes the current
// pixels-per-minute (zoom) explicitly rather than reading a module constant, so
// the same math serves any zoom level.

import type { Task } from "@taskd/extension-api";

// The hour band of the day grid — the full calendar day, 00:00–24:00. The
// grid overflows the panel and scrolls (see main.tsx for the initial anchor).
export const HOUR_START = 0; //  00:00
export const HOUR_END = 24; // 24:00 (next midnight)
export const DAY_MINUTES = (HOUR_END - HOUR_START) * 60; // 1440
export const GUTTER_W = 54; // hour-label column width, px
export const HEADER_H = 46; // day-header row height, px (sticky offset base)

// Timebox authoring granularity.
export const SNAP_MIN = 30; // move / drop snapping
export const RESIZE_SNAP_MIN = 15; // resize / duration snapping (finer)
export const DEFAULT_BOX_MIN = 60; // default timebox duration on drop
export const MIN_BOX_MIN = 15; // shortest a timebox may be

// Zoom: pixels-per-minute is stateful in the UI (see main.tsx) and clamped to
// this band. DEFAULT_PX_PER_MIN preserves the historic 54px/hour scale.
export const DEFAULT_PX_PER_MIN = 0.9; // 54 px/hour
export const MIN_PX_PER_MIN = 0.5; // 30 px/hour (zoomed out)
export const MAX_PX_PER_MIN = 2.4; // 144 px/hour (zoomed in)
export const ZOOM_FACTOR = 1.25; // multiplier per zoom step
export const ZOOM_KEY = "taskd-gcal-zoom"; // localStorage namespace
export const VIEW_KEY = "taskd-gcal-view"; // localStorage namespace

// Back-compat alias: the historic constant is now just the default zoom.
export const PX_PER_MIN = DEFAULT_PX_PER_MIN;

// The host fonts, referenced by name per the extension contract (Tailwind's
// --font-* tokens are not part of it).
export const MONO = '"IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, monospace';

export interface Interval {
  start: Date;
  end: Date;
}

const clamp = (v: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, v));
const snapTo = (min: number, step: number) => Math.round(min / step) * step;

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

// Local midnight of `d`.
export function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

// `d` shifted by `n` whole days (at local midnight).
export function addDays(d: Date, n: number): Date {
  const r = startOfDay(d);
  r.setDate(r.getDate() + n);
  return r;
}

// A Date on `base`'s calendar day at `minuteOfDay` minutes past midnight.
export function atMinute(base: Date, minuteOfDay: number): Date {
  return new Date(base.getFullYear(), base.getMonth(), base.getDate(), 0, Math.round(minuteOfDay));
}

// --- zoom ------------------------------------------------------------------

export function clampZoom(px: number): number {
  if (!Number.isFinite(px)) return DEFAULT_PX_PER_MIN;
  return clamp(px, MIN_PX_PER_MIN, MAX_PX_PER_MIN);
}
export function zoomIn(px: number): number {
  return clampZoom(px * ZOOM_FACTOR);
}
export function zoomOut(px: number): number {
  return clampZoom(px / ZOOM_FACTOR);
}

// localStorage is optional (unavailable in the node test env); read/write are
// defensive so the helpers stay call-anywhere.
export function loadZoom(): number {
  try {
    const raw = typeof localStorage !== "undefined" ? localStorage.getItem(ZOOM_KEY) : null;
    return raw ? clampZoom(parseFloat(raw)) : DEFAULT_PX_PER_MIN;
  } catch {
    return DEFAULT_PX_PER_MIN;
  }
}
export function saveZoom(px: number): void {
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(ZOOM_KEY, String(px));
  } catch {
    /* ignore */
  }
}

// --- multi-day view --------------------------------------------------------

export type ViewMode = "day" | "3day" | "week";
export const VIEW_MODES: ViewMode[] = ["day", "3day", "week"];

// Column count for a mode; also the number of days prev/next steps by.
export function spanFor(mode: ViewMode): number {
  return mode === "week" ? 7 : mode === "3day" ? 3 : 1;
}

// Monday (local midnight) of the ISO week containing `d`.
export function weekStart(d: Date): Date {
  const off = (startOfDay(d).getDay() + 6) % 7; // Mon=0 … Sun=6
  return addDays(d, -off);
}

// The days a mode shows, given an anchor day. Week snaps to its Monday; day and
// 3-day start at the anchor.
export function viewDays(anchor: Date, mode: ViewMode): Date[] {
  const start = mode === "week" ? weekStart(anchor) : startOfDay(anchor);
  return Array.from({ length: spanFor(mode) }, (_, i) => addDays(start, i));
}

// The seven days (Mon–Sun) of the week containing `now` — kept as a named
// helper (used elsewhere/tests); now expressed through viewDays.
export function weekDays(now: Date): Date[] {
  return viewDays(now, "week");
}

export function loadView(): ViewMode {
  try {
    const raw = typeof localStorage !== "undefined" ? localStorage.getItem(VIEW_KEY) : null;
    return (VIEW_MODES as string[]).includes(raw ?? "") ? (raw as ViewMode) : "day";
  } catch {
    return "day";
  }
}
export function saveView(mode: ViewMode): void {
  try {
    if (typeof localStorage !== "undefined") localStorage.setItem(VIEW_KEY, mode);
  } catch {
    /* ignore */
  }
}

// --- geometry (all zoom-parameterized) -------------------------------------

export function gridHeight(pxPerMin: number): number {
  return DAY_MINUTES * pxPerMin;
}

// Minute-of-day → y within the band (may fall outside for off-band minutes).
export function minuteToY(minute: number, pxPerMin: number): number {
  return (minute - HOUR_START * 60) * pxPerMin;
}

// y within the band → minute-of-day (unsnapped, unclamped).
export function yToMinute(y: number, pxPerMin: number): number {
  return HOUR_START * 60 + y / pxPerMin;
}

// Position an interval within the full-day band of the day it starts in,
// clamped to [HOUR_START, HOUR_END] (00:00–24:00). Returns null only for a
// degenerate interval with no positive height. Intervals that run past midnight
// (e.g. an all-day event's next-midnight end) clamp to the day's end (24:00).
export function blockRect(
  start: Date,
  end: Date,
  pxPerMin: number,
): { top: number; height: number } | null {
  const s = minutesOfDay(start);
  let e = minutesOfDay(end);
  if (!sameDay(start, end) || e <= s) e = 24 * 60;
  const top = Math.max(s, HOUR_START * 60);
  const bottom = Math.min(e, HOUR_END * 60);
  if (bottom <= top) return null;
  return {
    top: (top - HOUR_START * 60) * pxPerMin,
    height: (bottom - top) * pxPerMin,
  };
}

// Convert a drop's pixel offset within a day column into a snapped
// start-minute-of-day, clamped so a default-length box stays in the band.
export function dropMinutes(offsetY: number, pxPerMin: number): number {
  const snapped = snapTo(yToMinute(offsetY, pxPerMin), SNAP_MIN);
  return clamp(snapped, HOUR_START * 60, HOUR_END * 60 - DEFAULT_BOX_MIN);
}

// --- timebox mutation (pure — used by drag, resize, and keyboard) ----------

// Move an interval to start at `startMin` (minute-of-day on its start's day),
// preserving duration and clamping into the band.
export function withStartMinute(iv: Interval, startMin: number): Interval {
  const durMs = iv.end.getTime() - iv.start.getTime();
  const durMin = durMs / 60_000;
  const start = atMinute(iv.start, clamp(startMin, HOUR_START * 60, HOUR_END * 60 - durMin));
  return { start, end: new Date(start.getTime() + durMs) };
}

// Set an interval's end to `endMin`, enforcing MIN_BOX_MIN and the band end.
export function withEndMinute(iv: Interval, endMin: number): Interval {
  const startMin = minutesOfDay(iv.start);
  const end = atMinute(iv.start, clamp(endMin, startMin + MIN_BOX_MIN, HOUR_END * 60));
  return { start: iv.start, end };
}

// Set an interval's start to `startMin`, keeping its end fixed and enforcing
// MIN_BOX_MIN and the band start (the top-edge-resize counterpart of
// withEndMinute; unlike withStartMinute, duration is not preserved).
export function withStartMinuteKeepEnd(iv: Interval, startMin: number): Interval {
  const endMin = minutesOfDay(iv.end);
  const start = atMinute(iv.end, clamp(startMin, HOUR_START * 60, endMin - MIN_BOX_MIN));
  return { start, end: iv.end };
}

// Drag-move by a pixel delta: shift start, snapped to SNAP_MIN.
export function movedTimebox(iv: Interval, deltaPx: number, pxPerMin: number): Interval {
  const raw = minutesOfDay(iv.start) + deltaPx / pxPerMin;
  return withStartMinute(iv, snapTo(raw, SNAP_MIN));
}

// Drag-resize the bottom edge by a pixel delta: move end, snapped to
// RESIZE_SNAP_MIN.
export function resizedTimebox(iv: Interval, deltaPx: number, pxPerMin: number): Interval {
  const raw = minutesOfDay(iv.end) + deltaPx / pxPerMin;
  return withEndMinute(iv, snapTo(raw, RESIZE_SNAP_MIN));
}

// Drag-resize the top edge by a pixel delta: move start, snapped to
// RESIZE_SNAP_MIN, preserving the end.
export function resizedTimeboxStart(iv: Interval, deltaPx: number, pxPerMin: number): Interval {
  const raw = minutesOfDay(iv.start) + deltaPx / pxPerMin;
  return withStartMinuteKeepEnd(iv, snapTo(raw, RESIZE_SNAP_MIN));
}

// Keyboard nudges: shift start / grow-shrink duration by whole minutes.
export function nudgeStart(iv: Interval, deltaMin: number): Interval {
  return withStartMinute(iv, minutesOfDay(iv.start) + deltaMin);
}
export function nudgeDuration(iv: Interval, deltaMin: number): Interval {
  return withEndMinute(iv, minutesOfDay(iv.end) + deltaMin);
}

// --- overlap lane packing --------------------------------------------------

export interface LanePlacement<T> {
  block: T;
  /** 0-based column the block starts in. */
  lane: number;
  /** How many adjacent columns it spans (expands right into free space). */
  span: number;
  /** Column count of the block's overlap cluster (its width divisor). */
  cols: number;
}

// Greedy calendar-style packing of overlapping blocks into side-by-side
// columns. Beyond a naive equal-width split, each block expands rightward into
// any columns that are free during its span, so isolated blocks read wide and
// only genuinely-overlapping ones get narrow. `lanes` is the max column count
// across clusters (kept for callers/tests that want a single divisor).
export function assignLanes<T extends { top: number; height: number }>(
  blocks: T[],
): { placements: LanePlacement<T>[]; lanes: number } {
  const eps = 0.5;
  const overlaps = (a: { top: number; height: number }, b: { top: number; height: number }) =>
    a.top < b.top + b.height - eps && b.top < a.top + a.height - eps;

  const sorted = [...blocks].sort((a, b) => a.top - b.top || a.height - b.height);

  // Partition into clusters of transitively-overlapping blocks.
  const clusters: T[][] = [];
  let cur: T[] = [];
  let clusterBottom = -Infinity;
  for (const block of sorted) {
    if (cur.length > 0 && block.top >= clusterBottom - eps) {
      clusters.push(cur);
      cur = [];
      clusterBottom = -Infinity;
    }
    cur.push(block);
    clusterBottom = Math.max(clusterBottom, block.top + block.height);
  }
  if (cur.length > 0) clusters.push(cur);

  const placements: LanePlacement<T>[] = [];
  let maxCols = 1;
  for (const cluster of clusters) {
    // Assign each block to the first column free at its top.
    const columns: T[][] = [];
    const laneOf = new Map<T, number>();
    for (const block of cluster) {
      let lane = columns.findIndex((col) => col.every((o) => !overlaps(o, block)));
      if (lane === -1) {
        lane = columns.length;
        columns.push([]);
      }
      columns[lane].push(block);
      laneOf.set(block, lane);
    }
    const cols = Math.max(1, columns.length);
    maxCols = Math.max(maxCols, cols);
    // Expand each block rightward while the next column is free during it.
    for (const block of cluster) {
      const lane = laneOf.get(block)!;
      let span = 1;
      for (let c = lane + 1; c < cols; c++) {
        if (columns[c].some((o) => overlaps(o, block))) break;
        span++;
      }
      placements.push({ block, lane, span, cols });
    }
  }
  return { placements, lanes: maxCols };
}
