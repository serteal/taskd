import { describe, expect, it } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Task } from "../gen/task/task_pb";
import { dayKey, startOfWeek, upcomingFlatTasks, upcomingModel } from "./upcoming";

const now = new Date(2026, 6, 12, 10, 0, 0); // Sunday 2026-07-12

let n = 0;
function task(p: { title?: string; due?: Date; source?: string }): Task {
  return {
    id: `t${++n}`,
    title: p.title ?? "t",
    notes: "",
    labels: [],
    source: p.source ?? "",
    externalRef: "",
    revision: 0n,
    dueTime: p.due ? timestampFromDate(p.due) : undefined,
  } as unknown as Task;
}

const day = (d: number, h = 12) => new Date(2026, 6, d, h, 0, 0);

describe("upcomingModel", () => {
  it("buckets local tasks by day and leads with overdue", () => {
    const tasks = [
      task({ title: "slipped", due: day(10) }),
      task({ title: "today", due: day(12) }),
      task({ title: "tomorrow", due: day(13) }),
      task({ title: "undated" }),
    ];
    const m = upcomingModel(tasks, now, new Date(2026, 6, 12), 14);
    expect(m.overdue.map((t) => t.title)).toEqual(["slipped"]);
    expect(m.days).toHaveLength(14);
    expect(m.days[0].tasks.map((t) => t.title)).toEqual(["today"]);
    expect(m.days[1].tasks.map((t) => t.title)).toEqual(["tomorrow"]);
    // undated never appears
    expect(upcomingFlatTasks(m).map((t) => t.title)).toEqual(["slipped", "today", "tomorrow"]);
  });

  it("synced items land as events, not tasks", () => {
    const tasks = [
      task({ title: "standup", due: day(12, 9), source: "gcal:personal" }),
      task({ title: "local", due: day(12) }),
    ];
    const m = upcomingModel(tasks, now, new Date(2026, 6, 12), 7);
    expect(m.days[0].events.map((t) => t.title)).toEqual(["standup"]);
    expect(m.days[0].tasks.map((t) => t.title)).toEqual(["local"]);
    // events stay out of the keyboard order
    expect(upcomingFlatTasks(m).map((t) => t.title)).toEqual(["local"]);
  });

  it("orders a day by time, then omits overdue when browsing ahead", () => {
    const tasks = [
      task({ title: "late", due: day(13, 20) }),
      task({ title: "early", due: day(13, 8) }),
      task({ title: "slipped", due: day(1) }),
    ];
    const ahead = upcomingModel(tasks, now, new Date(2026, 6, 13), 7, { includeOverdue: false });
    expect(ahead.overdue).toEqual([]);
    expect(ahead.days[0].tasks.map((t) => t.title)).toEqual(["early", "late"]);
  });

  it("an overdue synced item is neither overdue nor an event outside the window", () => {
    const m = upcomingModel(
      [task({ title: "old event", due: day(1), source: "gcal:x" })],
      now,
      new Date(2026, 6, 12),
      7,
    );
    expect(m.overdue).toEqual([]);
    expect(m.days.every((d) => d.events.length === 0)).toBe(true);
  });
});

describe("week math", () => {
  it("startOfWeek is the Monday of the containing week", () => {
    expect(dayKey(startOfWeek(new Date(2026, 6, 12)))).toBe("2026-07-06"); // Sun → prior Mon
    expect(dayKey(startOfWeek(new Date(2026, 6, 6)))).toBe("2026-07-06"); // Mon → itself
    expect(dayKey(startOfWeek(new Date(2026, 6, 8)))).toBe("2026-07-06"); // Wed
  });
});
