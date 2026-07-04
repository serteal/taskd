import { useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { useSnapshot, useStore } from "../lib/hooks";
import { dayDiff, tsDate } from "../lib/format";
import { matchesView, type View } from "../lib/views";
import { TaskRow } from "./TaskRow";

// Grouping encodes time pressure, nothing else: Overdue → Today → Tomorrow →
// This week → Later → No date. Within a group: soonest due first, then
// newest created.
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

export function visibleTasks(tasks: Iterable<Task>, view: View, now: Date): Task[] {
  const out = [...tasks].filter((t) => matchesView(t, view, now));
  out.sort((a, b) => {
    const ad = tsDate(a.dueTime)?.getTime() ?? Infinity;
    const bd = tsDate(b.dueTime)?.getTime() ?? Infinity;
    if (ad !== bd) return ad - bd;
    const ac = tsDate(a.createTime)?.getTime() ?? 0;
    const bc = tsDate(b.createTime)?.getTime() ?? 0;
    return bc - ac;
  });
  return out;
}

const EMPTY_COPY: Record<string, string> = {
  today: "Nothing due today.",
  inbox: "Inbox zero — no unfiled tasks.",
  upcoming: "Nothing scheduled.",
  all: "No active tasks.",
};

export function TaskList({
  tasks,
  view,
  now,
  selectedId,
  onSelect,
  onOpen,
}: {
  tasks: Task[];
  view: View;
  now: Date;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onOpen: (id: string) => void;
}) {
  const store = useStore();
  const snap = useSnapshot();
  const [leaving, setLeaving] = useState<Set<string>>(new Set());

  if (tasks.length === 0) {
    return (
      <div className="px-3 py-16 text-center text-[13px] text-mute">
        {EMPTY_COPY[view.kind] ?? "Nothing here."}
        <div className="mt-1 font-mono text-[11px] text-faint">
          press <kbd className="rounded border border-line px-1">/</kbd> to add a task
        </div>
      </div>
    );
  }

  const complete = (t: Task) => {
    // Brief strike-through before the row leaves the active set.
    setLeaving((s) => new Set(s).add(t.id));
    setTimeout(() => {
      store.update(t.id, { completed: true, expectedRevision: t.revision }).catch(() => {
        /* store resyncs; toast handled globally */
      });
      setLeaving((s) => {
        const n = new Set(s);
        n.delete(t.id);
        return n;
      });
    }, 250);
  };

  const grouped = new Map<string, Task[]>();
  for (const t of tasks) {
    const g = groupOf(t, now);
    const arr = grouped.get(g);
    if (arr) arr.push(t);
    else grouped.set(g, [t]);
  }

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
          </h2>
          {grouped.get(g)!.map((t) => (
            <TaskRow
              key={t.id}
              task={t}
              now={now}
              selected={t.id === selectedId}
              pulsing={snap.pulses.has(t.id)}
              checked={leaving.has(t.id)}
              onToggle={() => complete(t)}
              onOpen={() => onOpen(t.id)}
              onSelect={() => onSelect(t.id)}
            />
          ))}
        </section>
      ))}
    </div>
  );
}
