import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { TaskSchema, type Task } from "../gen/task/task_pb";
import { flattenSections, nestSections, subtaskCounts } from "./nest";

const NOW = new Date(2026, 6, 6, 12, 0, 0); // Mon 2026-07-06 local
const day = (n: number) => new Date(2026, 6, 6 + n, 12, 0, 0);

let seq = 0;
function mkTask(over: Partial<Task> & { id: string }): Task {
  // createTime ascending by construction order, so smart-sort ties are stable.
  return Object.assign(
    create(TaskSchema, { title: over.id, revision: 1n, createTime: timestampFromDate(day(seq++)) }),
    over,
  );
}

const titleMap = (tasks: Task[]) => (id: string) => tasks.find((t) => t.id === id)?.title;

function opts(tasks: Task[], extra?: { grouped?: boolean; collapsed?: Set<string> }) {
  return {
    grouped: extra?.grouped ?? true,
    now: NOW,
    collapsed: extra?.collapsed ?? new Set<string>(),
    titleOf: titleMap(tasks),
  };
}

describe("nestSections — same-group nesting", () => {
  it("nests a child directly under its parent, indented, when both share a group", () => {
    const parent = mkTask({ id: "parent", title: "Parent" });
    const child = mkTask({ id: "child", title: "Child", parentId: "parent" });
    const tasks = [parent, child]; // both undated → "No date"

    const secs = nestSections(tasks, opts(tasks));
    expect(secs).toHaveLength(1);
    expect(secs[0].header).toBe("No date");
    expect(secs[0].rows.map((r) => [r.task.id, r.depth])).toEqual([
      ["parent", 0],
      ["child", 1],
    ]);
    expect(secs[0].rows[0].collapsible).toBe(true);
    expect(secs[0].rows[1].breadcrumb).toBeUndefined();
    // Keyboard order matches the visual order exactly.
    expect(flattenSections(secs).map((t) => t.id)).toEqual(["parent", "child"]);
  });

  it("keeps the child under the parent even when the child sorts first", () => {
    // Parent undated, child undated but created later — smart-sort would place
    // the newer child before the parent; nesting must still put parent first.
    const parent = mkTask({ id: "parent", title: "Parent" });
    const child = mkTask({ id: "child", title: "Child", parentId: "parent" });
    const tasks = [child, parent]; // pre-sorted with child first

    const secs = nestSections(tasks, opts(tasks));
    expect(secs[0].rows.map((r) => r.task.id)).toEqual(["parent", "child"]);
  });
});

describe("nestSections — hierarchy beats time-grouping", () => {
  it("nests a child under a parent in ANOTHER group, moving it to the parent's group", () => {
    const parent = mkTask({ id: "parent", title: "Parent", dueTime: timestampFromDate(day(0)) }); // Today
    const child = mkTask({
      id: "child",
      title: "Child",
      parentId: "parent",
      dueTime: timestampFromDate(day(1)), // Tomorrow — but the visible parent wins
    });
    const tasks = [parent, child];

    const secs = nestSections(tasks, opts(tasks));
    // The child left Tomorrow entirely; the emptied group disappears.
    expect(secs.map((s) => s.header)).toEqual(["Today"]);
    const today = secs[0];
    expect(today.rows.map((r) => [r.task.id, r.depth])).toEqual([
      ["parent", 0],
      ["child", 1],
    ]);
    expect(today.rows[0].collapsible).toBe(true);
    expect(today.rows[1].breadcrumb).toBeUndefined();
    // The parent's group count grew to include the moved child.
    expect(today.count).toBe(2);
    expect(flattenSections(secs).map((t) => t.id)).toEqual(["parent", "child"]);
  });

  it("the headline case: undated children attach under a dated parent, no scatter", () => {
    const parent = mkTask({ id: "parent", title: "Parent", dueTime: timestampFromDate(day(4)) }); // This week
    const kid1 = mkTask({ id: "kid1", title: "Kid 1", parentId: "parent" }); // undated
    const kid2 = mkTask({ id: "kid2", title: "Kid 2", parentId: "parent" }); // undated
    const tasks = [parent, kid1, kid2];

    const secs = nestSections(tasks, opts(tasks));
    // No "No date" section left over — both children moved under the parent.
    expect(secs.map((s) => s.header)).toEqual(["This week"]);
    expect(secs[0].count).toBe(3);
    expect(secs[0].rows.map((r) => [r.task.id, r.depth])).toEqual([
      ["parent", 0],
      ["kid1", 1],
      ["kid2", 1],
    ]);
  });

  it("a moved child shrinks its former group's count without emptying it", () => {
    const parent = mkTask({ id: "parent", title: "Parent", dueTime: timestampFromDate(day(0)) }); // Today
    const child = mkTask({
      id: "child",
      title: "Child",
      parentId: "parent",
      dueTime: timestampFromDate(day(1)), // Tomorrow, nests into Today
    });
    const other = mkTask({ id: "other", title: "Other", dueTime: timestampFromDate(day(1)) }); // Tomorrow
    const tasks = [parent, child, other];

    const secs = nestSections(tasks, opts(tasks));
    const today = secs.find((s) => s.header === "Today")!;
    const tomorrow = secs.find((s) => s.header === "Tomorrow")!;
    expect(today.count).toBe(2); // parent + moved-in child
    expect(tomorrow.count).toBe(1); // only the unrelated task remains
    expect(tomorrow.rows.map((r) => r.task.id)).toEqual(["other"]);
  });

  it("shows a breadcrumb ONLY when the parent is absent from the list", () => {
    // Only the child is in the working set; the parent lives elsewhere in the
    // replica, so titleOf still resolves the breadcrumb.
    const child = mkTask({ id: "child", title: "Child", parentId: "parent" });
    const titleOf = (id: string) => (id === "parent" ? "Absent Parent" : undefined);

    const secs = nestSections([child], { grouped: true, now: NOW, collapsed: new Set(), titleOf });
    expect(secs[0].rows[0].depth).toBe(0);
    expect(secs[0].rows[0].breadcrumb).toBe("Absent Parent");
  });
});

describe("nestSections — collapse exclusion", () => {
  it("drops a collapsed parent's children from the rows and the flat order", () => {
    const parent = mkTask({ id: "parent", title: "Parent" });
    const child = mkTask({ id: "child", title: "Child", parentId: "parent" });
    const tasks = [parent, child];

    const secs = nestSections(tasks, opts(tasks, { collapsed: new Set(["parent"]) }));
    expect(secs[0].rows.map((r) => r.task.id)).toEqual(["parent"]);
    // The parent still advertises the toggle even while collapsed.
    expect(secs[0].rows[0].collapsible).toBe(true);
    // The count reflects the true group size, not the collapsed row count.
    expect(secs[0].count).toBe(2);
    expect(flattenSections(secs).map((t) => t.id)).toEqual(["parent"]);
  });

  it("collapse also hides a cross-group child that nested in, keeping it counted", () => {
    const parent = mkTask({ id: "parent", title: "Parent", dueTime: timestampFromDate(day(0)) }); // Today
    const child = mkTask({
      id: "child",
      title: "Child",
      parentId: "parent",
      dueTime: timestampFromDate(day(1)), // Tomorrow, nests into Today
    });
    const tasks = [parent, child];

    const secs = nestSections(tasks, opts(tasks, { collapsed: new Set(["parent"]) }));
    expect(secs.map((s) => s.header)).toEqual(["Today"]);
    expect(secs[0].rows.map((r) => r.task.id)).toEqual(["parent"]);
    expect(secs[0].count).toBe(2); // collapsed child still counted
    expect(flattenSections(secs).map((t) => t.id)).toEqual(["parent"]);
  });
});

describe("nestSections — flat (board/manual) bypass", () => {
  it("returns one flat section with no nesting when grouped is false", () => {
    const parent = mkTask({ id: "parent", title: "Parent" });
    const child = mkTask({ id: "child", title: "Child", parentId: "parent" });
    const tasks = [parent, child];

    const secs = nestSections(tasks, opts(tasks, { grouped: false }));
    expect(secs).toHaveLength(1);
    expect(secs[0].header).toBeNull();
    expect(secs[0].rows.every((r) => r.depth === 0)).toBe(true);
    expect(secs[0].rows.every((r) => r.breadcrumb === undefined)).toBe(true);
    expect(flattenSections(secs).map((t) => t.id)).toEqual(["parent", "child"]);
  });

  it("stays flat even across groups — board/manual never nest", () => {
    // Same shape as the hierarchy-beats-grouping case, but grouped:false: the
    // child must keep its own sorted position, undecorated.
    const parent = mkTask({ id: "parent", title: "Parent", dueTime: timestampFromDate(day(0)) });
    const child = mkTask({
      id: "child",
      title: "Child",
      parentId: "parent",
      dueTime: timestampFromDate(day(1)),
    });
    const tasks = [child, parent]; // child sorts first; flat keeps it there

    const secs = nestSections(tasks, opts(tasks, { grouped: false }));
    expect(secs).toHaveLength(1);
    expect(secs[0].rows.map((r) => [r.task.id, r.depth])).toEqual([
      ["child", 0],
      ["parent", 0],
    ]);
    expect(secs[0].rows.every((r) => r.breadcrumb === undefined && !r.collapsible)).toBe(true);
  });

  it("returns no sections for an empty set", () => {
    expect(nestSections([], opts([]))).toEqual([]);
    expect(nestSections([], opts([], { grouped: false }))).toEqual([]);
  });
});

describe("subtaskCounts", () => {
  it("counts open children per parent id", () => {
    const tasks = [
      mkTask({ id: "p" }),
      mkTask({ id: "a", parentId: "p" }),
      mkTask({ id: "b", parentId: "p" }),
      mkTask({ id: "c", parentId: "other" }),
    ];
    const counts = subtaskCounts(tasks);
    expect(counts.get("p")).toBe(2);
    expect(counts.get("other")).toBe(1);
    expect(counts.has("a")).toBe(false);
  });
});
