import type { Task } from "../gen/task/task_pb";
import { useStore } from "../lib/hooks";
import { completeMany, deleteMany, rescheduleMany, addLabelMany } from "../lib/actions";
import { Popover } from "./Popover";
import { ScheduleMenu, LabelMenu } from "./pickers";

// The bottom bar shown while a multi-selection exists: batch actions over the
// selected tasks, each undoable via one toast. Clears the selection after.
export function BulkBar({
  tasks,
  now,
  labelOptions,
  onClear,
}: {
  tasks: Task[];
  now: Date;
  labelOptions: string[];
  onClear: () => void;
}) {
  const store = useStore();
  const run = (fn: () => void) => {
    fn();
    onClear();
  };
  const btn = "rounded px-2 py-0.5 text-[12.5px] text-ink hover:bg-ink/[.06] dark:hover:bg-ink/[.1]";

  return (
    <div
      data-testid="bulk-bar"
      className="flex items-center gap-1.5 border-t border-line bg-surface px-3 py-1.5"
    >
      <span className="mr-1 font-mono text-[12px] text-accent">{tasks.length} selected</span>
      <button onClick={() => run(() => completeMany(store, tasks))} className={btn}>
        Complete
      </button>
      <Popover
        trigger={({ toggle }) => (
          <button onClick={toggle} className={btn}>
            Schedule
          </button>
        )}
      >
        {(close) => (
          <ScheduleMenu now={now} onChange={(d) => run(() => rescheduleMany(store, tasks, d))} close={close} />
        )}
      </Popover>
      <Popover
        trigger={({ toggle }) => (
          <button onClick={toggle} className={btn}>
            Label
          </button>
        )}
      >
        {() => <LabelMenu labels={[]} options={labelOptions} onAdd={(l) => run(() => addLabelMany(store, tasks, l))} />}
      </Popover>
      <button onClick={() => run(() => deleteMany(store, tasks))} className={`${btn} text-warn`}>
        Delete
      </button>
      <button onClick={onClear} className="ml-auto font-mono text-[11px] text-mute hover:text-ink">
        clear ·esc
      </button>
    </div>
  );
}
