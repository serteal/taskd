import { describe, expect, it } from "vitest";
import {
  chipParts,
  dayDiff,
  endOfDay,
  fmtStamp,
  humanDue,
  isEndOfDay,
  shortId,
  startOfDay,
  toLocalInput,
} from "./format";

const at = (y: number, m: number, d: number, hh = 12, mm = 0) => new Date(y, m - 1, d, hh, mm, 0);

describe("startOfDay / endOfDay", () => {
  it("snap to the day's edges", () => {
    const d = at(2026, 7, 6, 15, 30);
    const s = startOfDay(d);
    expect([s.getHours(), s.getMinutes(), s.getSeconds()]).toEqual([0, 0, 0]);
    const e = endOfDay(d);
    expect([e.getHours(), e.getMinutes(), e.getSeconds()]).toEqual([23, 59, 59]);
    expect(s.getDate()).toBe(6); // no rollover
  });
});

describe("dayDiff", () => {
  const now = at(2026, 7, 6, 12);
  it("counts whole calendar days regardless of time of day", () => {
    expect(dayDiff(at(2026, 7, 6, 1), now)).toBe(0);
    expect(dayDiff(at(2026, 7, 6, 23), now)).toBe(0);
    expect(dayDiff(at(2026, 7, 7, 1), now)).toBe(1);
    expect(dayDiff(at(2026, 7, 5, 23), now)).toBe(-1);
    expect(dayDiff(at(2026, 7, 16, 12), now)).toBe(10);
  });
});

describe("humanDue", () => {
  const now = at(2026, 7, 6, 12);
  it("labels near days with words and the right tone", () => {
    expect(humanDue(at(2026, 7, 6, 20), now)).toEqual({ text: "today", tone: "today" });
    expect(humanDue(at(2026, 7, 7, 9), now)).toEqual({ text: "tomorrow", tone: "soon" });
    expect(humanDue(at(2026, 7, 5, 9), now)).toEqual({ text: "yesterday", tone: "overdue" });
    expect(humanDue(at(2026, 7, 3, 9), now)).toEqual({ text: "3d ago", tone: "overdue" });
  });
  it("uses weekday for this-week and a date for later", () => {
    expect(humanDue(at(2026, 7, 9, 9), now).tone).toBe("soon"); // within 7d
    expect(humanDue(at(2026, 8, 20, 9), now).tone).toBe("later"); // > 7d
  });

  it("withTime appends the clock only for a due that carries a time-of-day", () => {
    // An end-of-day due is a plain date — no time shown even with withTime.
    expect(humanDue(endOfDay(at(2026, 7, 7, 9)), now, { withTime: true }).text).toBe("tomorrow");
    // A timed due shows HH:MM (zero-padded).
    expect(humanDue(at(2026, 7, 7, 9, 5), now, { withTime: true }).text).toBe("tomorrow 09:05");
    // Default (no opts) never appends, preserving every existing call site.
    expect(humanDue(at(2026, 7, 7, 9, 5), now).text).toBe("tomorrow");
  });
});

describe("isEndOfDay", () => {
  it("is true only at the 23:59:59 end-of-day marker", () => {
    expect(isEndOfDay(endOfDay(at(2026, 7, 6)))).toBe(true);
    expect(isEndOfDay(at(2026, 7, 6, 23, 59))).toBe(false); // seconds not 59
    expect(isEndOfDay(at(2026, 7, 6, 9, 0))).toBe(false);
  });
});

describe("chipParts", () => {
  it("splits namespaced labels only", () => {
    expect(chipParts("project:home")).toEqual({ ns: "project", val: "home" });
    expect(chipParts("waiting")).toEqual({ val: "waiting" });
    expect(chipParts(":x")).toEqual({ val: ":x" }); // no namespace before colon
    expect(chipParts("p1:")).toEqual({ val: "p1:" }); // nothing after colon
  });
});

describe("fmtStamp / toLocalInput / shortId", () => {
  it("format timestamps and ids", () => {
    const d = at(2026, 7, 6, 9, 5);
    expect(fmtStamp(d)).toBe("2026-07-06 09:05");
    expect(toLocalInput(d)).toBe("2026-07-06T09:05");
    expect(shortId("01KWQ5EN1AWDG46QYGVBCVJRC1")).toBe("01kwq5en");
  });
});
