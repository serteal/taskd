import { useEffect, useMemo, useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { useSnapshot, useStore } from "../lib/hooks";
import { fmtStamp, shortId, toLocalInput, tsDate } from "../lib/format";
import { fromNatural, humanize } from "../lib/recur";
import { buildAPI, registry } from "../lib/extensions";
import { completeTask, deleteTaskWithUndo, SYNCED_COMPLETE_MSG } from "../lib/actions";
import { pushUndo } from "../lib/undo";
import { Chip } from "./Chip";
import { Icon } from "./icons";
import { ExtensionBoundary } from "./ExtensionBoundary";

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

// Field edits save individually (blur/Enter) with the task's revision as
// expected_revision; a conflict just means the replica already shows the
// newer truth — the panel re-renders from it and the toast says so.
export function DetailPanel({
  task,
  onClose,
  onOpenTask,
  width,
  resizeHandle,
}: {
  task: Task;
  onClose: () => void;
  /** Navigate the panel to another task (parent breadcrumb, subtask rows). */
  onOpenTask?: (id: string) => void;
  /** Width in px (the drag-resizable preference). Falls back to the default. */
  width?: number;
  /** The drag handle on the panel's left edge. */
  resizeHandle?: React.ReactNode;
}) {
  const store = useStore();
  const snap = useSnapshot();
  const synced = task.source !== "";
  const [title, setTitle] = useState(task.title);
  const [notes, setNotes] = useState(task.notes);
  const [newLabel, setNewLabel] = useState("");
  const [newSubtask, setNewSubtask] = useState("");
  // The recurrence editor's Custom… draft; null = the free-text input is closed.
  const [customRecur, setCustomRecur] = useState<string | null>(null);
  const [customRecurErr, setCustomRecurErr] = useState(false);

  // The replica is the source of truth for STORED fields: when the task's
  // title/notes change underneath us (watch event, other window), the inputs
  // re-sync to them. Keyed on the values — not the task object — so a
  // background echo of our own save never touches an input mid-edit.
  useEffect(() => {
    setTitle(task.title);
    setNotes(task.notes);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [task.id, task.title, task.notes]);

  // TRANSIENT drafts (label-add, subtask-add, the Custom… recurrence text)
  // mirror nothing stored, so only switching to a different task resets them —
  // a watch echo arriving while the user is typing must not wipe the draft
  // (or unmount the input under their cursor).
  useEffect(() => {
    setNewLabel("");
    setNewSubtask("");
    setCustomRecur(null);
    setCustomRecurErr(false);
  }, [task.id]);

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

  // --- recurrence editor (local tasks only; the server rejects it on synced) —
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
      // Abandoned empty — close the input without writing or complaining.
      setCustomRecur(null);
      setCustomRecurErr(false);
      return;
    }
    const rule = fromNatural(customRecur);
    if (rule === null) {
      setCustomRecurErr(true); // inline hint, no write
      return;
    }
    setCustomRecurErr(false);
    setCustomRecur(null);
    commitRecurrence(rule);
  };

  // --- subtasks (depth capped at 1: a child never shows its own section) ----
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
    // A subtask inherits nothing but the parent link.
    void store.create({ title: t, parentId: task.id }).catch(() => {});
  };

  return (
    <div
      data-testid="detail-panel"
      // App.tsx's global handler skips Escape while focus is in an input or
      // textarea (so typing "Escape"-adjacent keys elsewhere isn't affected),
      // which left this panel's own fields unable to close it at all — the
      // close button's "esc" hint was a lie whenever a field had focus.
      onKeyDown={(e) => e.key === "Escape" && onClose()}
      style={{ width: width ?? 340 }}
      className="relative flex h-full shrink-0 flex-col border-l border-line bg-surface"
    >
      {resizeHandle}
      <div className="flex items-center justify-between border-b border-line px-3 py-2">
        <span className="font-mono text-[11px] text-faint">{shortId(task.id)}</span>
        <button
          onClick={onClose}
          aria-label="Close details"
          className="rounded px-1.5 font-mono text-[13px] text-mute hover:text-ink"
        >
          esc
        </button>
      </div>

      <div className="min-h-0 flex-1 space-y-4 overflow-y-auto p-3">
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

        {/* A subtask points back at its parent instead of carrying its own
            Subtasks section (hierarchy is capped at one level). */}
        {isChild && (
          <button
            data-testid="parent-breadcrumb"
            onClick={() => onOpenTask?.(task.parentId)}
            title="Open parent task"
            className="relative z-10 flex max-w-full items-center gap-1 font-mono text-[11px] text-mute hover:text-accent"
          >
            <span aria-hidden>↳</span>
            <span className="truncate">{parent?.title ?? shortId(task.parentId)}</span>
          </button>
        )}

        <input
          value={title}
          disabled={synced}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={() =>
            title.trim() !== task.title &&
            title.trim() !== "" &&
            commitEdit({ title: title.trim() }, { title: task.title })
          }
          onKeyDown={(e) => e.key === "Enter" && (e.target as HTMLInputElement).blur()}
          aria-label="Title"
          className="w-full bg-transparent text-[15px] font-medium leading-6 focus:outline-none disabled:text-mute"
        />

        <div>
          <FieldLabel>due</FieldLabel>
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
              className="rounded border border-line bg-paper px-1.5 py-1 font-mono text-[12px] focus:outline-none disabled:text-mute"
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

        {/* Recurrence is user-owned and local-only — the server rejects it on
            synced tasks, so the editor simply isn't offered there. */}
        {!synced && (
          <div data-testid="recurrence-editor">
            <FieldLabel>repeats</FieldLabel>
            <div className="flex items-center gap-2">
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
                className="rounded border border-line bg-paper px-1.5 py-1 font-mono text-[12px] focus:outline-none"
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
                  className="inline-flex min-w-0 items-center gap-1 font-mono text-[11px] text-mute"
                >
                  <Icon name="repeat" size={11} />
                  <span className="truncate">{humanize(task.recurrence)}</span>
                </span>
              )}
            </div>
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
          <FieldLabel>labels</FieldLabel>
          <div className="flex flex-wrap items-center gap-1.5">
            {task.labels.map((l) => (
              <Chip
                key={l}
                label={l}
                onRemove={() =>
                  commitEdit({ labels: task.labels.filter((x) => x !== l) }, { labels: task.labels })
                }
              />
            ))}
            <input
              value={newLabel}
              onChange={(e) => setNewLabel(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && newLabel.trim() !== "") {
                  commitEdit({ labels: [...task.labels, newLabel.trim()] }, { labels: task.labels });
                  setNewLabel("");
                }
              }}
              placeholder="+ label"
              aria-label="Add label"
              className="w-20 bg-transparent font-mono text-[11px] placeholder:text-faint focus:outline-none"
            />
          </div>
        </div>

        <div>
          <FieldLabel>notes</FieldLabel>
          <textarea
            value={notes}
            onChange={(e) => setNotes(e.target.value)}
            onBlur={() => notes !== task.notes && commitEdit({ notes }, { notes: task.notes })}
            rows={4}
            placeholder="Anything worth remembering…"
            aria-label="Notes"
            className="w-full resize-y rounded border border-line bg-paper px-2 py-1.5 text-[13px] leading-5 placeholder:text-faint focus:outline-none"
          />
        </div>

        {/* Subtasks: the open children from the replica. Offered on any
            non-child task — including a synced parent (a local checklist under
            a PR is supported); the children themselves are always local. A
            child task shows the parent breadcrumb (top) instead. */}
        {!isChild && (
          <div data-testid="subtasks-section">
            <FieldLabel>
              subtasks{children.length > 0 ? ` · ${children.length}` : ""}
            </FieldLabel>
            <div className="space-y-1">
              {children.map((c) => (
                <div key={c.id} data-testid="subtask-row" className="group/sub flex items-center gap-2">
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

        <dl className="space-y-1 border-t border-line pt-3 font-mono text-[11px] text-faint">
          {created && <MetaRow k="created" v={fmtStamp(created)} />}
          {updated && <MetaRow k="updated" v={fmtStamp(updated)} />}
          <MetaRow k="revision" v={String(task.revision)} />
          {task.externalRef !== "" && !refIsURL && <MetaRow k="ref" v={task.externalRef} />}
        </dl>
      </div>

      <div className="flex items-center justify-between border-t border-line px-3 py-2">
        {/* Completion is source-owned on synced tasks — both Complete and
            Reopen are disabled there (the banner above already says so). */}
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
        {/* Delete matches the list/palette paths: an optimistic delete with an
            Undo toast (no confirm), via lib/actions.deleteTaskWithUndo. */}
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
