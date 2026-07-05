import { beforeEach, describe, expect, it, vi } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Task } from "../gen/task/task_pb";
import {
  matchesView,
  parseView,
  sameView,
  viewToSearch,
  readViewPrefs,
  writeViewPrefs,
  DEFAULT_VIEW_PREFS,
  type View,
} from "./views";

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

describe("view prefs (readViewPrefs / writeViewPrefs)", () => {
  const mem = new Map<string, string>();
  const goodStorage = {
    getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
    setItem: (k: string, v: string) => void mem.set(k, v),
    removeItem: (k: string) => void mem.delete(k),
    clear: () => mem.clear(),
  };

  beforeEach(() => {
    mem.clear();
    vi.stubGlobal("localStorage", goodStorage);
  });

  it("defaults when nothing is stored for the view", () => {
    expect(readViewPrefs({ kind: "today" })).toEqual(DEFAULT_VIEW_PREFS);
  });

  it("round-trips a written value for that view", () => {
    const prefs = { sort: "title" as const, board: true, groupBy: "project" as const };
    writeViewPrefs({ kind: "today" }, prefs);
    expect(readViewPrefs({ kind: "today" })).toEqual(prefs);
  });

  it("keeps prefs independent across different views", () => {
    writeViewPrefs({ kind: "today" }, { sort: "title", board: true, groupBy: "project" });
    expect(readViewPrefs({ kind: "all" })).toEqual(DEFAULT_VIEW_PREFS);
  });

  it("merges a partial/legacy stored entry over the defaults", () => {
    mem.set("taskd-view-prefs", JSON.stringify({ [viewToSearch({ kind: "today" })]: { sort: "manual" } }));
    expect(readViewPrefs({ kind: "today" })).toEqual({ ...DEFAULT_VIEW_PREFS, sort: "manual" });
  });

  it("falls back to defaults when stored JSON is corrupt", () => {
    mem.set("taskd-view-prefs", "{not json");
    expect(readViewPrefs({ kind: "today" })).toEqual(DEFAULT_VIEW_PREFS);
  });

  it("write swallows a storage error instead of throwing", () => {
    vi.stubGlobal("localStorage", {
      getItem: goodStorage.getItem,
      setItem: () => {
        throw new Error("quota exceeded");
      },
    });
    expect(() => writeViewPrefs({ kind: "today" }, DEFAULT_VIEW_PREFS)).not.toThrow();
  });
});
