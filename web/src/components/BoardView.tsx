import { useMemo, useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { humanDue, tsDate } from "../lib/format";
import { readTaskId, setTaskDrag } from "../lib/dnd";
import { setPriority, setProject } from "../lib/actions";
import { useStore } from "../lib/hooks";
import { Chip } from "./Chip";

export type BoardGroupBy = "priority" | "project";

// Due-date tint, mirroring TaskRow: pressure reads as color.
const toneClass: Record<string, string> = {
  overdue: "text-warn",
  today: "text-accent",
  soon: "text-mute",
  later: "text-faint",
};

const PRIORITY_RE = /^p[1-3]$/;
const PROJECT_PREFIX = "project:";

type Column = { key: string; title: string; tasks: Task[] };

// A task's column key for the given dimension: its single priority label
// ("p1"…"p3"), or its first project label ("project:x"), else "" (the
// no-priority / no-project bucket).
function columnKey(t: Task, groupBy: BoardGroupBy): string {
  if (groupBy === "priority") return t.labels.find((l) => PRIORITY_RE.test(l)) ?? "";
  return t.labels.find((l) => l.startsWith(PROJECT_PREFIX)) ?? "";
}

// The board is just the columns — the app header owns the list/board toggle
// and the group-by control. Cards are drag sources on the shared task-drag
// mime; each column is a drop target that reassigns the dragged task's
// priority (priority board) or project (project board).
export function BoardView({
  tasks,
  groupBy,
  now,
  selectedId,
  onSelect,
  onOpen,
}: {
  tasks: Task[];
  groupBy: BoardGroupBy;
  now: Date;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onOpen: (id: string) => void;
}) {
  const store = useStore();
  // Which column a drag is currently hovering (by key) for the drop highlight.
  const [overKey, setOverKey] = useState<string | null>(null);

  const columns = useMemo<Column[]>(() => {
    let cols: Column[];
    if (groupBy === "priority") {
      cols = [
        { key: "p1", title: "P1", tasks: [] },
        { key: "p2", title: "P2", tasks: [] },
        { key: "p3", title: "P3", tasks: [] },
        { key: "", title: "No priority", tasks: [] },
      ];
    } else {
      const projects = new Set<string>();
      for (const t of tasks) {
        const p = t.labels.find((l) => l.startsWith(PROJECT_PREFIX));
        if (p) projects.add(p);
      }
      cols = [...projects].sort().map((p) => ({
        key: p,
        title: p.slice(PROJECT_PREFIX.length),
        tasks: [],
      }));
      cols.push({ key: "", title: "No project", tasks: [] });
    }
    const byKey = new Map(cols.map((c) => [c.key, c]));
    const fallback = byKey.get("")!;
    for (const t of tasks) (byKey.get(columnKey(t, groupBy)) ?? fallback).tasks.push(t);
    return cols;
  }, [tasks, groupBy]);

  if (tasks.length === 0) {
    return (
      <div className="flex h-full items-center justify-center text-[13px] text-mute">No tasks.</div>
    );
  }

  const card = (t: Task, col: Column) => {
    const due = tsDate(t.dueTime);
    const dueInfo = due ? humanDue(due, now) : undefined;
    // Drop the chip that defines this card's column, to cut repetition.
    const chips = t.labels.filter((l) =>
      groupBy === "priority" ? !PRIORITY_RE.test(l) : l !== col.key,
    );
    const selected = t.id === selectedId;
    return (
      <div
        key={t.id}
        draggable
        onDragStart={(e) => setTaskDrag(e.dataTransfer, t.id)}
        onMouseEnter={() => onSelect(t.id)}
        onClick={() => onOpen(t.id)}
        className={`mb-1.5 cursor-pointer rounded-md border bg-surface p-2 ${
          selected ? "border-accent/50 ring-1 ring-accent/30" : "border-line"
        }`}
      >
        <div className="truncate text-[13px]">{t.title}</div>
        {chips.length > 0 && (
          <div className="mt-1 flex flex-wrap gap-1">
            {chips.map((l) => (
              <Chip key={l} label={l} />
            ))}
          </div>
        )}
        {dueInfo && (
          <div className={`mt-1 font-mono text-[11px] ${toneClass[dueInfo.tone]}`}>{dueInfo.text}</div>
        )}
      </div>
    );
  };

  return (
    <div data-testid="board" className="flex h-full overflow-x-auto">
      {columns.map((col) => (
        <div
          key={col.key || "__none__"}
          data-testid={`board-col-${col.key || "none"}`}
          onDragOver={(e) => {
            e.preventDefault();
            setOverKey(col.key);
          }}
          onDragLeave={() => setOverKey((k) => (k === col.key ? null : k))}
          onDrop={(e) => {
            e.preventDefault();
            setOverKey(null);
            const id = readTaskId(e.dataTransfer);
            if (!id) return;
            const task = tasks.find((t) => t.id === id);
            if (!task) return;
            if (groupBy === "priority") setPriority(store, task, col.key || null);
            else setProject(store, task, col.key || null);
          }}
          className={`flex w-[240px] min-w-[240px] shrink-0 flex-col border-r border-line/60 ${
            overKey === col.key ? "bg-accent/5" : ""
          }`}
        >
          <div className="flex-1 overflow-y-auto">
            <div className="sticky top-0 z-10 flex items-baseline gap-2 border-b border-line bg-paper px-3 pb-1 pt-2 font-mono text-[10.5px] font-medium uppercase tracking-[0.14em] text-mute">
              {col.title}
              <span className="text-faint">{col.tasks.length}</span>
            </div>
            <div className="px-1.5 pt-1.5">{col.tasks.map((t) => card(t, col))}</div>
          </div>
        </div>
      ))}
    </div>
  );
}
