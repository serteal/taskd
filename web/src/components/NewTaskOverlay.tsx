import { useEffect, useMemo, useRef, useState } from "react";
import { useSnapshot, useStore } from "../lib/hooks";
import { parseQuickAdd } from "../lib/quickadd";
import { registry, useRegistry } from "../lib/extensions";
import { chipParts, humanDue } from "../lib/format";
import { Chip } from "./Chip";
import { Popover, PillButton } from "./Popover";
import { ScheduleMenu, PriorityMenu, LabelMenu, ProjectMenu } from "./pickers";
import { Icon } from "./icons";

// The single "new task" surface — a modal that replaced the old inline bar.
// The title field parses quick-add tokens live (#label, p1-3, dates), and the
// pills below show and override the parsed values, Todoist-style. Everything
// maps to our model: priority and project are labels, "schedule" is due_time.

type Override<T> = T | undefined; // undefined = inherit from the parsed title

const PRIORITY_RE = /^p[1-3]$/;
const isProject = (l: string) => l.startsWith("project:");

export interface NewTaskInitial {
  project?: string; // a "project:x" label
  due?: Date | null;
}

export function NewTaskOverlay({
  now,
  initial,
  onClose,
}: {
  now: Date;
  initial?: NewTaskInitial;
  onClose: () => void;
}) {
  const store = useStore();
  const snap = useSnapshot();

  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [dueOv, setDueOv] = useState<Override<Date | null>>(initial?.due ?? undefined);
  const [prioOv, setPrioOv] = useState<Override<string | null>>(undefined);
  const [labelsOv, setLabelsOv] = useState<Override<string[]>>(undefined);
  const [projectOv, setProjectOv] = useState<Override<string | null>>(initial?.project ?? undefined);
  const [keepOpen, setKeepOpen] = useState(false);
  const titleRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => titleRef.current?.focus(), []);

  const regVersion = useRegistry(); // pick up extension-contributed quick-add tokens
  const parsed = useMemo(
    () => parseQuickAdd(title, now, registry.quickAddTokens),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [title, now, regVersion],
  );
  const parsedPriority = parsed.labels.find((l) => PRIORITY_RE.test(l)) ?? null;
  const parsedProject = parsed.labels.find(isProject) ?? null;
  const parsedLabels = parsed.labels.filter((l) => !PRIORITY_RE.test(l) && !isProject(l));

  const due = dueOv !== undefined ? dueOv : parsed.due ?? null;
  const priority = prioOv !== undefined ? prioOv : parsedPriority;
  const labels = labelsOv !== undefined ? labelsOv : parsedLabels;
  const project = projectOv !== undefined ? projectOv : parsedProject;

  // Autocomplete sources from the live replica.
  const allLabels = useMemo(() => {
    const s = new Set<string>();
    for (const t of snap.tasks.values()) for (const l of t.labels) s.add(l);
    return [...s];
  }, [snap]);
  const projectOptions = useMemo(() => allLabels.filter(isProject).sort(), [allLabels]);
  const labelOptions = useMemo(
    () => allLabels.filter((l) => !PRIORITY_RE.test(l) && !isProject(l)).sort(),
    [allLabels],
  );

  const canAdd = parsed.title.trim() !== "";

  const submit = () => {
    if (!canAdd) return;
    const finalLabels = [...(project ? [project] : []), ...(priority ? [priority] : []), ...labels];
    void store
      .create({
        title: parsed.title.trim(),
        notes: description.trim() || undefined,
        labels: finalLabels,
        due: due ?? undefined,
      })
      .catch(() => {});
    if (keepOpen) {
      setTitle("");
      setDescription("");
      setLabelsOv(undefined);
      setPrioOv(undefined);
      titleRef.current?.focus();
    } else {
      onClose();
    }
  };

  return (
    <div
      className="fixed inset-0 z-40 flex items-start justify-center bg-black/30 px-4 pt-[12vh]"
      onMouseDown={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="New task"
        className="w-[640px] max-w-full overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose();
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) submit();
        }}
      >
        <div className="p-4">
          <textarea
            ref={titleRef}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                submit();
              }
            }}
            rows={1}
            placeholder="Task name"
            aria-label="Task name"
            className="w-full resize-none bg-transparent text-[19px] font-semibold leading-tight placeholder:text-faint focus:outline-none"
          />
          <textarea
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={1}
            placeholder="Description"
            aria-label="Description"
            className="mt-1 w-full resize-none bg-transparent text-[13.5px] leading-snug text-mute placeholder:text-faint focus:outline-none"
          />

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Popover
              trigger={({ toggle }) => (
                <PillButton
                  icon={<Icon name="calendar" size={14} />}
                  label={due ? humanDue(due, now).text : "Schedule"}
                  active={!!due}
                  onClick={toggle}
                  onClear={due ? () => setDueOv(null) : undefined}
                />
              )}
            >
              {(close) => <ScheduleMenu now={now} onChange={(d) => setDueOv(d)} close={close} />}
            </Popover>

            <Popover
              trigger={({ toggle }) => (
                <PillButton
                  icon={<Icon name="flag" size={14} />}
                  label={priority ? priority.toUpperCase() : "Priority"}
                  active={!!priority}
                  onClick={toggle}
                />
              )}
            >
              {(close) => <PriorityMenu onChange={(p) => setPrioOv(p)} close={close} />}
            </Popover>

            <Popover
              trigger={({ toggle }) => (
                <PillButton
                  icon={<Icon name="tag" size={14} />}
                  label="Labels"
                  active={labels.length > 0}
                  onClick={() => {
                    if (labelsOv === undefined) setLabelsOv(parsedLabels);
                    toggle();
                  }}
                />
              )}
            >
              {() => (
                <LabelMenu
                  labels={labels}
                  options={labelOptions}
                  onAdd={(l) => setLabelsOv([...labels, l])}
                />
              )}
            </Popover>

            {labels.map((l) => (
              <Chip key={l} label={l} onRemove={() => setLabelsOv(labels.filter((x) => x !== l))} />
            ))}
          </div>
        </div>

        <div className="flex items-center justify-between border-t border-line px-4 py-2.5">
          <Popover
            trigger={({ toggle }) => (
              <PillButton
                icon={<Icon name="inbox" size={14} />}
                label={
                  <>
                    {project ? chipParts(project).val : "Inbox"}
                    <span className="ml-1 text-faint">▾</span>
                  </>
                }
                active={!!project}
                onClick={toggle}
              />
            )}
          >
            {(close) => (
              <ProjectMenu options={projectOptions} onChange={(p) => setProjectOv(p)} close={close} />
            )}
          </Popover>

          <div className="flex items-center gap-2">
            <label className="mr-1 hidden cursor-pointer select-none items-center gap-1 font-mono text-[11px] text-faint sm:flex">
              <input type="checkbox" checked={keepOpen} onChange={(e) => setKeepOpen(e.target.checked)} />
              add more
            </label>
            <button
              onClick={onClose}
              className="rounded-md bg-ink/[.06] px-3 py-1.5 text-[13px] font-medium text-ink hover:bg-ink/[.1] dark:bg-ink/[.1] dark:hover:bg-ink/[.16]"
            >
              Cancel
            </button>
            <button
              onClick={submit}
              disabled={!canAdd}
              className="rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white disabled:cursor-not-allowed disabled:opacity-40"
            >
              Add task
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
