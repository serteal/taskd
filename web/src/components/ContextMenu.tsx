import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Task } from "../gen/task/task_pb";
import type { TaskStore } from "../lib/store";
import {
  rescheduleTask,
  setPriority,
  addLabel,
  completeTask,
  deleteTaskWithUndo,
  planToday,
} from "../lib/actions";
import { ScheduleMenu, PriorityMenu, LabelMenu } from "./pickers";
import { Icon, type IconName } from "./icons";

// A right-click context menu positioned at the cursor. Dismisses on
// outside-click, Escape, scroll, or resize, and is keyboard-navigable
// (Up/Down move focus among the controls; Enter/Space activate the button).
// Rendered in a portal so `position: fixed` is honoured regardless of any
// transformed ancestor (the list rows animate).
export function ContextMenu({
  x,
  y,
  onClose,
  children,
}: {
  x: number;
  y: number;
  onClose: () => void;
  children: (close: () => void) => React.ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ left: number; top: number }>({ left: x, top: y });

  // Flip/clamp into the viewport once measured.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const pad = 6;
    setPos({
      left: Math.max(pad, Math.min(x, window.innerWidth - r.width - pad)),
      top: Math.max(pad, Math.min(y, window.innerHeight - r.height - pad)),
    });
  }, [x, y]);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    // Capture Escape before the app's global handler so it only closes us.
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopPropagation();
        e.preventDefault();
        onClose();
      }
    };
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey, true);
    document.addEventListener("scroll", onClose, true);
    window.addEventListener("resize", onClose);
    window.addEventListener("blur", onClose);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey, true);
      document.removeEventListener("scroll", onClose, true);
      window.removeEventListener("resize", onClose);
      window.removeEventListener("blur", onClose);
    };
  }, [onClose]);

  // Focus the first control on open.
  useEffect(() => {
    ref.current?.querySelector<HTMLElement>("button, input")?.focus();
  }, []);

  // Roving focus among the menu's focusable controls.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const items = [...(ref.current?.querySelectorAll<HTMLElement>("button, input") ?? [])];
    if (items.length === 0) return;
    e.preventDefault();
    const i = items.indexOf(document.activeElement as HTMLElement);
    const next =
      e.key === "ArrowDown"
        ? items[i < 0 ? 0 : (i + 1) % items.length]
        : items[i <= 0 ? items.length - 1 : i - 1];
    next?.focus();
  };

  return createPortal(
    <div
      ref={ref}
      role="menu"
      aria-label="Task actions"
      onKeyDown={onKeyDown}
      onContextMenu={(e) => e.preventDefault()}
      style={{ position: "fixed", left: pos.left, top: pos.top }}
      className="z-[60] min-w-[196px] max-w-[260px] rounded-lg border border-line bg-surface p-1 text-ink shadow-xl"
    >
      {children(onClose)}
    </div>,
    document.body,
  );
}

// One row in the root menu: an icon, a label, and an optional drill-in chevron.
function CItem({
  icon,
  children,
  onClick,
  chevron,
  danger,
}: {
  icon?: IconName;
  children: React.ReactNode;
  onClick: () => void;
  chevron?: boolean;
  danger?: boolean;
}) {
  return (
    <button
      type="button"
      role="menuitem"
      onClick={onClick}
      className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-ink/[.05] dark:hover:bg-ink/[.08] ${
        danger ? "text-warn" : "text-ink"
      }`}
    >
      {icon && (
        <span className={danger ? "text-warn" : "text-mute"}>
          <Icon name={icon} size={14} />
        </span>
      )}
      <span className="flex-1">{children}</span>
      {chevron && <Icon name="chevron-right" size={13} className="text-faint" />}
    </button>
  );
}

type Pane = "root" | "schedule" | "priority" | "label";

// The task-action content of the context menu. Schedule / Priority / Add-label
// drill into the shared pickers; everything else routes through lib/actions so
// the same undo toasts fire as elsewhere. Synced (source) tasks show a subset —
// no inline title editing, since their title is owned by the source.
export function TaskContextMenu({
  task,
  now,
  store,
  labelOptions,
  onOpen,
  onEdit,
  close,
}: {
  task: Task;
  now: Date;
  store: TaskStore;
  labelOptions: string[];
  onOpen: () => void;
  onEdit: () => void;
  close: () => void;
}) {
  const [pane, setPane] = useState<Pane>("root");
  const paneRef = useRef<HTMLDivElement>(null);
  const synced = task.source !== "";
  const done = task.completedTime !== undefined;

  // Move focus into the pane's first control whenever it changes.
  useEffect(() => {
    paneRef.current?.querySelector<HTMLElement>("button, input")?.focus();
  }, [pane]);

  const back = (
    <button
      type="button"
      role="menuitem"
      onClick={() => setPane("root")}
      className="mb-1 flex w-full items-center gap-1 rounded-md px-2 py-1 text-left text-[12px] text-mute hover:bg-ink/[.05] dark:hover:bg-ink/[.08]"
    >
      <Icon name="chevron-right" size={12} className="rotate-180" />
      Back
    </button>
  );

  if (pane === "schedule")
    return (
      <div ref={paneRef}>
        {back}
        <ScheduleMenu now={now} onChange={(d) => rescheduleTask(store, task, d)} close={close} />
      </div>
    );
  if (pane === "priority")
    return (
      <div ref={paneRef}>
        {back}
        <PriorityMenu onChange={(p) => setPriority(store, task, p)} close={close} />
      </div>
    );
  if (pane === "label")
    return (
      <div ref={paneRef}>
        {back}
        <LabelMenu
          labels={task.labels}
          options={labelOptions}
          onAdd={(l) => {
            addLabel(store, task, l);
            close();
          }}
        />
      </div>
    );

  return (
    <div ref={paneRef}>
      <CItem icon="open" onClick={onOpen}>
        Open
      </CItem>
      {!synced && !done && (
        <CItem icon="pencil" onClick={onEdit}>
          Rename
        </CItem>
      )}
      <CItem
        icon={done ? "circle" : "check"}
        onClick={() => {
          if (done) void store.update(task.id, { completed: false }).catch(() => {});
          else completeTask(store, task);
          close();
        }}
      >
        {done ? "Reopen" : "Complete"}
      </CItem>
      <CItem icon="calendar" chevron onClick={() => setPane("schedule")}>
        Schedule…
      </CItem>
      <CItem icon="flag" chevron onClick={() => setPane("priority")}>
        Priority
      </CItem>
      <CItem icon="tag" chevron onClick={() => setPane("label")}>
        Add label…
      </CItem>
      <CItem
        icon="clock"
        onClick={() => {
          planToday(store, task, now);
          close();
        }}
      >
        Plan today
      </CItem>
      <div className="my-1 border-t border-line" />
      <CItem
        icon="trash"
        danger
        onClick={() => {
          deleteTaskWithUndo(store, task);
          close();
        }}
      >
        Delete
      </CItem>
    </div>
  );
}
