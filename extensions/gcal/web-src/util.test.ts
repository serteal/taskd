import { describe, expect, it } from "vitest";
import type { Task } from "@taskd/extension-api";
import {
  DEFAULT_BOX_MIN,
  HOUR_END,
  HOUR_START,
  assignLanes,
  blockRect,
  dropMinutes,
  eventInterval,
  getTimebox,
  isAllDay,
  isGcal,
  minutesOfDay,
  parseISO,
  sameDay,
  weekDays,
} from "./util";

const task = (p: Partial<{ source: string; externalData: unknown; userData: unknown }>): Task =>
  ({ source: p.source ?? "", externalData: p.externalData, userData: p.userData }) as unknown as Task;

describe("dropMinutes", () => {
  it("snaps to the 30-minute grid", () => {
    expect(dropMinutes(0)).toBe(HOUR_START * 60); // 420
    expect(dropMinutes(27)).toBe(450); // 420 + 27/0.9 = 450
  });
  it("clamps so a default box stays inside the band", () => {
    expect(dropMinutes(-1000)).toBe(HOUR_START * 60);
    expect(dropMinutes(1_000_000)).toBe(HOUR_END * 60 - DEFAULT_BOX_MIN);
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
  it("returns null for an interval fully outside the band", () => {
    expect(blockRect(new Date(2026, 6, 6, 5), new Date(2026, 6, 6, 6))).toBeNull();
  });
  it("positions an in-band interval", () => {
    const r = blockRect(new Date(2026, 6, 6, 9), new Date(2026, 6, 6, 10))!;
    expect(r.top).toBeCloseTo((540 - 420) * 0.9); // 108
    expect(r.height).toBeCloseTo(60 * 0.9); // 54
  });
});

describe("weekDays", () => {
  it("returns Mon..Sun of the containing week", () => {
    const days = weekDays(new Date(2026, 6, 8)); // Wed 2026-07-08
    expect(days).toHaveLength(7);
    expect(days[0].getDay()).toBe(1); // Monday
    expect(days[0].getDate()).toBe(6);
    expect(days[6].getDate()).toBe(12);
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
    const iv = eventInterval(task({ externalData: { start: "2026-07-06T09:00:00Z" } }))!;
    expect(iv.end.getTime() - iv.start.getTime()).toBe(60 * 60_000);
    expect(eventInterval(task({ externalData: {} }))).toBeUndefined();
  });
  it("getTimebox reads user_data.timebox", () => {
    const iv = getTimebox(
      task({ userData: { timebox: { start: "2026-07-06T09:00:00Z", end: "2026-07-06T10:00:00Z" } } }),
    )!;
    expect(iv.start).toBeInstanceOf(Date);
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
});
