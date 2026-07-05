import { beforeEach, describe, expect, it, vi } from "vitest";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import type { Task } from "../gen/task/task_pb";
import { matchesFilter, savedFilters, type FilterPredicate } from "./filters";

const NOW = new Date(2026, 6, 6, 12, 0, 0); // Mon 2026-07-06

const task = (
  p: Partial<{ title: string; notes: string; labels: string[]; due: Date; source: string; completed: Date }>,
): Task =>
  ({
    id: "t",
    title: p.title ?? "",
    notes: p.notes ?? "",
    labels: p.labels ?? [],
    source: p.source ?? "",
    dueTime: p.due ? timestampFromDate(p.due) : undefined,
    completedTime: p.completed ? timestampFromDate(p.completed) : undefined,
  }) as unknown as Task;

const match = (p: FilterPredicate, t: Task) => matchesFilter(p, t, NOW);

describe("matchesFilter", () => {
  it("an empty predicate matches any active task", () => {
    expect(match({}, task({}))).toBe(true);
    expect(match({}, task({ source: "github" }))).toBe(true);
  });

  it("source constrains to an exact source when set", () => {
    expect(match({ source: "github" }, task({ source: "github" }))).toBe(true);
    expect(match({ source: "github" }, task({ source: "gcal:x" }))).toBe(false);
    // "" targets local tasks explicitly.
    expect(match({ source: "" }, task({ source: "" }))).toBe(true);
    expect(match({ source: "" }, task({ source: "github" }))).toBe(false);
  });

  it("labelsAll requires every label; labelsAny requires one", () => {
    expect(match({ labelsAll: ["a", "b"] }, task({ labels: ["a", "b", "c"] }))).toBe(true);
    expect(match({ labelsAll: ["a", "b"] }, task({ labels: ["a"] }))).toBe(false);
    expect(match({ labelsAny: ["x", "y"] }, task({ labels: ["y"] }))).toBe(true);
    expect(match({ labelsAny: ["x", "y"] }, task({ labels: ["z"] }))).toBe(false);
    // Empty arrays impose no constraint.
    expect(match({ labelsAll: [], labelsAny: [] }, task({}))).toBe(true);
  });

  it("text is a case-insensitive substring over title + notes", () => {
    expect(match({ text: "ship" }, task({ title: "Ship the release" }))).toBe(true);
    expect(match({ text: "SHIP" }, task({ title: "ship it" }))).toBe(true);
    expect(match({ text: "urgent" }, task({ notes: "This is Urgent" }))).toBe(true);
    expect(match({ text: "missing" }, task({ title: "nope" }))).toBe(false);
    expect(match({ text: "   " }, task({ title: "anything" }))).toBe(true); // blank = no constraint
  });

  it("hasDue requires a due date", () => {
    expect(match({ hasDue: true }, task({ due: new Date(2026, 6, 9) }))).toBe(true);
    expect(match({ hasDue: true }, task({}))).toBe(false);
    expect(match({ hasDue: false }, task({}))).toBe(true);
  });

  it("dueWithinDays includes overdue and up to N days out", () => {
    expect(match({ dueWithinDays: 3 }, task({ due: new Date(2026, 6, 6) }))).toBe(true); // today
    expect(match({ dueWithinDays: 3 }, task({ due: new Date(2026, 6, 9) }))).toBe(true); // +3
    expect(match({ dueWithinDays: 3 }, task({ due: new Date(2026, 6, 10) }))).toBe(false); // +4
    expect(match({ dueWithinDays: 3 }, task({ due: new Date(2026, 6, 1) }))).toBe(true); // overdue
    expect(match({ dueWithinDays: 3 }, task({}))).toBe(false); // undated
  });

  it("excludes completed tasks unless includeCompleted", () => {
    const done = task({ completed: new Date(2026, 6, 5) });
    expect(match({}, done)).toBe(false);
    expect(match({ includeCompleted: true }, done)).toBe(true);
  });

  it("AND-s every constraint together (a 'Reviews' filter)", () => {
    const p: FilterPredicate = { source: "github", labelsAny: ["review"] };
    expect(match(p, task({ source: "github", labels: ["review"] }))).toBe(true);
    expect(match(p, task({ source: "github", labels: ["bug"] }))).toBe(false);
    expect(match(p, task({ source: "", labels: ["review"] }))).toBe(false);
  });
});

describe("savedFilters store", () => {
  const mem = new Map<string, string>();
  beforeEach(() => {
    mem.clear();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
      setItem: (k: string, v: string) => void mem.set(k, v),
      removeItem: (k: string) => void mem.delete(k),
    });
    // Reset the singleton to a known-empty state between tests.
    for (const f of savedFilters.getSnapshot()) savedFilters.remove(f.id);
  });

  it("adds, looks up, updates, and removes a filter", () => {
    const created = savedFilters.add({ name: "Reviews", predicate: { source: "github" } });
    expect(created.id).toBeTruthy();
    expect(savedFilters.get(created.id)?.name).toBe("Reviews");
    expect(savedFilters.getSnapshot()).toHaveLength(1);

    savedFilters.update(created.id, { name: "Code reviews" });
    expect(savedFilters.get(created.id)?.name).toBe("Code reviews");

    savedFilters.remove(created.id);
    expect(savedFilters.get(created.id)).toBeUndefined();
    expect(savedFilters.getSnapshot()).toHaveLength(0);
  });

  it("persists to localStorage under taskd-filters", () => {
    savedFilters.add({ name: "Persisted", predicate: { hasDue: true } });
    expect(mem.get("taskd-filters")).toContain("Persisted");
  });

  it("notifies subscribers on change", () => {
    const seen: number[] = [];
    const unsub = savedFilters.subscribe(() => seen.push(savedFilters.getSnapshot().length));
    const f = savedFilters.add({ name: "S", predicate: {} });
    savedFilters.remove(f.id);
    unsub();
    expect(seen).toEqual([1, 0]);
  });
});
