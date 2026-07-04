import { useEffect, useMemo, useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { useStore } from "../lib/hooks";
import { fmtStamp, shortId, toLocalInput, tsDate } from "../lib/format";
import { buildAPI, registry } from "../lib/extensions";
import { Chip } from "./Chip";
import { ExtensionBoundary } from "./ExtensionBoundary";

// Field edits save individually (blur/Enter) with the task's revision as
// expected_revision; a conflict just means the replica already shows the
// newer truth — the panel re-renders from it and the toast says so.
export function DetailPanel({ task, onClose }: { task: Task; onClose: () => void }) {
  const store = useStore();
  const synced = task.source !== "";
  const [title, setTitle] = useState(task.title);
  const [notes, setNotes] = useState(task.notes);
  const [newLabel, setNewLabel] = useState("");

  // The replica is the source of truth: whenever this task changes under
  // us (watch event, other window), reset unsaved field state to it.
  useEffect(() => {
    setTitle(task.title);
    setNotes(task.notes);
  }, [task]);

  const save = (patch: Parameters<typeof store.update>[1]) =>
    store.update(task.id, { ...patch, expectedRevision: task.revision }).catch(() => {});

  const due = tsDate(task.dueTime);
  const created = tsDate(task.createTime);
  const updated = tsDate(task.updateTime);
  const refIsURL = /^https?:\/\//.test(task.externalRef);
  const data = task.externalData ?? {};
  const dataEntries = Object.entries(data);
  const presenter = registry.presenterFor(task);
  const api = useMemo(() => buildAPI(store), [store]);

  return (
    <div className="flex h-full w-[340px] shrink-0 flex-col border-l border-line bg-surface">
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

        <input
          value={title}
          disabled={synced}
          onChange={(e) => setTitle(e.target.value)}
          onBlur={() => title.trim() !== task.title && title.trim() !== "" && save({ title })}
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
                save({ due: e.target.value === "" ? null : new Date(e.target.value) })
              }
              aria-label="Due date"
              className="rounded border border-line bg-paper px-1.5 py-1 font-mono text-[12px] focus:outline-none disabled:text-mute"
            />
            {due && !synced && (
              <button onClick={() => save({ due: null })} className="text-[11px] text-mute hover:text-warn">
                clear
              </button>
            )}
          </div>
        </div>

        <div>
          <FieldLabel>labels</FieldLabel>
          <div className="flex flex-wrap items-center gap-1.5">
            {task.labels.map((l) => (
              <Chip
                key={l}
                label={l}
                onRemove={() => save({ labels: task.labels.filter((x) => x !== l) })}
              />
            ))}
            <input
              value={newLabel}
              onChange={(e) => setNewLabel(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && newLabel.trim() !== "") {
                  save({ labels: [...task.labels, newLabel.trim()] });
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
            onBlur={() => notes !== task.notes && save({ notes })}
            rows={4}
            placeholder="Anything worth remembering…"
            aria-label="Notes"
            className="w-full resize-y rounded border border-line bg-paper px-2 py-1.5 text-[13px] leading-5 placeholder:text-faint focus:outline-none"
          />
        </div>

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
        <button
          onClick={() =>
            save({ completed: true }).then(onClose)
          }
          className="rounded border border-accent/50 px-2 py-1 text-[12px] text-accent hover:bg-accent/10"
        >
          Complete
        </button>
        <button
          onClick={() => {
            if (window.confirm(`Delete "${task.title}"? This cannot be undone.`)) {
              store.delete(task.id).catch(() => {});
              onClose();
            }
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
