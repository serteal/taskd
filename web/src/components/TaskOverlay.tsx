import { useEffect, useMemo, useRef, useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { useSnapshot, useStore } from "../lib/hooks";
import { chipParts, fmtStamp, shortId, toLocalInput, tsDate } from "../lib/format";
import { fromNatural, humanize } from "../lib/recur";
import { buildAPI, registry } from "../lib/extensions";
import { completeTask, deleteTaskWithUndo, SYNCED_COMPLETE_MSG } from "../lib/actions";
import { pushUndo } from "../lib/undo";
import { Chip } from "./Chip";
import { Icon } from "./icons";
import { ExtensionBoundary } from "./ExtensionBoundary";
import { Popover } from "./Popover";
import { PriorityMenu, ProjectMenu, LabelMenu, PRIORITIES } from "./pickers";
import { useTitleTypeahead } from "./TitleTypeahead";

const PRIORITY_RE = /^p[1-3]$/;
const isProject = (l: string) => l.startsWith("project:");

// The recurrence presets the editor's select offers; "Custom…" reveals a
// natural-language input parsed by lib/recur's fromNatural.
const RECUR_PRESETS: { label: string; rule: string }[] = [
  { label: "None", rule: "" },
  { label: "Every day", rule: "FREQ=DAILY" },
  { label: "Weekdays", rule: "FREQ=WEEKLY;BYDAY=MO,TU,WE,TH,FR" },
  { label: "Every week", rule: "FREQ=WEEKLY" },
  { label: "Every 2 weeks", rule: "FREQ=WEEKLY;INTERVAL=2" },
  { label: "Every month", rule: "FREQ=MONTHLY" },
  { label: "Every year", rule: "FREQ=YEARLY" },
];

// Grow a textarea to fit its content; the CSS max-height caps it.
function autoGrow(el: HTMLTextAreaElement | null): void {
  if (!el) return;
  el.style.height = "auto";
  el.style.height = `${el.scrollHeight}px`;
}

// The task edit surface — a centered modal (Todoist-style): content on the
// left (title, description, subtasks, source sections), metadata on the right
// (project, date, repeats, priority, labels). Field edits save individually
// (blur/Enter) with the task's revision as expected_revision, exactly like the
// old detail rail; a conflict just means the replica already shows the newer
// truth. ↑/↓ in the header (or ctrl+j/k, ctrl+n/p anywhere in the card) step
// through the surrounding list without leaving the modal.
export function TaskOverlay({
  task,
  onClose,
  onOpenTask,
  onNav,
}: {
  task: Task;
  onClose: () => void;
  /** Navigate the overlay to another task (parent breadcrumb, subtask rows). */
  onOpenTask?: (id: string) => void;
  /** Step to the previous/next task in the current list. */
  onNav?: (delta: 1 | -1) => void;
}) {
  const store = useStore();
  const snap = useSnapshot();
  const synced = task.source !== "";
  const [title, setTitle] = useState(task.title);
  const [notes, setNotes] = useState(task.notes);
  const [newSubtask, setNewSubtask] = useState("");
  // The recurrence editor's Custom… draft; null = the free-text input is closed.
  const [customRecur, setCustomRecur] = useState<string | null>(null);
  const [customRecurErr, setCustomRecurErr] = useState(false);
  const titleRef = useRef<HTMLTextAreaElement>(null);
  const notesRef = useRef<HTMLTextAreaElement>(null);

  // The replica is the source of truth for STORED fields: when the task's
  // title/notes change underneath us (watch event, other window), the inputs
  // re-sync to them. Keyed on the values — not the task object — so a
  // background echo of our own save never touches an input mid-edit.
  useEffect(() => {
    setTitle(task.title);
    setNotes(task.notes);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task.id, task.title, task.notes]);

  // TRANSIENT drafts mirror nothing stored, so only switching tasks resets
  // them — a watch echo mid-typing must not wipe the draft.
  useEffect(() => {
    setNewSubtask("");
    setCustomRecur(null);
    setCustomRecurErr(false);
  }, [task.id]);

  useEffect(() => autoGrow(titleRef.current), [title]);
  useEffect(() => autoGrow(notesRef.current), [notes]);

  const save = (patch: Parameters<typeof store.update>[1]) =>
    store.update(task.id, { ...patch, expectedRevision: task.revision }).catch(() => {});

  // Commit a field edit AND register it on the global undo stack, so ⌘Z can
  // restore the previous value. The undo write is last-write-wins
  // (expectedRevision 0): by the time it runs the task's revision has moved on.
  const editLabel = `edit “${task.title.length > 24 ? task.title.slice(0, 23) + "…" : task.title}”`;
  const commitEdit = (
    patch: Parameters<typeof store.update>[1],
    restore: Parameters<typeof store.update>[1],
  ) => {
    save(patch);
    pushUndo(editLabel, () => void store.update(task.id, { ...restore, expectedRevision: 0n }).catch(() => {}));
  };

  const due = tsDate(task.dueTime);
  const created = tsDate(task.createTime);
  const updated = tsDate(task.updateTime);
  const refIsURL = /^https?:\/\//.test(task.externalRef);
  const data = task.externalData ?? {};
  const dataEntries = Object.entries(data);
  const presenter = registry.presenterFor(task);
  const api = useMemo(() => buildAPI(store), [store]);

  // Priority and project live in the labels, by convention.
  const priority = task.labels.find((l) => PRIORITY_RE.test(l)) ?? null;
  const project = task.labels.find(isProject) ?? null;
  const plainLabels = task.labels.filter((l) => !PRIORITY_RE.test(l) && !isProject(l));
  const setPriority = (p: string | null) =>
    commitEdit(
      { labels: [...task.labels.filter((l) => !PRIORITY_RE.test(l)), ...(p ? [p] : [])] },
      { labels: task.labels },
    );
  const setProject = (p: string | null) =>
    commitEdit(
      { labels: [...(p ? [p] : []), ...task.labels.filter((l) => !isProject(l))] },
      { labels: task.labels },
    );

  // Autocomplete sources from the live replica.
  const allLabels = useMemo(() => {
    const s = new Set<string>();
    for (const t of snap.tasks.values()) for (const l of t.labels) s.add(l);
    return [...s].sort();
  }, [snap]);
  const projectOptions = useMemo(() => allLabels.filter(isProject), [allLabels]);
  const labelOptions = useMemo(
    () => allLabels.filter((l) => !PRIORITY_RE.test(l) && !isProject(l)),
    [allLabels],
  );

  // "@" mentions in the title (labels are structured here, so no "#").
  const typeahead = useTitleTypeahead({
    ref: titleRef,
    value: title,
    onChange: setTitle,
    labels: labelOptions,
    triggers: ["@"],
  });

  // --- recurrence editor (local tasks only) ---------------------------------
  const presetValue = RECUR_PRESETS.some((p) => p.rule === task.recurrence)
    ? task.recurrence
    : "custom";
  const commitRecurrence = (rule: string) => {
    if (rule === task.recurrence) return;
    commitEdit(
      { recurrence: rule === "" ? null : rule },
      { recurrence: task.recurrence === "" ? null : task.recurrence },
    );
  };
  const submitCustomRecur = () => {
    if (customRecur === null) return;
    if (customRecur.trim() === "") {
      setCustomRecur(null);
      setCustomRecurErr(false);
      return;
    }
    const rule = fromNatural(customRecur);
    if (rule === null) {
      setCustomRecurErr(true);
      return;
    }
    setCustomRecurErr(false);
    setCustomRecur(null);
    commitRecurrence(rule);
  };

  // --- subtasks (depth capped at 1) -----------------------------------------
  const isChild = task.parentId !== "";
  const parent = isChild ? snap.tasks.get(task.parentId) : undefined;
  const children = useMemo(() => {
    if (isChild) return [];
    return [...snap.tasks.values()]
      .filter((t) => t.parentId === task.id)
      .sort(
        (a, b) => (tsDate(a.createTime)?.getTime() ?? 0) - (tsDate(b.createTime)?.getTime() ?? 0),
      );
  }, [snap, task.id, isChild]);
  const addSubtask = () => {
    const t = newSubtask.trim();
    if (t === "") return;
    setNewSubtask("");
    void store.create({ title: t, parentId: task.id }).catch(() => {});
  };

  const commitTitle = () => {
    if (title.trim() !== task.title && title.trim() !== "") {
      commitEdit({ title: title.trim() }, { title: task.title });
    }
  };

  const priorityColor = (p: string) => PRIORITIES.find((x) => x.value === p)?.color;

  return (
    <div
      className="fixed inset-0 z-40 flex items-start justify-center bg-black/30 px-4 pt-[7vh]"
      onMouseDown={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Task details"
        data-testid="detail-panel"
        className="flex max-h-[84vh] w-[880px] max-w-full flex-col overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") {
            onClose();
            return;
          }
          // ctrl+j/k and ctrl+n/p: step through the list without leaving the
          // modal. Blur first so a pending field edit commits (blur = save).
          const ctrl = e.ctrlKey && !e.metaKey && !e.altKey;
          if (ctrl && (e.key === "j" || e.key === "k" || e.key === "n" || e.key === "p")) {
            e.preventDefault();
            e.stopPropagation();
            (document.activeElement as HTMLElement | null)?.blur();
            onNav?.(e.key === "j" || e.key === "n" ? 1 : -1);
          }
        }}
      >
        {/* Header: where the task lives, prev/next, esc. */}
        <div className="flex items-center gap-2 border-b border-line px-4 py-2">
          <span className="flex min-w-0 items-center gap-1.5 text-[12.5px] text-mute">
            <Icon name={synced ? "swap" : "inbox"} size={13} />
            <span className="truncate">
              {synced ? task.source : project ? chipParts(project).val : "Inbox"}
            </span>
          </span>
          <span className="ml-auto flex items-center gap-1">
            <span className="mr-2 font-mono text-[11px] text-faint">{shortId(task.id)}</span>
            {onNav && (
              <>
                <button
                  onClick={() => onNav(-1)}
                  aria-label="Previous task"
                  title="Previous task (ctrl+k)"
                  className="rounded p-1 text-mute hover:bg-ink/[.06] hover:text-ink"
                >
                  <Icon name="chevron-right" size={14} className="-rotate-90" />
                </button>
                <button
                  onClick={() => onNav(1)}
                  aria-label="Next task"
                  title="Next task (ctrl+j)"
                  className="rounded p-1 text-mute hover:bg-ink/[.06] hover:text-ink"
                >
                  <Icon name="chevron-right" size={14} className="rotate-90" />
                </button>
              </>
            )}
            <button
              onClick={onClose}
              aria-label="Close details"
              className="ml-1 rounded px-1.5 py-1 font-mono text-[12px] text-mute hover:bg-ink/[.06] hover:text-ink"
            >
              esc
            </button>
          </span>
        </div>

        <div className="flex min-h-0 flex-1">
          {/* Left: the task itself. */}
          <div className="min-w-0 flex-1 space-y-4 overflow-y-auto p-4">
            {synced && (
              <div className="rounded border border-line bg-paper px-2.5 py-2 text-[12px] leading-5 text-mute">
                Synced from <span className="font-mono text-ink">{task.source}</span>. Title, due
                date and completion follow the source; labels and notes are yours.
                {refIsURL && (
                  <>
                    {" "}
                    <a
                      href={task.externalRef}
                      target="_blank"
                      rel="noreferrer"
                      className="text-accent underline decoration-accent/40 underline-offset-2"
                    >
                      Open original ↗
                    </a>
                  </>
                )}
              </div>
            )}

            {/* A subtask points back at its parent (hierarchy caps at 1). */}
            {isChild && (
              <button
                data-testid="parent-breadcrumb"
                onClick={() => onOpenTask?.(task.parentId)}
                title="Open parent task"
                className="flex max-w-full items-center gap-1 font-mono text-[11px] text-mute hover:text-accent"
              >
                <span aria-hidden>↳</span>
                <span className="truncate">{parent?.title ?? shortId(task.parentId)}</span>
              </button>
            )}

            <div className="flex items-start gap-3">
              <button
                aria-label={task.completedTime ? `Reopen ${task.title}` : `Complete ${task.title}`}
                disabled={synced}
                title={synced ? SYNCED_COMPLETE_MSG : undefined}
                onClick={() => {
                  if (synced) return;
                  completeTask(store, task);
                  if (task.recurrence === "") onClose();
                }}
                className={`mt-[3px] flex h-[20px] w-[20px] shrink-0 items-center justify-center rounded-full border transition-colors ${
                  synced
                    ? "cursor-not-allowed border-line text-transparent"
                    : "border-mute/60 text-transparent hover:border-accent hover:text-accent/60"
                }`}
              >
                <svg width="11" height="11" viewBox="0 0 10 10" fill="none" aria-hidden>
                  <path d="M1.5 5.5 4 8l4.5-6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
                </svg>
              </button>
              <div className="relative min-w-0 flex-1">
                <textarea
                  ref={titleRef}
                  value={title}
                  disabled={synced}
                  rows={1}
                  onChange={typeahead.onChange}
                  onBlur={commitTitle}
                  onKeyDown={(e) => {
                    if (typeahead.onKeyDown(e)) return; // the dropdown owns nav/accept keys
                    if (e.key === "Enter") {
                      e.preventDefault();
                      (e.target as HTMLTextAreaElement).blur();
                    }
                  }}
                  aria-label="Title"
                  className="max-h-[30vh] w-full resize-none bg-transparent text-[19px] font-semibold leading-tight focus:outline-none disabled:text-mute"
                />
                {typeahead.menu}
              </div>
            </div>

            <textarea
              ref={notesRef}
              value={notes}
              onChange={(e) => setNotes(e.target.value)}
              onBlur={() => notes !== task.notes && commitEdit({ notes }, { notes: task.notes })}
              rows={1}
              placeholder="Description — anything worth remembering…"
              aria-label="Notes"
              className="ml-8 max-h-[30vh] w-[calc(100%-2rem)] resize-none bg-transparent text-[13.5px] leading-snug text-mute placeholder:text-faint focus:outline-none"
            />

            {/* Subtasks: the open children from the replica. Offered on any
                non-child task; the children themselves are always local. */}
            {!isChild && (
              <div data-testid="subtasks-section" className="ml-8">
                <FieldLabel>
                  subtasks{children.length > 0 ? ` · ${children.length}` : ""}
                </FieldLabel>
                <div className="space-y-1">
                  {children.map((c) => (
                    <div key={c.id} data-testid="subtask-row" className="flex items-center gap-2">
                      <button
                        aria-label={`Complete ${c.title}`}
                        onClick={() => completeTask(store, c)}
                        className="flex h-[14px] w-[14px] shrink-0 items-center justify-center rounded-full border border-mute/60 text-transparent transition-colors hover:border-accent hover:text-accent/60"
                      >
                        <svg width="8" height="8" viewBox="0 0 10 10" fill="none" aria-hidden>
                          <path d="M1.5 5.5 4 8l4.5-6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
                        </svg>
                      </button>
                      <button
                        onClick={() => onOpenTask?.(c.id)}
                        className="min-w-0 flex-1 truncate text-left text-[13px] hover:text-accent"
                      >
                        {c.title}
                      </button>
                    </div>
                  ))}
                  <input
                    value={newSubtask}
                    onChange={(e) => setNewSubtask(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && addSubtask()}
                    placeholder="+ Add subtask"
                    aria-label="Add subtask"
                    className="w-full bg-transparent text-[12.5px] placeholder:text-faint focus:outline-none"
                  />
                </div>
              </div>
            )}

            {presenter?.DetailSection && (
              <ExtensionBoundary name={task.source || "extension"}>
                <presenter.DetailSection task={task} api={api} />
              </ExtensionBoundary>
            )}

            {dataEntries.length > 0 && !presenter?.DetailSection && (
              <div>
                <FieldLabel>{task.source} data</FieldLabel>
                <dl className="space-y-1 rounded border border-line bg-paper p-2 font-mono text-[11px]">
                  {dataEntries.map(([k, v]) => (
                    <div key={k} className="flex gap-2">
                      <dt className="w-24 shrink-0 text-faint">{k}</dt>
                      <dd className="min-w-0 break-words text-mute">{String(v)}</dd>
                    </div>
                  ))}
                </dl>
              </div>
            )}
          </div>

          {/* Right: metadata rail. */}
          <div className="w-[264px] shrink-0 space-y-4 overflow-y-auto border-l border-line bg-paper/60 p-4">
            <div>
              <FieldLabel>project</FieldLabel>
              <Popover
                trigger={({ toggle }) => (
                  <button
                    onClick={toggle}
                    disabled={synced}
                    data-testid="project-field"
                    className="flex w-full items-center gap-1.5 rounded-md border border-transparent px-1.5 py-1 text-left text-[13px] hover:border-line disabled:cursor-not-allowed"
                  >
                    <Icon name="inbox" size={13} className="text-mute" />
                    {project ? chipParts(project).val : "Inbox"}
                  </button>
                )}
              >
                {(close) => (
                  <ProjectMenu options={projectOptions} onChange={setProject} close={close} />
                )}
              </Popover>
            </div>

            <div>
              <FieldLabel>date</FieldLabel>
              <div className="flex items-center gap-2">
                <input
                  type="datetime-local"
                  disabled={synced}
                  value={due ? toLocalInput(due) : ""}
                  onChange={(e) =>
                    commitEdit(
                      { due: e.target.value === "" ? null : new Date(e.target.value) },
                      { due: due ?? null },
                    )
                  }
                  aria-label="Due date"
                  className="w-full rounded border border-line bg-paper px-1.5 py-1 font-mono text-[12px] focus:outline-none disabled:text-mute"
                />
                {due && !synced && (
                  <button
                    onClick={() => commitEdit({ due: null }, { due })}
                    className="text-[11px] text-mute hover:text-warn"
                  >
                    clear
                  </button>
                )}
              </div>
            </div>

            {/* Recurrence is user-owned and local-only. */}
            {!synced && (
              <div data-testid="recurrence-editor">
                <FieldLabel>repeats</FieldLabel>
                <select
                  aria-label="Recurrence"
                  value={customRecur !== null ? "custom" : presetValue}
                  onChange={(e) => {
                    const v = e.target.value;
                    if (v === "custom") {
                      setCustomRecur("");
                      setCustomRecurErr(false);
                    } else {
                      setCustomRecur(null);
                      setCustomRecurErr(false);
                      commitRecurrence(v);
                    }
                  }}
                  className="w-full rounded border border-line bg-paper px-1.5 py-1 font-mono text-[12px] focus:outline-none"
                >
                  {RECUR_PRESETS.map((p) => (
                    <option key={p.label} value={p.rule}>
                      {p.label}
                    </option>
                  ))}
                  <option value="custom">Custom…</option>
                </select>
                {task.recurrence !== "" && customRecur === null && (
                  <span
                    data-testid="recurrence-current"
                    className="mt-1 inline-flex min-w-0 items-center gap-1 font-mono text-[11px] text-mute"
                  >
                    <Icon name="repeat" size={11} />
                    <span className="truncate">{humanize(task.recurrence)}</span>
                  </span>
                )}
                {customRecur !== null && (
                  <div className="mt-1.5">
                    <input
                      autoFocus
                      value={customRecur}
                      onChange={(e) => {
                        setCustomRecur(e.target.value);
                        setCustomRecurErr(false);
                      }}
                      onKeyDown={(e) => e.key === "Enter" && submitCustomRecur()}
                      onBlur={submitCustomRecur}
                      placeholder="e.g. every 3 days, every mon,wed"
                      aria-label="Custom recurrence"
                      className="w-full rounded border border-line bg-paper px-2 py-1 font-mono text-[12px] placeholder:text-faint focus:outline-none"
                    />
                    {customRecurErr && (
                      <div data-testid="recurrence-error" className="mt-1 text-[11px] text-warn">
                        Not a recurrence — try “every 2 weeks” or “every mon,fri”.
                      </div>
                    )}
                  </div>
                )}
              </div>
            )}

            <div>
              <FieldLabel>priority</FieldLabel>
              <Popover
                trigger={({ toggle }) => (
                  <button
                    onClick={toggle}
                    disabled={synced}
                    data-testid="priority-field"
                    className="flex w-full items-center gap-1.5 rounded-md border border-transparent px-1.5 py-1 text-left text-[13px] hover:border-line disabled:cursor-not-allowed"
                  >
                    <span style={priority ? { color: priorityColor(priority) } : undefined} className={priority ? "" : "text-mute"}>
                      <Icon name="flag" size={13} />
                    </span>
                    {priority ? priority.toUpperCase() : <span className="text-mute">None</span>}
                  </button>
                )}
              >
                {(close) => <PriorityMenu onChange={setPriority} close={close} />}
              </Popover>
            </div>

            <div>
              <FieldLabel>labels</FieldLabel>
              <div className="flex flex-wrap items-center gap-1.5">
                {plainLabels.map((l) => (
                  <Chip
                    key={l}
                    label={l}
                    onRemove={() =>
                      commitEdit(
                        { labels: task.labels.filter((x) => x !== l) },
                        { labels: task.labels },
                      )
                    }
                  />
                ))}
                <Popover
                  trigger={({ toggle }) => (
                    <button
                      onClick={toggle}
                      aria-label="Add label"
                      data-testid="add-label"
                      className="rounded-full border border-dashed border-line px-1.5 py-px font-mono text-[11px] leading-4 text-mute hover:border-mute hover:text-ink"
                    >
                      + label
                    </button>
                  )}
                >
                  {() => (
                    <LabelMenu
                      labels={task.labels}
                      options={labelOptions}
                      onAdd={(l) =>
                        commitEdit({ labels: [...task.labels, l] }, { labels: task.labels })
                      }
                    />
                  )}
                </Popover>
              </div>
            </div>

            <dl className="space-y-1 border-t border-line pt-3 font-mono text-[11px] text-faint">
              {created && <MetaRow k="created" v={fmtStamp(created)} />}
              {updated && <MetaRow k="updated" v={fmtStamp(updated)} />}
              <MetaRow k="revision" v={String(task.revision)} />
              {task.externalRef !== "" && !refIsURL && <MetaRow k="ref" v={task.externalRef} />}
            </dl>
          </div>
        </div>

        <div className="flex items-center justify-between border-t border-line px-4 py-2">
          {/* Completion is source-owned on synced tasks (banner says so). */}
          {task.completedTime ? (
            <button
              onClick={() => save({ completed: false }).then(onClose)}
              disabled={synced}
              title={synced ? SYNCED_COMPLETE_MSG : undefined}
              className="rounded border border-line px-2 py-1 text-[12px] text-mute hover:text-ink disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:text-mute"
            >
              Reopen
            </button>
          ) : (
            <button
              onClick={() => {
                completeTask(store, task);
                onClose();
              }}
              disabled={synced}
              title={synced ? SYNCED_COMPLETE_MSG : undefined}
              className="rounded border border-accent/50 px-2 py-1 text-[12px] text-accent hover:bg-accent/10 disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-transparent"
            >
              Complete
            </button>
          )}
          <button
            onClick={() => {
              deleteTaskWithUndo(store, task);
              onClose();
            }}
            className="rounded px-2 py-1 text-[12px] text-mute hover:text-warn"
          >
            Delete
          </button>
        </div>
      </div>
    </div>
  );
}

function FieldLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="pb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-faint">
      {children}
    </div>
  );
}

function MetaRow({ k, v }: { k: string; v: string }) {
  return (
    <div className="flex gap-2">
      <dt className="w-16 shrink-0">{k}</dt>
      <dd className="text-mute">{v}</dd>
    </div>
  );
}
