import type { Task } from "../gen/task/task_pb";
import { dayDiff, tsDate } from "./format";

// Turns a view's sorted working set into the flattened display order, with the
// one level of subtask nesting the data model allows (Task.parentId, capped at
// depth 1). The SAME flattened order drives both the visual list (TaskList) and
// keyboard traversal (App's `tasks`), so j/k always matches what's on screen.
//
// Hierarchy beats time-grouping: when a child's parent is visible ANYWHERE in
// the list, the child nests directly under it (indented, depth 1) in the
// PARENT'S group — leaving its own time-pressure group, whose header count
// shrinks accordingly (a group left empty disappears). Only a child whose
// parent is absent from the view renders flat at its own sorted position, with
// a "↳ parent" breadcrumb instead. Collapsing a parent drops its nested
// children from the order entirely (and so from keyboard traversal). Board and
// manual/flat lists bypass nesting.

// Grouping encodes time pressure, nothing else: Overdue → Today → Tomorrow →
// This week → Later → No date. (Moved here from TaskList so the nesting and the
// list rendering agree on group membership.)
export const GROUPS = ["Overdue", "Today", "Tomorrow", "This week", "Later", "No date"] as const;
export type Group = (typeof GROUPS)[number];

export function groupOf(t: Task, now: Date): Group {
  const due = tsDate(t.dueTime);
  if (!due) return "No date";
  const d = dayDiff(due, now);
  if (d < 0) return "Overdue";
  if (d === 0) return "Today";
  if (d === 1) return "Tomorrow";
  if (d < 7) return "This week";
  return "Later";
}

export interface NestRow {
  task: Task;
  depth: 0 | 1;
  /** A flat child whose parent isn't nesting it here shows this breadcrumb. */
  breadcrumb?: string;
  /** True when this parent has nested children in this section (collapse toggle). */
  collapsible: boolean;
}

export interface NestSection {
  key: string;
  /** null header = a single ungrouped (flat) section. */
  header: Group | null;
  /** Total tasks in the group — top-level rows plus the children nested under
   *  them (wherever those children's own dates fall), before collapse hides
   *  any. */
  count: number;
  rows: NestRow[];
}

export interface NestOptions {
  /** Group by time pressure; false yields one flat section with no nesting. */
  grouped: boolean;
  now: Date;
  collapsed: ReadonlySet<string>;
  /** Resolve any task's title (over the full replica) for breadcrumbs. */
  titleOf: (id: string) => string | undefined;
}

/** Build the display sections for a working set (already sorted). Grouped lists
 *  nest children under their in-list parents (in the parent's group); flat
 *  lists (grouped:false) are a straight passthrough. */
export function nestSections(tasks: Task[], opts: NestOptions): NestSection[] {
  const { grouped, now, collapsed, titleOf } = opts;

  if (!grouped) {
    // Flat bypass: one section, every row at depth 0, no nesting.
    return tasks.length === 0
      ? []
      : [
          {
            key: "all",
            header: null,
            count: tasks.length,
            rows: tasks.map((t) => ({ task: t, depth: 0 as const, collapsible: false })),
          },
        ];
  }

  const inSet = new Set<string>();
  for (const t of tasks) inSet.add(t.id);

  // Hierarchy beats time-grouping: a child nests whenever its parent is
  // visible anywhere in this list — the child joins the parent's group.
  const nests = (t: Task): boolean => t.parentId !== "" && inSet.has(t.parentId);

  // Nested children keyed by parent id, preserving the sorted order.
  const childrenByParent = new Map<string, Task[]>();
  for (const t of tasks) {
    if (!nests(t)) continue;
    const arr = childrenByParent.get(t.parentId);
    if (arr) arr.push(t);
    else childrenByParent.set(t.parentId, [t]);
  }

  const sections: NestSection[] = [];
  for (const g of GROUPS) {
    // Group membership after the moves: the top-level rows whose OWN group is
    // g, each bringing all its visible children along (a group whose members
    // all nested elsewhere simply disappears).
    const tops = tasks.filter((t) => !nests(t) && groupOf(t, now) === g);
    if (tops.length === 0) continue;

    let count = 0;
    const rows: NestRow[] = [];
    for (const t of tops) {
      const kids = childrenByParent.get(t.id) ?? [];
      // A top-level row with a parentId has a parent NOT in this list — the
      // only case that still renders the flat "↳ parent" breadcrumb.
      const breadcrumb = t.parentId !== "" ? titleOf(t.parentId) : undefined;
      rows.push({ task: t, depth: 0, breadcrumb, collapsible: kids.length > 0 });
      count += 1 + kids.length;
      if (kids.length > 0 && !collapsed.has(t.id)) {
        for (const k of kids) rows.push({ task: k, depth: 1, collapsible: false });
      }
    }
    sections.push({ key: g, header: g, count, rows });
  }
  return sections;
}

/** The flat task order across all sections — what keyboard traversal consumes. */
export function flattenSections(sections: NestSection[]): Task[] {
  return sections.flatMap((s) => s.rows.map((r) => r.task));
}

/** Open-subtask counts by parent id, over an active task set (the replica).
 *  Drives the "N subtasks" chip; counts every open child regardless of view. */
export function subtaskCounts(tasks: Iterable<Task>): Map<string, number> {
  const counts = new Map<string, number>();
  for (const t of tasks) {
    if (t.parentId !== "") counts.set(t.parentId, (counts.get(t.parentId) ?? 0) + 1);
  }
  return counts;
}
