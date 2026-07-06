import { describe, expect, it } from "vitest";
import type { Task } from "../gen/task/task_pb";
import {
  buildStaticCommands,
  scoreMatch,
  searchTaskCommands,
  type CommandContext,
} from "./commands";
import { savedViews } from "./savedviews";
import { savedFilters } from "./filters";

const task = (id: string, title: string, extra: Partial<Task> = {}): Task =>
  ({ id, title, notes: "", labels: [], source: "", ...extra }) as unknown as Task;

const ctx = (over: Partial<CommandContext> = {}): CommandContext => ({
  tasks: [task("1", "Buy milk", { labels: ["project:home"] })],
  store: {} as CommandContext["store"],
  selectedId: null,
  navigate: () => {},
  setSort: () => {},
  toggleTheme: () => {},
  panels: [{ id: "day", title: "Today" }],
  togglePanel: () => {},
  openTask: () => {},
  openAdd: () => {},
  saveCurrentView: () => {},
  applySaved: () => {},
  extCommands: [],
  now: new Date(2026, 6, 6),
  ...over,
});

describe("scoreMatch", () => {
  it("returns -1 when any query word is absent", () => {
    expect(scoreMatch("Go to Today", "zzz")).toBe(-1);
    expect(scoreMatch("Go to Today", "go zzz")).toBe(-1);
  });
  it("scores earlier matches lower (better)", () => {
    expect(scoreMatch("def", "def")).toBe(0);
    expect(scoreMatch("abc def", "def")).toBeGreaterThan(0);
    expect(scoreMatch("def", "def")).toBeLessThan(scoreMatch("abc def", "def"));
  });
  it("is case-insensitive and requires all words", () => {
    expect(scoreMatch("Sort: Manual", "manual")).toBeGreaterThanOrEqual(0);
    expect(scoreMatch("Sort: Manual", "sort manual")).toBeGreaterThanOrEqual(0);
  });
});

describe("buildStaticCommands", () => {
  it("includes global actions, sorts, navigation and panel toggles", () => {
    const titles = buildStaticCommands(ctx()).map((c) => c.title);
    expect(titles).toContain("New task");
    expect(titles).toContain("Toggle theme");
    expect(titles).toContain("Sort: Manual");
    expect(titles).toContain("Go to Today");
    expect(titles).toContain("Toggle today panel");
    expect(titles).toContain("Go to project home"); // from the task's project label
  });

  it("adds selected-task actions only when a task is selected", () => {
    expect(buildStaticCommands(ctx()).some((c) => c.group === "Selected task")).toBe(false);
    const withSel = buildStaticCommands(ctx({ selectedId: "1" }));
    const sel = withSel.filter((c) => c.group === "Selected task").map((c) => c.title);
    expect(sel.some((t) => t.startsWith("Complete"))).toBe(true);
    expect(sel.some((t) => t.startsWith("Delete"))).toBe(true);
  });

  it("surfaces extension commands", () => {
    const withExt = buildStaticCommands(
      ctx({ extCommands: [{ id: "x", title: "Do the thing", run: () => {} }] }),
    );
    expect(withExt.map((c) => c.title)).toContain("Do the thing");
  });

  it("includes 'Go to' commands for saved views and saved filters", () => {
    savedViews.add({ name: "My View", view: { kind: "today" }, sort: "smart", board: false, groupBy: "priority" });
    const view = savedViews.getSnapshot().find((v) => v.name === "My View")!;
    const filter = savedFilters.add({ name: "My Filter", predicate: { source: "github" } });
    try {
      let applied: unknown;
      let navigated: unknown;
      const commands = buildStaticCommands(
        ctx({ applySaved: (v) => (applied = v), navigate: (v) => (navigated = v) }),
      );

      const viewCmd = commands.find((c) => c.title === "Go to My View");
      expect(viewCmd, "saved view should have a palette command").toBeTruthy();
      viewCmd!.run();
      expect(applied).toEqual(view);

      const filterCmd = commands.find((c) => c.title === "Go to My Filter");
      expect(filterCmd, "saved filter should have a palette command").toBeTruthy();
      filterCmd!.run();
      expect(navigated).toEqual({ kind: "filter", id: filter.id });
    } finally {
      savedViews.remove(view.id);
      savedFilters.remove(filter.id);
    }
  });
});

describe("searchTaskCommands", () => {
  it("is empty without a query and matches titles otherwise", () => {
    expect(searchTaskCommands(ctx(), "")).toHaveLength(0);
    const hits = searchTaskCommands(ctx(), "milk");
    expect(hits.map((c) => c.title)).toEqual(["Buy milk"]);
  });
});
