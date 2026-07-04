import { useEffect, useMemo, useRef, useState } from "react";
import { useSnapshot, useStore } from "../lib/hooks";
import { parseQuickAdd } from "../lib/quickadd";
import { chipParts, endOfDay, humanDue } from "../lib/format";
import { Chip } from "./Chip";

// The single "new task" surface — a modal that replaces the old inline bar.
// The title field parses quick-add tokens live (#label, p1-3, dates), and the
// pills below show and override the parsed values, Todoist-style. Everything
// maps to our model: priority and project are labels, "schedule" is due_time.

type Override<T> = T | undefined; // undefined = inherit from the parsed title

const PRIORITY_RE = /^p[1-3]$/;
const isProject = (l: string) => l.startsWith("project:");

function addDays(d: Date, n: number): Date {
  const out = new Date(d);
  out.setDate(out.getDate() + n);
  return out;
}
// The next occurrence of weekday `dow` (0=Sun); today counts when includeToday.
function nextDow(now: Date, dow: number, includeToday: boolean): Date {
  let delta = (dow - now.getDay() + 7) % 7;
  if (delta === 0 && !includeToday) delta = 7;
  return endOfDay(addDays(now, delta));
}

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

  const parsed = useMemo(() => parseQuickAdd(title, now), [title, now]);
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
  const projectOptions = useMemo(
    () => allLabels.filter(isProject).sort(),
    [allLabels],
  );
  const labelOptions = useMemo(
    () => allLabels.filter((l) => !PRIORITY_RE.test(l) && !isProject(l)).sort(),
    [allLabels],
  );

  const canAdd = parsed.title.trim() !== "";

  const submit = () => {
    if (!canAdd) return;
    const finalLabels = [
      ...(project ? [project] : []),
      ...(priority ? [priority] : []),
      ...labels,
    ];
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
      // keep due/project context for rapid entry
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
            <SchedulePill now={now} due={due} onChange={(d) => setDueOv(d)} />
            <PriorityPill priority={priority} onChange={(p) => setPrioOv(p)} />
            <LabelsPill
              labels={labels}
              options={labelOptions}
              onOpen={() => labelsOv === undefined && setLabelsOv(parsedLabels)}
              onChange={(ls) => setLabelsOv(ls)}
            />
            {labels.map((l) => (
              <Chip
                key={l}
                label={l}
                onRemove={() => setLabelsOv(labels.filter((x) => x !== l))}
              />
            ))}
          </div>
        </div>

        <div className="flex items-center justify-between border-t border-line px-4 py-2.5">
          <ProjectPill project={project} options={projectOptions} onChange={(p) => setProjectOv(p)} />
          <div className="flex items-center gap-2">
            <label className="mr-1 hidden cursor-pointer select-none items-center gap-1 font-mono text-[11px] text-faint sm:flex">
              <input
                type="checkbox"
                checked={keepOpen}
                onChange={(e) => setKeepOpen(e.target.checked)}
              />
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

// --- popover primitive -----------------------------------------------------

function Popover({
  label,
  active,
  tone,
  children,
}: {
  label: React.ReactNode;
  active?: boolean;
  tone?: "accent" | "warn";
  children: (close: () => void) => React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", onDown);
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      document.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const toneClass = active
    ? tone === "warn"
      ? "border-warn/50 text-warn"
      : "border-accent/50 text-accent"
    : "border-line text-ink hover:border-mute";

  return (
    <div ref={ref} className="relative">
      <button
        type="button"
        onClick={() => setOpen((o) => !o)}
        className={`flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[12.5px] ${toneClass}`}
      >
        {label}
      </button>
      {open && (
        <div
          className="absolute left-0 top-full z-50 mt-1 min-w-[180px] rounded-lg border border-line bg-surface p-1 shadow-xl"
          onMouseDown={(e) => e.stopPropagation()}
        >
          {children(() => setOpen(false))}
        </div>
      )}
    </div>
  );
}

function MenuItem({
  onClick,
  children,
  hint,
}: {
  onClick: () => void;
  children: React.ReactNode;
  hint?: string;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className="flex w-full items-center justify-between gap-3 rounded-md px-2 py-1.5 text-left text-[13px] text-ink hover:bg-ink/[.05] dark:hover:bg-ink/[.08]"
    >
      <span>{children}</span>
      {hint && <span className="font-mono text-[11px] text-faint">{hint}</span>}
    </button>
  );
}

// --- pills -----------------------------------------------------------------

function SchedulePill({
  now,
  due,
  onChange,
}: {
  now: Date;
  due: Date | null;
  onChange: (d: Date | null) => void;
}) {
  const label = due ? humanDue(due, now).text : "Schedule";
  return (
    <Popover
      active={!!due}
      label={
        <>
          <span aria-hidden>📅</span>
          <span className="capitalize">{label}</span>
          {due && (
            <span
              role="button"
              aria-label="Clear date"
              onClick={(e) => {
                e.stopPropagation();
                onChange(null);
              }}
              className="ml-0.5 text-mute hover:text-warn"
            >
              ×
            </span>
          )}
        </>
      }
    >
      {(close) => (
        <div>
          <MenuItem onClick={() => (onChange(endOfDay(now)), close())} hint="today">
            Today
          </MenuItem>
          <MenuItem onClick={() => (onChange(endOfDay(addDays(now, 1))), close())} hint="tomorrow">
            Tomorrow
          </MenuItem>
          <MenuItem onClick={() => (onChange(nextDow(now, 6, true)), close())} hint="sat">
            This weekend
          </MenuItem>
          <MenuItem onClick={() => (onChange(nextDow(now, 1, false)), close())} hint="mon">
            Next week
          </MenuItem>
          <MenuItem onClick={() => (onChange(null), close())}>No date</MenuItem>
          <div className="mt-1 border-t border-line px-2 pb-1 pt-2">
            <input
              type="datetime-local"
              onChange={(e) => e.target.value && (onChange(new Date(e.target.value)), close())}
              aria-label="Custom date and time"
              className="w-full rounded border border-line bg-paper px-1.5 py-1 font-mono text-[12px] focus:outline-none"
            />
          </div>
        </div>
      )}
    </Popover>
  );
}

const PRIORITIES: { label: string; value: string; color: string }[] = [
  { label: "Priority 1", value: "p1", color: "var(--warn)" },
  { label: "Priority 2", value: "p2", color: "#d98a2b" },
  { label: "Priority 3", value: "p3", color: "var(--accent)" },
];

function PriorityPill({
  priority,
  onChange,
}: {
  priority: string | null;
  onChange: (p: string | null) => void;
}) {
  return (
    <Popover
      active={!!priority}
      label={
        <>
          <span aria-hidden>⚑</span>
          <span>{priority ? priority.toUpperCase() : "Priority"}</span>
        </>
      }
    >
      {(close) => (
        <div>
          {PRIORITIES.map((p) => (
            <MenuItem key={p.value} onClick={() => (onChange(p.value), close())} hint={p.value}>
              <span className="inline-flex items-center gap-2">
                <span aria-hidden style={{ color: p.color }}>
                  ⚑
                </span>
                {p.label}
              </span>
            </MenuItem>
          ))}
          <MenuItem onClick={() => (onChange(null), close())}>No priority</MenuItem>
        </div>
      )}
    </Popover>
  );
}

function LabelsPill({
  labels,
  options,
  onOpen,
  onChange,
}: {
  labels: string[];
  options: string[];
  onOpen: () => void;
  onChange: (ls: string[]) => void;
}) {
  const [query, setQuery] = useState("");
  const matches = options.filter(
    (l) => !labels.includes(l) && l.toLowerCase().includes(query.toLowerCase()),
  );
  const add = (l: string) => {
    onChange([...labels, l]);
    setQuery("");
  };
  return (
    <div onClick={onOpen}>
      <Popover
        active={labels.length > 0}
        label={
          <>
            <span aria-hidden>🏷</span>
            <span>Labels</span>
          </>
        }
      >
        {() => (
          <div className="min-w-[200px]">
            <input
              autoFocus
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && query.trim()) add(query.trim());
              }}
              placeholder="Type a label…"
              className="mb-1 w-full rounded border border-line bg-paper px-1.5 py-1 text-[13px] focus:outline-none"
            />
            {query.trim() && !options.includes(query.trim()) && (
              <MenuItem onClick={() => add(query.trim())} hint="new">
                Create “{query.trim()}”
              </MenuItem>
            )}
            {matches.slice(0, 8).map((l) => (
              <MenuItem key={l} onClick={() => add(l)}>
                {l}
              </MenuItem>
            ))}
            {matches.length === 0 && !query && (
              <div className="px-2 py-1.5 text-[12px] text-faint">No labels yet</div>
            )}
          </div>
        )}
      </Popover>
    </div>
  );
}

function ProjectPill({
  project,
  options,
  onChange,
}: {
  project: string | null;
  options: string[];
  onChange: (p: string | null) => void;
}) {
  return (
    <Popover
      active={!!project}
      label={
        <>
          <span aria-hidden>📥</span>
          <span>{project ? chipParts(project).val : "Inbox"}</span>
          <span className="text-faint">▾</span>
        </>
      }
    >
      {(close) => (
        <div>
          <MenuItem onClick={() => (onChange(null), close())}>Inbox</MenuItem>
          {options.map((p) => (
            <MenuItem key={p} onClick={() => (onChange(p), close())}>
              {chipParts(p).val}
            </MenuItem>
          ))}
        </div>
      )}
    </Popover>
  );
}
