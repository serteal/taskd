import type { Task } from "../gen/task/task_pb";
import { humanDue, tsDate } from "../lib/format";
import { getTimebox, fmtClock, fmtTimeboxRange } from "../lib/timebox";
import { registry } from "../lib/extensions";
import { setTaskDrag } from "../lib/dnd";
import { useStore } from "../lib/hooks";
import { rescheduleTask } from "../lib/actions";
import { Chip } from "./Chip";
import { Popover } from "./Popover";
import { ScheduleMenu } from "./pickers";
import { Icon } from "./icons";

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
  bulkSelected,
  pulsing,
  checked,
  editing,
  onToggle,
  onActivate,
  onSelect,
  onStartEdit,
  onRename,
  onEndEdit,
  onContextMenu,
}: {
  task: Task;
  now: Date;
  /** Keyboard-focus highlight. */
  selected: boolean;
  /** Part of the multi-select set. */
  bulkSelected: boolean;
  pulsing: boolean;
  /** Optimistic strike-through while the completion round-trips. */
  checked: boolean;
  /** Title is being edited inline. */
  editing: boolean;
  onToggle: () => void;
  onActivate: (mods: { meta: boolean; shift: boolean }) => void;
  onSelect: () => void;
  onStartEdit: () => void;
  onRename: (title: string) => void;
  onEndEdit: () => void;
  /** Open the row's context menu at the cursor. */
  onContextMenu?: (x: number, y: number) => void;
}) {
  const store = useStore();
  const due = tsDate(task.dueTime);
  const dueInfo = due ? humanDue(due, now) : undefined;
  const meta = registry.presenterFor(task)?.rowMeta?.(task) ?? {};
  const synced = task.source !== "";
  // A user-owned timebox surfaces as a quiet planned-time chip (non-synced
  // only; synced rows carry their source's own time).
  const timebox = !synced ? getTimebox(task) : undefined;

  return (
    <div
      data-task-row={task.id}
      draggable
      onDragStart={(e) => setTaskDrag(e.dataTransfer, task.id)}
      onMouseEnter={onSelect}
      onClick={(e) => onActivate({ meta: e.metaKey || e.ctrlKey, shift: e.shiftKey })}
      onContextMenu={(e) => {
        if (!onContextMenu || editing) return;
        e.preventDefault();
        onContextMenu(e.clientX, e.clientY);
      }}
      className={`group relative flex cursor-pointer items-center gap-2.5 border-b border-line/70 px-3 py-[7px] ${
        bulkSelected
          ? "bg-accent/[.08]"
          : selected
            ? "bg-ink/[.045] dark:bg-ink/[.07]"
            : ""
      } ${pulsing ? "pulse" : ""}`}
    >
      {bulkSelected && <span className="absolute inset-y-0 left-0 w-[2px] bg-accent" aria-hidden />}
      <span
        aria-hidden
        className="-ml-1.5 w-2 shrink-0 cursor-grab select-none text-center font-mono text-[11px] leading-none text-transparent group-hover:text-faint"
        title="Drag to timebox or reorder"
      >
        ⠿
      </span>
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

      {meta.icon != null && (
        <span className="flex shrink-0 items-center text-mute">{meta.icon}</span>
      )}

      {editing ? (
        <input
          autoFocus
          defaultValue={task.title}
          onClick={(e) => e.stopPropagation()}
          onKeyDown={(e) => {
            e.stopPropagation();
            if (e.key === "Enter") e.currentTarget.blur();
            else if (e.key === "Escape") {
              e.currentTarget.value = task.title; // discard, blur won't rename
              onEndEdit();
            }
          }}
          onBlur={(e) => {
            onRename(e.currentTarget.value);
            onEndEdit();
          }}
          className="min-w-0 flex-1 border-b border-accent bg-transparent text-[13.5px] focus:outline-none"
        />
      ) : (
        <span
          onDoubleClick={(e) => {
            if (synced) return;
            e.stopPropagation();
            onStartEdit();
          }}
          className={`min-w-0 flex-1 truncate text-[13.5px] ${checked ? "text-mute line-through" : ""}`}
        >
          {task.title}
          {meta.subtitle && <span className="ml-2 font-mono text-[11px] text-faint">{meta.subtitle}</span>}
        </span>
      )}

      {/* Meta chips — always visible; hover no longer swaps them out. Row
          actions (schedule, priority, label, delete) live in the right-click
          context menu instead. */}
      <span className="hidden shrink-0 items-center gap-1 sm:flex">
        {timebox && (
          <span
            data-testid="row-timebox"
            title={`Planned ${fmtTimeboxRange(timebox)}`}
            className="inline-flex items-center gap-0.5 rounded-full border border-accent/30 bg-accent/[.06] px-1.5 py-px font-mono text-[11px] leading-4 text-accent"
          >
            <Icon name="clock" size={11} />
            {fmtClock(timebox.start)}
          </span>
        )}
        {(meta.extraChips ?? []).map((l) => (
          <Chip key={`x-${l}`} label={l} />
        ))}
        {task.labels.slice(0, 3).map((l) => (
          <Chip key={l} label={l} />
        ))}
        {task.labels.length > 3 && (
          <span className="font-mono text-[11px] text-faint">+{task.labels.length - 3}</span>
        )}
      </span>

      {/* Due date — click to reschedule (synced tasks show read-only time). */}
      {meta.timeText ? (
        <span className="w-[88px] shrink-0 text-right font-mono text-[11px] text-mute">{meta.timeText}</span>
      ) : synced ? (
        dueInfo && (
          <span className={`w-[88px] shrink-0 text-right font-mono text-[11px] ${toneClass[dueInfo.tone]}`}>
            {dueInfo.text}
          </span>
        )
      ) : (
        <span className="w-[88px] shrink-0 text-right" onClick={(e) => e.stopPropagation()}>
          <Popover
            align="right"
            trigger={({ toggle }) => (
              <button
                onClick={toggle}
                className={`font-mono text-[11px] ${dueInfo ? toneClass[dueInfo.tone] : "text-faint opacity-0 group-hover:opacity-100"}`}
              >
                {dueInfo ? dueInfo.text : "+ date"}
              </button>
            )}
          >
            {(close) => <ScheduleMenu now={now} onChange={(d) => rescheduleTask(store, task, d)} close={close} />}
          </Popover>
        </span>
      )}
    </div>
  );
}
