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
  readDefaultView,
  writeDefaultView,
  promotedSections,
  countView,
  DEFAULT_VIEW_PREFS,
  type View,
  type PromotableKind,
} from "./views";
import { savedFilters, type FilterPredicate, type SavedFilter } from "./filters";

const NOW = new Date(2026, 6, 6, 12, 0, 0); // Mon 2026-07-06

const task = (p: Partial<{ labels: string[]; due: Date; source: string }>): Task =>
  ({
    id: "t",
    labels: p.labels ?? [],
    source: p.source ?? "",
    dueTime: p.due ? timestampFromDate(p.due) : undefined,
  }) as unknown as Task;

describe("matchesView", () => {
  it("all matches local tasks but excludes synced items", () => {
    expect(matchesView(task({}), { kind: "all" }, NOW)).toBe(true);
    expect(matchesView(task({ source: "github" }), { kind: "all" }, NOW)).toBe(false);
    expect(matchesView(task({ source: "gcal:personal" }), { kind: "all" }, NOW)).toBe(false);
  });

  it("inbox excludes project labels and synced items", () => {
    expect(matchesView(task({}), { kind: "inbox" }, NOW)).toBe(true);
    expect(matchesView(task({ labels: ["project:home"] }), { kind: "inbox" }, NOW)).toBe(false);
    expect(matchesView(task({ labels: ["p1"] }), { kind: "inbox" }, NOW)).toBe(true);
    // A synced task with no project label still does NOT land in Inbox.
    expect(matchesView(task({ source: "github" }), { kind: "inbox" }, NOW)).toBe(false);
  });

  it("today is a local task due on or before today", () => {
    expect(matchesView(task({ due: new Date(2026, 6, 6, 20) }), { kind: "today" }, NOW)).toBe(true);
    expect(matchesView(task({ due: new Date(2026, 6, 5) }), { kind: "today" }, NOW)).toBe(true);
    expect(matchesView(task({ due: new Date(2026, 6, 7) }), { kind: "today" }, NOW)).toBe(false);
    expect(matchesView(task({}), { kind: "today" }, NOW)).toBe(false);
    // Synced items never leak into Today, even when due today.
    expect(
      matchesView(task({ due: new Date(2026, 6, 6), source: "gcal:personal" }), { kind: "today" }, NOW),
    ).toBe(false);
  });

  it("upcoming is a local task due strictly after today", () => {
    expect(matchesView(task({ due: new Date(2026, 6, 9) }), { kind: "upcoming" }, NOW)).toBe(true); // 3d ahead
    expect(matchesView(task({ due: new Date(2026, 6, 7) }), { kind: "upcoming" }, NOW)).toBe(true); // tomorrow
    // Due today or overdue belongs to Today now — Upcoming no longer overlaps.
    expect(matchesView(task({ due: new Date(2026, 6, 6, 20) }), { kind: "upcoming" }, NOW)).toBe(false);
    expect(matchesView(task({ due: new Date(2026, 6, 5) }), { kind: "upcoming" }, NOW)).toBe(false);
    expect(matchesView(task({}), { kind: "upcoming" }, NOW)).toBe(false);
    expect(
      matchesView(task({ due: new Date(2026, 6, 9), source: "github" }), { kind: "upcoming" }, NOW),
    ).toBe(false);
  });

  it("label is local-by-default; includeSynced is the escape hatch; source is unconditional", () => {
    expect(matchesView(task({ labels: ["waiting"] }), { kind: "label", label: "waiting" }, NOW)).toBe(true);
    expect(matchesView(task({ labels: ["x"] }), { kind: "label", label: "waiting" }, NOW)).toBe(false);
    // A synced task carrying the label is hidden from the (local-by-default)
    // label view …
    const gh = task({ source: "github", labels: ["bug"] });
    expect(matchesView(gh, { kind: "label", label: "bug" }, NOW)).toBe(false);
    // … until includeSynced opts it in.
    expect(matchesView(gh, { kind: "label", label: "bug", includeSynced: true }, NOW)).toBe(true);
    // A source view always shows its synced feed.
    expect(matchesView(task({ source: "github" }), { kind: "source", source: "github" }, NOW)).toBe(true);
  });

  it("a filter view resolves its SavedFilter and can promote synced items", () => {
    const f = savedFilters.add({ name: "Reviews", predicate: { source: "github", labelsAny: ["review"] } });
    const gh = task({ source: "github", labels: ["review"] });
    expect(matchesView(gh, { kind: "filter", id: f.id }, NOW)).toBe(true);
    // A local task without the label is excluded.
    expect(matchesView(task({}), { kind: "filter", id: f.id }, NOW)).toBe(false);
    // An unknown filter id matches nothing.
    expect(matchesView(gh, { kind: "filter", id: "missing" }, NOW)).toBe(false);
    savedFilters.remove(f.id);
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
    { kind: "label", label: "bug", includeSynced: true },
    { kind: "source", source: "github" },
    { kind: "filter", id: "abc123" },
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

  it("reads the label synced=1 escape-hatch flag", () => {
    expect(parseView("?label=bug")).toEqual({ kind: "label", label: "bug", includeSynced: false });
    expect(parseView("?label=bug&synced=1")).toEqual({ kind: "label", label: "bug", includeSynced: true });
  });
});

describe("promotedSections & countView (showIn promotion)", () => {
  const mk = (id: string, p: Parameters<typeof task>[0]): Task => ({ ...task(p), id }) as Task;
  const f = (id: string, predicate: FilterPredicate, showIn: PromotableKind[]): SavedFilter => ({
    id,
    name: id,
    predicate,
    showIn,
  });

  it("promotes a filter's synced matches as a section, excluding base-view members", () => {
    const baseLocal = mk("base", { due: new Date(2026, 6, 6, 20) }); // local, due today → Today base
    const gh = mk("gh", { source: "github", labels: ["review"] }); // synced, promoted
    const filters = [f("reviews", { source: "github", labelsAny: ["review"] }, ["today"])];
    const secs = promotedSections([baseLocal, gh], "today", NOW, filters);
    expect(secs).toHaveLength(1);
    expect(secs[0].filter.id).toBe("reviews");
    expect(secs[0].tasks.map((t) => t.id)).toEqual(["gh"]);
  });

  it("dedups against the base list — a base member never reappears in a section", () => {
    const localDue = mk("ld", { due: new Date(2026, 6, 6, 20) }); // in Today's base AND matches hasDue
    const secs = promotedSections([localDue], "today", NOW, [f("dated", { hasDue: true }, ["today"])]);
    expect(secs).toHaveLength(0); // its only match is already in the base, so the section is empty
  });

  it("a task matching several promoting filters appears only in the first (filter order)", () => {
    const gh = mk("gh", { source: "github", labels: ["review", "urgent"] });
    const filters = [
      f("first", { labelsAny: ["review"] }, ["inbox"]),
      f("second", { labelsAny: ["urgent"] }, ["inbox"]),
    ];
    const secs = promotedSections([gh], "inbox", NOW, filters);
    expect(secs).toHaveLength(1);
    expect(secs[0].filter.id).toBe("first");
    expect(secs[0].tasks.map((t) => t.id)).toEqual(["gh"]);
  });

  it("only promotes filters whose showIn includes the requested kind", () => {
    const gh = mk("gh", { source: "github", labels: ["review"] });
    const filters = [f("reviews", { source: "github" }, ["upcoming"])];
    expect(promotedSections([gh], "today", NOW, filters)).toHaveLength(0);
    expect(promotedSections([gh], "upcoming", NOW, filters)).toHaveLength(1);
  });

  it("countView adds promoted tasks to the base for a promotable list, ignores showIn elsewhere", () => {
    const localDue = mk("ld", { due: new Date(2026, 6, 6, 20) }); // Today base
    const gh = mk("gh", { source: "github", labels: ["review"] }); // promoted into Today
    const filters = [f("reviews", { source: "github" }, ["today"])];
    expect(countView([localDue, gh], { kind: "today" }, NOW, filters)).toBe(2);
    // "all" isn't promotable — only the local task counts.
    expect(countView([localDue, gh], { kind: "all" }, NOW, filters)).toBe(1);
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

describe("default (startup) view (readDefaultView / writeDefaultView)", () => {
  const mem = new Map<string, string>();
  const goodStorage = {
    getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
    setItem: (k: string, v: string) => void mem.set(k, v),
  };

  beforeEach(() => {
    mem.clear();
    vi.stubGlobal("localStorage", goodStorage);
  });

  it("defaults to today when nothing is stored", () => {
    expect(readDefaultView()).toBe("today");
  });

  it("round-trips a written value", () => {
    writeDefaultView("inbox");
    expect(readDefaultView()).toBe("inbox");
  });

  it("falls back to today for a garbage stored value", () => {
    mem.set("taskd-default-view", "not-a-view");
    expect(readDefaultView()).toBe("today");
  });

  it("parseView('') opens to the stored default view", () => {
    writeDefaultView("all");
    expect(parseView("")).toEqual({ kind: "all" });
    // An explicit view param always wins over the stored default.
    expect(parseView("?view=inbox")).toEqual({ kind: "inbox" });
  });
});
