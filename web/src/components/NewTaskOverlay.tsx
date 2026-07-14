import type { ReactNode } from "react";
import { useEffect, useMemo, useRef, useState } from "react";
import { useSnapshot, useStore } from "../lib/hooks";
import { parseQuickAdd, tokenSpans, type TokenKind, type TokenSpan } from "../lib/quickadd";
import { registry, useRegistry } from "../lib/extensions";
import { chipParts, humanDue } from "../lib/format";
import { humanize } from "../lib/recur";
import { Chip } from "./Chip";
import { Popover, PillButton } from "./Popover";
import { ScheduleMenu, PriorityMenu, LabelMenu, ProjectMenu } from "./pickers";
import { Icon } from "./icons";
import { useTitleTypeahead } from "./TitleTypeahead";

// The single "new task" surface — a modal that replaced the old inline bar.
// The title field parses quick-add tokens live (#label, p1-3, dates), and the
// pills below show and override the parsed values, Todoist-style. Everything
// maps to our model: priority and project are labels, "schedule" is due_time.

type Override<T> = T | undefined; // undefined = inherit from the parsed title

const PRIORITY_RE = /^p[1-3]$/;
const isProject = (l: string) => l.startsWith("project:");

// Shared between the (invisible) textarea and the highlight div behind it —
// any mismatch here and the two texts stop lining up.
const TITLE_TEXT_CLASS = "text-[19px] font-semibold leading-tight";

const TOKEN_CLASS: Record<TokenKind, string> = {
  label: "rounded-sm bg-accent/15 text-accent",
  priority: "rounded-sm bg-accent/15 text-accent",
  due: "rounded-sm bg-warn/15 text-warn",
  ext: "rounded-sm bg-accent/15 text-accent",
  recur: "rounded-sm bg-warn/15 text-warn",
  mention: "rounded-sm bg-accent/20 text-accent",
};

// Todoist-style live formatting: recognized quick-add tokens get a tinted
// background as you type. Renders into a div stacked behind a text-transparent
// textarea (the classic contenteditable-highlight trick — the real textarea
// stays the actual input/caret/selection, this is purely decorative).
function renderHighlighted(text: string, spans: TokenSpan[]): ReactNode {
  const nodes: ReactNode[] = [];
  let cursor = 0;
  spans.forEach((s, i) => {
    if (s.start > cursor) nodes.push(text.slice(cursor, s.start));
    nodes.push(
      <span key={i} data-testid="quickadd-token" data-kind={s.kind} className={TOKEN_CLASS[s.kind]}>
        {text.slice(s.start, s.end)}
      </span>,
    );
    cursor = s.end;
  });
  nodes.push(text.slice(cursor));
  // A trailing newline collapses to nothing in a plain <div>; a textarea
  // still reserves the blank line for it. A zero-width space preserves it.
  if (text.endsWith("\n")) nodes.push("​");
  return nodes;
}

// Grow a textarea to fit its content (wrapped lines + explicit newlines). The
// CSS caps the height, so past the cap it scrolls instead of pushing the modal.
function autoGrow(el: HTMLTextAreaElement | null): void {
  if (!el) return;
  el.style.height = "auto";
  el.style.height = `${el.scrollHeight}px`;
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
  // Seed the due override as "inherit" (undefined), NOT from initial.due — a
  // view-derived default (e.g. Today prefills today's date) must NOT beat a
  // date typed into the title. Precedence: explicit chip interaction (pick /
  // clear ×) > typed date token > view default (see `due` below).
  const [dueOv, setDueOv] = useState<Override<Date | null>>(undefined);
  // Recurrence comes only from a typed phrase ("every 3 days", "daily"); the
  // chip's × is the sole override (clear), so the only override value is null.
  const [recurOv, setRecurOv] = useState<Override<null>>(undefined);
  const [prioOv, setPrioOv] = useState<Override<string | null>>(undefined);
  const [labelsOv, setLabelsOv] = useState<Override<string[]>>(undefined);
  const [projectOv, setProjectOv] = useState<Override<string | null>>(initial?.project ?? undefined);
  const titleRef = useRef<HTMLTextAreaElement>(null);
  const titleWrapRef = useRef<HTMLDivElement>(null);
  const descRef = useRef<HTMLTextAreaElement>(null);

  useEffect(() => titleRef.current?.focus(), []);
  // Keep both fields sized to their content as the user types or on reset.
  useEffect(() => {
    autoGrow(titleRef.current);
    // The highlight div behind the textarea is `absolute inset-0` so it can
    // stack under it exactly — but its wrapper's height is otherwise "auto",
    // and an absolutely positioned child of an auto-height parent resolves
    // circularly (Chromium falls back to the child's own content height,
    // which can differ from the textarea's by a few px). Pinning the
    // wrapper's height to the textarea's own resolved height breaks the tie.
    if (titleWrapRef.current && titleRef.current) {
      titleWrapRef.current.style.height = titleRef.current.style.height;
    }
  }, [title]);
  useEffect(() => autoGrow(descRef.current), [description]);

  const regVersion = useRegistry(); // pick up extension-contributed quick-add tokens
  const parsed = useMemo(
    () => parseQuickAdd(title, now, registry.quickAddTokens),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [title, now, regVersion],
  );
  const spans = useMemo(
    () => tokenSpans(title, now, registry.quickAddTokens),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [title, now, regVersion],
  );
  const parsedPriority = parsed.labels.find((l) => PRIORITY_RE.test(l)) ?? null;
  const parsedProject = parsed.labels.find(isProject) ?? null;
  const parsedLabels = parsed.labels.filter((l) => !PRIORITY_RE.test(l) && !isProject(l));

  const due = dueOv !== undefined ? dueOv : parsed.due ?? initial?.due ?? null;
  const recurrence = recurOv !== undefined ? recurOv : parsed.recurrence ?? null;
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

  // Inline "#label" / "@mention" autocomplete under the title field.
  const typeahead = useTitleTypeahead({
    ref: titleRef,
    value: title,
    onChange: setTitle,
    labels: useMemo(() => [...allLabels].sort(), [allLabels]),
  });

  const submit = () => {
    if (!canAdd) return;
    const finalLabels = [...(project ? [project] : []), ...(priority ? [priority] : []), ...labels];
    void store
      .create({
        title: parsed.title.trim(),
        notes: description.trim() || undefined,
        labels: finalLabels,
        due: due ?? undefined,
        recurrence: recurrence ?? undefined,
      })
      .catch(() => {});
    onClose();
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
          <div ref={titleWrapRef} className="relative">
            {/* Decorative twin behind the textarea — same text, same box, tokens
                tinted. aria-hidden since the textarea already carries the value. */}
            <div
              aria-hidden
              className={`pointer-events-none absolute inset-0 max-h-[40vh] overflow-hidden whitespace-pre-wrap break-words ${TITLE_TEXT_CLASS} text-ink`}
            >
              {renderHighlighted(title, spans)}
            </div>
            <textarea
              ref={titleRef}
              value={title}
              onChange={typeahead.onChange}
              onKeyDown={(e) => {
                if (typeahead.onKeyDown(e)) return; // the dropdown owns nav/accept keys
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  submit();
                }
              }}
              rows={1}
              placeholder="Task name  (#label · @mention · dates)"
              aria-label="Task name"
              className={`relative max-h-[40vh] w-full resize-none overflow-y-auto bg-transparent ${TITLE_TEXT_CLASS} text-transparent caret-ink placeholder:text-faint focus:outline-none`}
            />
            {typeahead.menu}
          </div>
          <textarea
            ref={descRef}
            value={description}
            onChange={(e) => setDescription(e.target.value)}
            rows={1}
            placeholder="Description"
            aria-label="Description"
            className="mt-1 max-h-[30vh] w-full resize-none overflow-y-auto bg-transparent text-[13.5px] leading-snug text-mute placeholder:text-faint focus:outline-none"
          />

          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Popover
              trigger={({ toggle }) => (
                <PillButton
                  icon={<Icon name="calendar" size={14} />}
                  label={due ? humanDue(due, now, { withTime: true }).text : "Schedule"}
                  active={!!due}
                  onClick={toggle}
                  onClear={due ? () => setDueOv(null) : undefined}
                />
              )}
            >
              {(close) => <ScheduleMenu now={now} onChange={(d) => setDueOv(d)} close={close} />}
            </Popover>

            {/* Recurrence chip — appears when a typed phrase set a rule. There
                is no picker (the natural grammar IS the input); × clears it. */}
            {recurrence && (
              <span data-testid="recurrence-chip">
                <PillButton
                  icon={<Icon name="repeat" size={13} />}
                  label={humanize(recurrence)}
                  active
                  tone="warn"
                  onClick={() => {}}
                  onClear={() => setRecurOv(null)}
                />
              </span>
            )}

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
