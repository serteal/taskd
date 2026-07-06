import { describe, expect, it } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Task } from "../gen/task/task_pb";
import { dueTaskIds, missedSince } from "./reminders";

const NOW = new Date(2026, 6, 6, 12, 0, 0); // Mon 2026-07-06, noon

const task = (id: string, p: Partial<{ due: Date; source: string }> = {}): Task =>
  ({
    id,
    source: p.source ?? "",
    dueTime: p.due ? timestampFromDate(p.due) : undefined,
  }) as unknown as Task;

describe("dueTaskIds", () => {
  it("includes a local task due exactly now or in the past", () => {
    const past = task("past", { due: new Date(2026, 6, 6, 11) });
    const exact = task("exact", { due: NOW });
    expect(dueTaskIds([past, exact], NOW)).toEqual(new Set(["past", "exact"]));
  });

  it("excludes a local task due in the future", () => {
    const future = task("future", { due: new Date(2026, 6, 6, 13) });
    expect(dueTaskIds([future], NOW)).toEqual(new Set());
  });

  it("excludes a task with no due date", () => {
    expect(dueTaskIds([task("none")], NOW)).toEqual(new Set());
  });

  it("excludes a synced (non-local) task even when overdue", () => {
    const synced = task("synced", { due: new Date(2026, 6, 6, 11), source: "gcal:personal" });
    expect(dueTaskIds([synced], NOW)).toEqual(new Set());
  });
});

describe("missedSince", () => {
  const WATERMARK = new Date(2026, 6, 6, 10).getTime(); // 10:00, two hours ago
  const ids = (ts: Task[]) => ts.map((t) => t.id);

  it("includes local tasks due strictly after the watermark and at or before now", () => {
    const inside = task("inside", { due: new Date(2026, 6, 6, 11) }); // 11:00
    const alsoIn = task("alsoIn", { due: new Date(2026, 6, 6, 10, 30) }); // 10:30
    expect(ids(missedSince([inside, alsoIn], WATERMARK, NOW)).sort()).toEqual(["alsoIn", "inside"]);
  });

  it("is empty when the window is empty (watermark == now)", () => {
    const atNow = task("atNow", { due: NOW });
    expect(missedSince([atNow], NOW.getTime(), NOW)).toEqual([]);
  });

  it("excludes a task due exactly at the watermark (lower bound is exclusive)", () => {
    const atWatermark = task("atWatermark", { due: new Date(WATERMARK) });
    expect(missedSince([atWatermark], WATERMARK, NOW)).toEqual([]);
  });

  it("includes a task due exactly at now (upper bound is inclusive)", () => {
    const atNow = task("atNow", { due: NOW });
    expect(ids(missedSince([atNow], WATERMARK, NOW))).toEqual(["atNow"]);
  });

  it("excludes a task due after now (still in the future)", () => {
    const future = task("future", { due: new Date(2026, 6, 6, 13) });
    expect(missedSince([future], WATERMARK, NOW)).toEqual([]);
  });

  it("excludes a synced task even inside the window", () => {
    const synced = task("synced", { due: new Date(2026, 6, 6, 11), source: "gcal:personal" });
    expect(missedSince([synced], WATERMARK, NOW)).toEqual([]);
  });

  it("excludes a task with no due date", () => {
    expect(missedSince([task("none")], WATERMARK, NOW)).toEqual([]);
  });
});
