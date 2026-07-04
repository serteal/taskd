import { describe, expect, it } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Task } from "../gen/task/task_pb";
import { matchesView, parseView, sameView, viewToSearch, type View } from "./views";

const NOW = new Date(2026, 6, 6, 12, 0, 0); // Mon 2026-07-06

const task = (p: Partial<{ labels: string[]; due: Date; source: string }>): Task =>
  ({
    id: "t",
    labels: p.labels ?? [],
    source: p.source ?? "",
    dueTime: p.due ? timestampFromDate(p.due) : undefined,
  }) as unknown as Task;

describe("matchesView", () => {
  it("all matches everything", () => {
    expect(matchesView(task({}), { kind: "all" }, NOW)).toBe(true);
  });

  it("inbox excludes tasks with a project label", () => {
    expect(matchesView(task({}), { kind: "inbox" }, NOW)).toBe(true);
    expect(matchesView(task({ labels: ["project:home"] }), { kind: "inbox" }, NOW)).toBe(false);
    expect(matchesView(task({ labels: ["p1"] }), { kind: "inbox" }, NOW)).toBe(true);
  });

  it("today is due on or before today", () => {
    expect(matchesView(task({ due: new Date(2026, 6, 6, 20) }), { kind: "today" }, NOW)).toBe(true);
    expect(matchesView(task({ due: new Date(2026, 6, 5) }), { kind: "today" }, NOW)).toBe(true);
    expect(matchesView(task({ due: new Date(2026, 6, 7) }), { kind: "today" }, NOW)).toBe(false);
    expect(matchesView(task({}), { kind: "today" }, NOW)).toBe(false);
  });

  it("upcoming is any dated task", () => {
    expect(matchesView(task({ due: new Date(2026, 6, 9) }), { kind: "upcoming" }, NOW)).toBe(true);
    expect(matchesView(task({}), { kind: "upcoming" }, NOW)).toBe(false);
  });

  it("label and source filter by exact value", () => {
    expect(matchesView(task({ labels: ["waiting"] }), { kind: "label", label: "waiting" }, NOW)).toBe(true);
    expect(matchesView(task({ labels: ["x"] }), { kind: "label", label: "waiting" }, NOW)).toBe(false);
    expect(matchesView(task({ source: "github" }), { kind: "source", source: "github" }, NOW)).toBe(true);
  });

  it("completed and ext are never replica matches", () => {
    expect(matchesView(task({}), { kind: "completed" }, NOW)).toBe(false);
    expect(matchesView(task({}), { kind: "ext", id: "gcal" }, NOW)).toBe(false);
  });
});

describe("parseView / viewToSearch round trip", () => {
  const views: View[] = [
    { kind: "inbox" },
    { kind: "today" },
    { kind: "upcoming" },
    { kind: "all" },
    { kind: "completed" },
    { kind: "label", label: "project:home" },
    { kind: "source", source: "github" },
    { kind: "ext", id: "gcal" },
  ];

  it("survives a search-string round trip", () => {
    for (const v of views) {
      const parsed = parseView(viewToSearch(v));
      expect(sameView(parsed, v), JSON.stringify(v)).toBe(true);
    }
  });

  it("defaults to today for an unknown/empty search", () => {
    expect(parseView("")).toEqual({ kind: "today" });
    expect(parseView("?view=bogus")).toEqual({ kind: "today" });
  });
});
