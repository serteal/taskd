import { describe, expect, it } from "vitest";
import { parseQuickAdd } from "./quickadd";

// Friday 2026-07-03, 10:00 local.
const NOW = new Date(2026, 6, 3, 10, 0, 0);

describe("parseQuickAdd", () => {
  it("plain text is all title", () => {
    expect(parseQuickAdd("buy milk and eggs", NOW)).toEqual({
      title: "buy milk and eggs",
      labels: [],
      due: undefined,
    });
  });

  it("extracts #labels and priorities, keeps title order", () => {
    const p = parseQuickAdd("fix the #project:taskd deploy p1 #ops", NOW);
    expect(p.title).toBe("fix the deploy");
    expect(p.labels).toEqual(["project:taskd", "p1", "ops"]);
  });

  it("today and tomorrow resolve to end of day", () => {
    const today = parseQuickAdd("x today", NOW).due!;
    expect(today.getDate()).toBe(3);
    expect(today.getHours()).toBe(23);
    const tomorrow = parseQuickAdd("x tomorrow", NOW).due!;
    expect(tomorrow.getDate()).toBe(4);
  });

  it("relative days and ISO dates", () => {
    expect(parseQuickAdd("x 10d", NOW).due!.getDate()).toBe(13);
    const iso = parseQuickAdd("x 2026-12-24", NOW).due!;
    expect([iso.getFullYear(), iso.getMonth(), iso.getDate()]).toEqual([2026, 11, 24]);
  });

  it("weekdays mean the next occurrence, strictly after today", () => {
    // NOW is a Friday; "fri" must be next Friday, not today.
    expect(parseQuickAdd("x fri", NOW).due!.getDate()).toBe(10);
    expect(parseQuickAdd("x monday", NOW).due!.getDate()).toBe(6);
  });

  it("last date token wins; p4 is not a priority; dedupes labels", () => {
    const p = parseQuickAdd("ship p4 #a #a today tomorrow", NOW);
    expect(p.title).toBe("ship p4");
    expect(p.labels).toEqual(["a"]);
    expect(p.due!.getDate()).toBe(4);
  });
});
