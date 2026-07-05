import { useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { useSnapshot, useStore } from "../lib/hooks";
import { dayDiff, tsDate } from "../lib/format";
import { matchesView, type SortMode, type View } from "../lib/views";
import { computeReorder, taskOrder } from "../lib/reorder";
import { readTaskId } from "../lib/dnd";
import { completeTask, rescheduleMany } from "../lib/actions";
import { TaskRow } from "./TaskRow";
import { Popover } from "./Popover";
import { ScheduleMenu } from "./pickers";

// Grouping encodes time pressure, nothing else: Overdue → Today → Tomorrow →
// This week → Later → No date. Within a group: soonest due first, then
// newest created. (Manual sort renders flat instead — grouping and manual
// order are mutually exclusive.)
const GROUPS = ["Overdue", "Today", "Tomorrow", "This week", "Later", "No date"] as const;

function groupOf(t: Task, now: Date): (typeof GROUPS)[number] {
  const due = tsDate(t.dueTime);
  if (!due) return "No date";
  const d = dayDiff(due, now);
  if (d < 0) return "Overdue";
  if (d === 0) return "Today";
  if (d === 1) return "Tomorrow";
  if (d < 7) return "This week";
  return "Later";
}

function smartCmp(a: Task, b: Task): number {
  const ad = tsDate(a.dueTime)?.getTime() ?? Infinity;
  const bd = tsDate(b.dueTime)?.getTime() ?? Infinity;
  if (ad !== bd) return ad - bd;
  const ac = tsDate(a.createTime)?.getTime() ?? 0;
  const bc = tsDate(b.createTime)?.getTime() ?? 0;
  return bc - ac;
}

export function visibleTasks(
  tasks: Iterable<Task>,
  view: View,
  now: Date,
  sort: SortMode = "smart",
): Task[] {
  const out = [...tasks].filter((t) => matchesView(t, view, now));
  switch (sort) {
    case "manual":
      out.sort((a, b) => {
        const ao = taskOrder(a);
        const bo = taskOrder(b);
        if (ao !== undefined && bo !== undefined) return ao - bo;
        if (ao !== undefined) return -1; // ordered rows before un-ordered
        if (bo !== undefined) return 1;
        return smartCmp(a, b);
      });
      break;
    case "created":
      out.sort(
        (a, b) => (tsDate(b.createTime)?.getTime() ?? 0) - (tsDate(a.createTime)?.getTime() ?? 0),
      );
      break;
    case "title":
      out.sort((a, b) => a.title.localeCompare(b.title));
      break;
    default:
      out.sort(smartCmp);
  }
  return out;
}

const EMPTY_COPY: Record<string, string> = {
  today: "Nothing due today.",
  inbox: "Inbox zero — no unfiled tasks.",
  upcoming: "Nothing scheduled.",
  all: "No active tasks.",
  source: "Nothing synced from this source yet.",
  filter: "No tasks match this filter.",
};

export function TaskList({
  tasks,
  view,
  now,
  sort,
  selectedId,
  bulkSelected,
  editingId,
  onSelect,
  onActivate,
  onManualReorder,
  onStartEdit,
  onRename,
  onEndEdit,
  onContextMenu,
}: {
  tasks: Task[];
  view: View;
  now: Date;
  sort: SortMode;
  selectedId: string | null;
  bulkSelected: Set<string>;
  editingId: string | null;
  onSelect: (id: string) => void;
  onActivate: (id: string, mods: { meta: boolean; shift: boolean }) => void;
  /** Called after a reorder that happened while not already in manual sort. */
  onManualReorder: () => void;
  onStartEdit: (id: string) => void;
  onRename: (id: string, title: string) => void;
  onEndEdit: () => void;
  /** Open a row's context menu at the cursor. */
  onContextMenu?: (id: string, x: number, y: number) => void;
}) {
  const store = useStore();
  const snap = useSnapshot();
  const [leaving, setLeaving] = useState<Set<string>>(new Set());
  // The row a drag is hovering over and which edge (for the insert indicator).
  const [dropAt, setDropAt] = useState<{ id: string; below: boolean } | null>(null);

  if (tasks.length === 0) {
    return (
      <div className="px-3 py-16 text-center text-[13px] text-mute">
        {EMPTY_COPY[view.kind] ?? "Nothing here."}
        <div className="mt-1 font-mono text-[11px] text-faint">
          press <kbd className="rounded border border-line px-1">q</kbd> to add a task
        </div>
      </div>
    );
  }

  const complete = (t: Task) => {
    // Brief strike-through before the row leaves the active set.
    setLeaving((s) => new Set(s).add(t.id));
    setTimeout(() => {
      completeTask(store, t); // optimistic + undo toast
      setLeaving((s) => {
        const n = new Set(s);
        n.delete(t.id);
        return n;
      });
    }, 250);
  };

  // Reorder within the current display sequence. Renumbers user_data.order to
  // the resulting order (only changed rows are written); starting from a
  // non-manual sort, this also switches the list to manual so the new order
  // sticks. `seq` is the order the user currently SEES (grouped-flattened or
  // the manual list) so the move lands where they dropped it.
  const applyReorder = (seq: Task[], draggedId: string, targetId: string, below: boolean) => {
    if (draggedId === targetId) return;
    const without = seq.filter((t) => t.id !== draggedId);
    let idx = without.findIndex((t) => t.id === targetId);
    if (idx === -1) return;
    if (below) idx += 1;
    for (const u of computeReorder(seq, draggedId, idx)) {
      const t = seq.find((x) => x.id === u.id);
      if (!t) continue;
      store
        .update(u.id, {
          userData: { ...(t.userData ?? {}), order: u.order },
          expectedRevision: t.revision,
        })
        .catch(() => {});
    }
    if (sort !== "manual") onManualReorder();
  };

  const row = (t: Task) => (
    <TaskRow
      task={t}
      now={now}
      selected={t.id === selectedId}
      bulkSelected={bulkSelected.has(t.id)}
      pulsing={snap.pulses.has(t.id)}
      checked={leaving.has(t.id)}
      editing={t.id === editingId}
      onToggle={() => complete(t)}
      onActivate={(mods) => onActivate(t.id, mods)}
      onSelect={() => onSelect(t.id)}
      onStartEdit={() => onStartEdit(t.id)}
      onRename={(title) => onRename(t.id, title)}
      onEndEdit={onEndEdit}
      onContextMenu={onContextMenu ? (x, y) => onContextMenu(t.id, x, y) : undefined}
    />
  );

  // A row wrapped as a reorder drop target. `seq` is the full display order.
  const dropRow = (t: Task, seq: Task[]) => (
    <div
      key={t.id}
      onDragOver={(e) => {
        e.preventDefault();
        const r = e.currentTarget.getBoundingClientRect();
        setDropAt({ id: t.id, below: e.clientY > r.top + r.height / 2 });
      }}
      onDragLeave={() => setDropAt((d) => (d?.id === t.id ? null : d))}
      onDrop={(e) => {
        e.preventDefault();
        const id = readTaskId(e.dataTransfer);
        const below = dropAt?.id === t.id ? dropAt.below : false;
        setDropAt(null);
        if (id) applyReorder(seq, id, t.id, below);
      }}
      className={
        dropAt?.id === t.id
          ? dropAt.below
            ? "border-b-2 border-b-accent"
            : "border-t-2 border-t-accent"
          : ""
      }
    >
      {row(t)}
    </div>
  );

  // Manual: one flat, reorderable list.
  if (sort === "manual") {
    return <div>{tasks.map((t) => dropRow(t, tasks))}</div>;
  }

  // Otherwise: grouped by time pressure, but still reorderable — dropping a
  // row onto another switches the list to manual (applyReorder does it).
  const grouped = new Map<string, Task[]>();
  for (const t of tasks) {
    const g = groupOf(t, now);
    const arr = grouped.get(g);
    if (arr) arr.push(t);
    else grouped.set(g, [t]);
  }
  const seq = GROUPS.flatMap((g) => grouped.get(g) ?? []);

  return (
    <div>
      {GROUPS.filter((g) => grouped.has(g)).map((g) => (
        <section key={g}>
          <h2
            className={`sticky top-0 z-10 flex items-baseline gap-2 border-b border-line bg-paper px-3 pb-1 pt-3 font-mono text-[10.5px] font-medium uppercase tracking-[0.14em] ${
              g === "Overdue" ? "text-warn" : "text-mute"
            }`}
          >
            {g}
            <span className="text-faint">{grouped.get(g)!.length}</span>
            {/* Overdue gets a one-click "clear my overdue": batch-reschedule
                every overdue task in view to a chosen day. */}
            {g === "Overdue" && (
              <span className="ml-auto self-center normal-case tracking-normal">
                <Popover
                  align="right"
                  trigger={({ toggle }) => (
                    <button
                      onClick={toggle}
                      aria-label="Reschedule overdue tasks"
                      className="rounded border border-warn/40 px-1.5 py-px text-[10px] font-medium text-warn hover:bg-warn/10"
                    >
                      Reschedule
                    </button>
                  )}
                >
                  {(close) => (
                    <ScheduleMenu
                      now={now}
                      onChange={(d) => rescheduleMany(store, grouped.get("Overdue") ?? [], d)}
                      close={close}
                    />
                  )}
                </Popover>
              </span>
            )}
          </h2>
          {grouped.get(g)!.map((t) => dropRow(t, seq))}
        </section>
      ))}
    </div>
  );
}
