import { describe, expect, it } from "vitest";
import type { Task } from "@taskd/extension-api";
import {
  DEFAULT_BOX_MIN,
  DEFAULT_PX_PER_MIN,
  HOUR_END,
  HOUR_START,
  MAX_PX_PER_MIN,
  MIN_BOX_MIN,
  MIN_PX_PER_MIN,
  assignLanes,
  atMinute,
  blockRect,
  clampZoom,
  dropMinutes,
  eventInterval,
  getTimebox,
  isAllDay,
  isGcal,
  minutesOfDay,
  movedTimebox,
  nudgeDuration,
  nudgeStart,
  parseISO,
  resizedTimebox,
  resizedTimeboxStart,
  sameDay,
  spanFor,
  viewDays,
  weekDays,
  weekStart,
  withEndMinute,
  withStartMinute,
  withStartMinuteKeepEnd,
  zoomIn,
  zoomOut,
  type Interval,
} from "./util";

const task = (p: Partial<{ source: string; externalData: unknown; userData: unknown }>): Task =>
  ({ source: p.source ?? "", externalData: p.externalData, userData: p.userData }) as unknown as Task;

const PX = DEFAULT_PX_PER_MIN;
// A 09:00–10:00 interval on a fixed day, handy for mutation tests.
const iv = (h1: number, m1: number, h2: number, m2: number): Interval => ({
  start: new Date(2026, 6, 6, h1, m1),
  end: new Date(2026, 6, 6, h2, m2),
});

describe("dropMinutes", () => {
  it("snaps to the 30-minute grid", () => {
    expect(dropMinutes(0, PX)).toBe(HOUR_START * 60); // 0 (top of the day)
    expect(dropMinutes(27, PX)).toBe(30); // 27 / 0.9 = 30 → snaps to 30
  });
  it("clamps so a default box stays inside the day (00:00–24:00)", () => {
    expect(dropMinutes(-1000, PX)).toBe(HOUR_START * 60); // 0
    expect(dropMinutes(1_000_000, PX)).toBe(HOUR_END * 60 - DEFAULT_BOX_MIN); // 1380 (23:00)
  });
  it("respects the current zoom", () => {
    // 108px at 2x zoom covers the same 60 minutes as 54px at 1x.
    expect(dropMinutes(108, PX * 2)).toBe(dropMinutes(54, PX));
    expect(dropMinutes(108, PX * 2)).toBe(HOUR_START * 60 + 60); // 60 (01:00)
  });
});

describe("sameDay / minutesOfDay", () => {
  it("compares calendar days", () => {
    expect(sameDay(new Date(2026, 6, 6, 1), new Date(2026, 6, 6, 23))).toBe(true);
    expect(sameDay(new Date(2026, 6, 6), new Date(2026, 6, 7))).toBe(false);
  });
  it("counts minutes since local midnight", () => {
    expect(minutesOfDay(new Date(2026, 6, 6, 9, 30))).toBe(570);
  });
});

describe("blockRect", () => {
  it("positions an interval by its true minute-of-day and scales with zoom", () => {
    // 09:00 = 540 min from midnight; the band starts at 00:00 so top = 540*px.
    const r = blockRect(new Date(2026, 6, 6, 9), new Date(2026, 6, 6, 10), PX)!;
    expect(r.top).toBeCloseTo(540 * 0.9); // 486
    expect(r.height).toBeCloseTo(60 * 0.9); // 54
    const zoomed = blockRect(new Date(2026, 6, 6, 9), new Date(2026, 6, 6, 10), PX * 2)!;
    expect(zoomed.height).toBeCloseTo(r.height * 2);
  });
  it("renders an early-morning event (05:30) at its true position", () => {
    // Previously outside the 07:00 window (clipped to null); now on the grid.
    const r = blockRect(new Date(2026, 6, 6, 5, 30), new Date(2026, 6, 6, 6, 30), PX)!;
    expect(r).not.toBeNull();
    expect(r.top).toBeCloseTo(330 * 0.9); // 05:30 = 330 min → 297px
    expect(r.height).toBeCloseTo(60 * 0.9); // 54
  });
  it("renders a late event ending at 23:45 near the bottom of the grid", () => {
    const r = blockRect(new Date(2026, 6, 6, 22, 0), new Date(2026, 6, 6, 23, 45), PX)!;
    expect(r).not.toBeNull();
    expect(r.top).toBeCloseTo(1320 * 0.9); // 22:00 = 1320 min → 1188px
    expect(r.height).toBeCloseTo(105 * 0.9); // 105 min → 94.5px, well inside 24:00
  });
  it("clamps an interval running past midnight to the day end (24:00)", () => {
    const r = blockRect(new Date(2026, 6, 6, 23, 0), new Date(2026, 6, 7, 1, 0), PX)!;
    expect(r.top).toBeCloseTo(1380 * 0.9); // 23:00
    // Bottom clamps to 24:00 (1440 min), not the next-day 01:00.
    expect(r.top + r.height).toBeCloseTo(HOUR_END * 60 * 0.9); // 1440*0.9
  });
});

describe("zoom", () => {
  it("clamps to the band", () => {
    expect(clampZoom(999)).toBe(MAX_PX_PER_MIN);
    expect(clampZoom(0)).toBe(MIN_PX_PER_MIN);
    expect(clampZoom(Number.NaN)).toBe(DEFAULT_PX_PER_MIN);
  });
  it("steps in and out, staying in range", () => {
    expect(zoomIn(DEFAULT_PX_PER_MIN)).toBeGreaterThan(DEFAULT_PX_PER_MIN);
    expect(zoomOut(DEFAULT_PX_PER_MIN)).toBeLessThan(DEFAULT_PX_PER_MIN);
    expect(zoomIn(MAX_PX_PER_MIN)).toBe(MAX_PX_PER_MIN);
    expect(zoomOut(MIN_PX_PER_MIN)).toBe(MIN_PX_PER_MIN);
  });
});

describe("multi-day view", () => {
  it("spanFor counts columns", () => {
    expect(spanFor("day")).toBe(1);
    expect(spanFor("3day")).toBe(3);
    expect(spanFor("week")).toBe(7);
  });
  it("viewDays: day starts at the anchor", () => {
    const days = viewDays(new Date(2026, 6, 8), "day");
    expect(days).toHaveLength(1);
    expect(days[0].getDate()).toBe(8);
  });
  it("viewDays: 3-day is the anchor plus two", () => {
    const days = viewDays(new Date(2026, 6, 8), "3day");
    expect(days.map((d) => d.getDate())).toEqual([8, 9, 10]);
  });
  it("viewDays: week snaps to Monday", () => {
    const days = viewDays(new Date(2026, 6, 8), "week"); // Wed
    expect(days).toHaveLength(7);
    expect(days[0].getDay()).toBe(1); // Monday
    expect(days[0].getDate()).toBe(6);
  });
  it("weekStart is the Monday of the containing week", () => {
    expect(weekStart(new Date(2026, 6, 8)).getDate()).toBe(6);
    expect(weekStart(new Date(2026, 6, 12)).getDate()).toBe(6); // Sunday still maps back
  });
  it("weekDays returns Mon..Sun", () => {
    const days = weekDays(new Date(2026, 6, 8));
    expect(days[0].getDate()).toBe(6);
    expect(days[6].getDate()).toBe(12);
  });
});

describe("timebox mutation", () => {
  it("atMinute builds a same-day time", () => {
    const d = atMinute(new Date(2026, 6, 6, 3), 9 * 60 + 30);
    expect(d.getHours()).toBe(9);
    expect(d.getMinutes()).toBe(30);
  });
  it("withStartMinute preserves duration and clamps into the day", () => {
    const moved = withStartMinute(iv(9, 0, 10, 0), 8 * 60);
    expect(minutesOfDay(moved.start)).toBe(8 * 60);
    expect(moved.end.getTime() - moved.start.getTime()).toBe(60 * 60_000);
    // A start before 00:00 clamps to the top of the day.
    expect(minutesOfDay(withStartMinute(iv(9, 0, 10, 0), -60).start)).toBe(HOUR_START * 60); // 0
    // A start so late a 1h box would spill past 24:00 clamps to 23:00.
    expect(minutesOfDay(withStartMinute(iv(9, 0, 10, 0), 24 * 60).start)).toBe(HOUR_END * 60 - 60); // 1380
  });
  it("withEndMinute enforces a minimum duration and clamps the end to 24:00", () => {
    const shrunk = withEndMinute(iv(9, 0, 10, 0), 9 * 60 + 5); // below MIN_BOX_MIN
    expect(minutesOfDay(shrunk.end)).toBe(9 * 60 + MIN_BOX_MIN);
    // An end past 24:00 clamps to the day end — represented as next midnight.
    const capped = withEndMinute(iv(9, 0, 10, 0), 30 * 60).end;
    expect(capped.getTime()).toBe(new Date(2026, 6, 7).getTime()); // 24:00 = next midnight
  });
  it("movedTimebox shifts start by a snapped pixel delta", () => {
    // +60px at 0.9px/min = ~66min → snaps to 60 → 10:00 start.
    const moved = movedTimebox(iv(9, 0, 10, 0), 60, PX);
    expect(minutesOfDay(moved.start)).toBe(10 * 60);
    expect(moved.end.getTime() - moved.start.getTime()).toBe(60 * 60_000);
  });
  it("resizedTimebox moves the end by a snapped pixel delta", () => {
    // +27px at 0.9px/min = 30min → 10:30 end (snap 15).
    const resized = resizedTimebox(iv(9, 0, 10, 0), 27, PX);
    expect(minutesOfDay(resized.end)).toBe(10 * 60 + 30);
    expect(minutesOfDay(resized.start)).toBe(9 * 60); // start unchanged
  });
  it("withStartMinuteKeepEnd enforces a minimum duration and the day start", () => {
    const shrunk = withStartMinuteKeepEnd(iv(9, 0, 10, 0), 9 * 60 + 55); // above end - MIN_BOX_MIN
    expect(minutesOfDay(shrunk.start)).toBe(10 * 60 - MIN_BOX_MIN);
    // A start before 00:00 clamps to the top of the day.
    expect(minutesOfDay(withStartMinuteKeepEnd(iv(9, 0, 10, 0), -60).start)).toBe(HOUR_START * 60); // 0
    expect(minutesOfDay(shrunk.end)).toBe(10 * 60); // end unchanged
  });
  it("keeps min-duration timeboxes honored at the day edges", () => {
    // Top edge pinned against 00:00: a default box dropped at the top stays put.
    expect(minutesOfDay(withStartMinute(iv(0, 0, 1, 0), -30).start)).toBe(HOUR_START * 60); // 0
    // Resizing the top edge up past 00:00 clamps the start but keeps >= MIN.
    const top = withStartMinuteKeepEnd(iv(0, 0, 0, 30), -60); // end 00:30
    expect(minutesOfDay(top.start)).toBe(HOUR_START * 60); // 0
    expect(minutesOfDay(top.end) - minutesOfDay(top.start)).toBeGreaterThanOrEqual(MIN_BOX_MIN);
    // Bottom edge against 24:00: shrinking a 23:50 box below MIN still yields a
    // MIN_BOX_MIN duration (its end tips just past midnight).
    const bottom = withEndMinute(iv(23, 50, 23, 55), 23 * 60 + 52);
    expect(bottom.end.getTime() - bottom.start.getTime()).toBe(MIN_BOX_MIN * 60_000);
  });
  it("resizedTimeboxStart moves the start by a snapped pixel delta, preserving the end", () => {
    // -27px at 0.9px/min = -30min → 8:30 start (snap 15).
    const resized = resizedTimeboxStart(iv(9, 0, 10, 0), -27, PX);
    expect(minutesOfDay(resized.start)).toBe(8 * 60 + 30);
    expect(minutesOfDay(resized.end)).toBe(10 * 60); // end unchanged
  });
  it("nudgeStart / nudgeDuration move by whole minutes", () => {
    expect(minutesOfDay(nudgeStart(iv(9, 0, 10, 0), -30).start)).toBe(8 * 60 + 30);
    expect(minutesOfDay(nudgeDuration(iv(9, 0, 10, 0), 15).end)).toBe(10 * 60 + 15);
  });
});

describe("task classification", () => {
  it("isGcal matches the gcal source prefix", () => {
    expect(isGcal(task({ source: "gcal:personal" }))).toBe(true);
    expect(isGcal(task({ source: "github" }))).toBe(false);
  });
  it("isAllDay reads external_data.all_day", () => {
    expect(isAllDay(task({ externalData: { all_day: true } }))).toBe(true);
    expect(isAllDay(task({ externalData: {} }))).toBe(false);
  });
  it("eventInterval falls back to a 1h end", () => {
    const parsed = eventInterval(task({ externalData: { start: "2026-07-06T09:00:00Z" } }))!;
    expect(parsed.end.getTime() - parsed.start.getTime()).toBe(60 * 60_000);
    expect(eventInterval(task({ externalData: {} }))).toBeUndefined();
  });
  it("getTimebox reads user_data.timebox", () => {
    const parsed = getTimebox(
      task({ userData: { timebox: { start: "2026-07-06T09:00:00Z", end: "2026-07-06T10:00:00Z" } } }),
    )!;
    expect(parsed.start).toBeInstanceOf(Date);
    expect(getTimebox(task({ userData: {} }))).toBeUndefined();
  });
});

describe("parseISO", () => {
  it("parses valid strings, rejects the rest", () => {
    expect(parseISO("2026-07-06T09:00:00Z")).toBeInstanceOf(Date);
    expect(parseISO("nonsense")).toBeUndefined();
    expect(parseISO(42)).toBeUndefined();
  });
});

describe("assignLanes", () => {
  it("splits overlapping blocks into lanes and merges disjoint ones", () => {
    const overlap = assignLanes([
      { top: 0, height: 100 },
      { top: 50, height: 100 },
    ]);
    expect(overlap.lanes).toBe(2);

    const disjoint = assignLanes([
      { top: 0, height: 40 },
      { top: 60, height: 40 },
    ]);
    expect(disjoint.lanes).toBe(1);
  });

  it("gives disjoint blocks the full width (span === cols === 1)", () => {
    const { placements } = assignLanes([
      { top: 0, height: 40 },
      { top: 60, height: 40 },
    ]);
    for (const p of placements) {
      expect(p.cols).toBe(1);
      expect(p.span).toBe(1);
      expect(p.lane).toBe(0);
    }
  });

  it("packs a fully-overlapping triple into three columns", () => {
    const { placements, lanes } = assignLanes([
      { top: 0, height: 60 },
      { top: 0, height: 60 },
      { top: 0, height: 60 },
    ]);
    expect(lanes).toBe(3);
    for (const p of placements) {
      expect(p.cols).toBe(3);
      expect(p.span).toBe(1); // none can expand — every column is busy
    }
    expect(placements.map((p) => p.lane).sort()).toEqual([0, 1, 2]);
  });

  it("never lets a block's lane+span exceed its cluster's column count", () => {
    const blocks = [
      { top: 0, height: 100 },
      { top: 0, height: 40 },
      { top: 0, height: 40 },
      { top: 50, height: 40 },
    ];
    for (const p of assignLanes(blocks).placements) {
      expect(p.lane + p.span).toBeLessThanOrEqual(p.cols);
      expect(p.span).toBeGreaterThanOrEqual(1);
    }
  });
});
