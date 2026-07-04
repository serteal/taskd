import type { Task } from "../gen/task/task_pb";
import { humanDue, tsDate } from "../lib/format";
import { Chip } from "./Chip";

const toneClass: Record<string, string> = {
  overdue: "text-warn",
  today: "text-accent",
  soon: "text-mute",
  later: "text-faint",
};

export function TaskRow({
  task,
  now,
  selected,
  pulsing,
  checked,
  onToggle,
  onOpen,
  onSelect,
}: {
  task: Task;
  now: Date;
  selected: boolean;
  pulsing: boolean;
  /** Optimistic strike-through while the completion round-trips. */
  checked: boolean;
  onToggle: () => void;
  onOpen: () => void;
  onSelect: () => void;
}) {
  const due = tsDate(task.dueTime);
  const dueInfo = due ? humanDue(due, now) : undefined;

  return (
    <div
      data-task-row={task.id}
      onMouseEnter={onSelect}
      onClick={onOpen}
      className={`group flex cursor-pointer items-center gap-2.5 border-b border-line/70 px-3 py-[7px] ${
        selected ? "bg-ink/[.045] dark:bg-ink/[.07]" : ""
      } ${pulsing ? "pulse" : ""}`}
    >
      <button
        aria-label={checked ? `Reopen ${task.title}` : `Complete ${task.title}`}
        onClick={(e) => {
          e.stopPropagation();
          onToggle();
        }}
        className={`flex h-[16px] w-[16px] shrink-0 items-center justify-center rounded-full border transition-colors ${
          checked
            ? "border-accent bg-accent text-white"
            : "border-mute/60 text-transparent hover:border-accent hover:text-accent/60"
        }`}
      >
        <svg width="9" height="9" viewBox="0 0 10 10" fill="none" aria-hidden>
          <path d="M1.5 5.5 4 8l4.5-6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
        </svg>
      </button>

      <span
        className={`min-w-0 flex-1 truncate text-[13.5px] ${
          checked ? "text-mute line-through" : ""
        }`}
      >
        {task.title}
      </span>

      {task.source !== "" && (
        <span className="hidden shrink-0 rounded-sm border border-line px-1 font-mono text-[10px] text-mute sm:inline">
          {task.source}
        </span>
      )}

      <span className="hidden shrink-0 gap-1 sm:flex">
        {task.labels.slice(0, 3).map((l) => (
          <Chip key={l} label={l} />
        ))}
        {task.labels.length > 3 && (
          <span className="font-mono text-[11px] text-faint">+{task.labels.length - 3}</span>
        )}
      </span>

      {dueInfo && (
        <span className={`w-[72px] shrink-0 text-right font-mono text-[11px] ${toneClass[dueInfo.tone]}`}>
          {dueInfo.text}
        </span>
      )}
    </div>
  );
}
