import { useState } from "react";
import { useNow, useSnapshot, useStore } from "../lib/hooks";
import { matchesView, sameView, viewTitle, type View } from "../lib/views";
import { chipParts } from "../lib/format";
import { registry, useRegistry } from "../lib/extensions";
import { addLabel, setProject } from "../lib/actions";
import { notify } from "../lib/notify";
import { readTaskId } from "../lib/dnd";
import { savedViews, useSavedViews, type SavedView } from "../lib/savedviews";
import { Icon } from "./icons";

// Everything below the fixed views is computed from the live replica —
// projects are the "project:" labels in use, sources are whatever syncers
// exist. Using a label makes it appear here; that is the whole feature.
export function Sidebar({
  view,
  onNavigate,
  onAddTask,
  onApplySaved,
  dark,
  onToggleTheme,
}: {
  view: View;
  onNavigate: (v: View) => void;
  onAddTask: () => void;
  onApplySaved: (v: SavedView) => void;
  dark: boolean;
  onToggleTheme: () => void;
}) {
  const snap = useSnapshot();
  const store = useStore();
  const now = useNow();
  const saved = useSavedViews();
  useRegistry(); // re-render as extensions register views
  const tasks = [...snap.tasks.values()];

  // Drop a dragged task onto a project → move it there; onto a label → add it.
  const dropOnProject = (project: string) => (taskId: string) => {
    const t = snap.tasks.get(taskId);
    if (!t) return;
    setProject(store, t, project);
    notify.toast({ message: `Moved to ${chipParts(project).val}` });
  };
  const dropOnLabel = (label: string) => (taskId: string) => {
    const t = snap.tasks.get(taskId);
    if (!t || t.labels.includes(label)) return;
    addLabel(store, t, label);
    notify.toast({ message: `Added ${label}` });
  };

  const count = (v: View) => tasks.filter((t) => matchesView(t, v, now)).length;

  const labelCounts = new Map<string, number>();
  const sourceCounts = new Map<string, number>();
  for (const t of tasks) {
    for (const l of t.labels) labelCounts.set(l, (labelCounts.get(l) ?? 0) + 1);
    if (t.source !== "") sourceCounts.set(t.source, (sourceCounts.get(t.source) ?? 0) + 1);
  }
  const projects = [...labelCounts.keys()].filter((l) => chipParts(l).ns === "project").sort();
  const plain = [...labelCounts.keys()].filter((l) => chipParts(l).ns !== "project").sort();
  const sources = [...sourceCounts.keys()].sort();

  const fixed: View[] = [
    { kind: "inbox" },
    { kind: "today" },
    { kind: "upcoming" },
    { kind: "all" },
    { kind: "completed" },
  ];

  return (
    <aside className="flex w-52 shrink-0 flex-col border-r border-line bg-surface">
      <div className="px-3 pb-3 pt-4 font-mono text-[15px] font-medium tracking-tight">
        taskd<span className="caret text-accent">_</span>
      </div>

      <div className="px-2 pb-2">
        <button
          onClick={onAddTask}
          className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] font-medium text-accent hover:bg-accent/10"
        >
          <span className="flex h-[17px] w-[17px] items-center justify-center rounded-full bg-accent text-white">
            <Icon name="plus" size={12} strokeWidth={2.5} />
          </span>
          Add task
          <span className="ml-auto font-mono text-[11px] text-faint">q</span>
        </button>
      </div>

      <nav className="min-h-0 flex-1 overflow-y-auto pb-4">
        <ul>
          {fixed.map((v) => (
            <SideItem
              key={viewTitle(v)}
              label={viewTitle(v)}
              count={v.kind === "completed" ? undefined : count(v)}
              active={sameView(view, v)}
              onClick={() => onNavigate(v)}
            />
          ))}
          {registry.views.map((v) => (
            <SideItem
              key={`ext-${v.id}`}
              label={v.title}
              active={view.kind === "ext" && view.id === v.id}
              onClick={() => onNavigate({ kind: "ext", id: v.id })}
            />
          ))}
        </ul>

        {saved.length > 0 && (
          <SideSection title="views">
            {saved.map((v) => (
              <li
                key={v.id}
                data-testid="saved-view"
                data-view-name={v.name}
                className="group/sv flex items-center"
              >
                <button
                  onClick={() => onApplySaved(v)}
                  className="flex-1 truncate px-3 py-[5px] text-left text-[13px] text-ink hover:bg-ink/[.04] dark:hover:bg-ink/[.07]"
                >
                  {v.name}
                </button>
                <button
                  onClick={() => savedViews.remove(v.id)}
                  aria-label={`Remove view ${v.name}`}
                  className="invisible px-2 text-mute hover:text-warn group-hover/sv:visible"
                >
                  ×
                </button>
              </li>
            ))}
          </SideSection>
        )}

        {projects.length > 0 && (
          <SideSection title="projects">
            {projects.map((l) => (
              <SideItem
                key={l}
                label={chipParts(l).val}
                count={labelCounts.get(l)}
                active={sameView(view, { kind: "label", label: l })}
                onClick={() => onNavigate({ kind: "label", label: l })}
                onDropTask={dropOnProject(l)}
              />
            ))}
          </SideSection>
        )}

        {plain.length > 0 && (
          <SideSection title="labels">
            {plain.map((l) => (
              <SideItem
                key={l}
                label={l}
                count={labelCounts.get(l)}
                active={sameView(view, { kind: "label", label: l })}
                onClick={() => onNavigate({ kind: "label", label: l })}
                onDropTask={dropOnLabel(l)}
              />
            ))}
          </SideSection>
        )}

        {sources.length > 0 && (
          <SideSection title="sources">
            {sources.map((s) => (
              <SideItem
                key={s}
                label={s}
                mono
                count={sourceCounts.get(s)}
                active={sameView(view, { kind: "source", source: s })}
                onClick={() => onNavigate({ kind: "source", source: s })}
              />
            ))}
          </SideSection>
        )}
      </nav>

      <button
        onClick={onToggleTheme}
        className="border-t border-line px-3 py-2 text-left font-mono text-[11px] text-mute hover:text-ink"
      >
        theme: {dark ? "dusk" : "paper"}
      </button>
    </aside>
  );
}

function SideSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mt-4" data-testid={`sidebar-section-${title}`}>
      <div className="px-3 pb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-faint">
        {title}
      </div>
      <ul>{children}</ul>
    </div>
  );
}

function SideItem({
  label,
  count,
  active,
  mono,
  onClick,
  onDropTask,
}: {
  label: string;
  count?: number;
  active: boolean;
  mono?: boolean;
  onClick: () => void;
  /** When set, a dragged task dropped here is passed by id. */
  onDropTask?: (taskId: string) => void;
}) {
  const [dragOver, setDragOver] = useState(false);
  return (
    <li>
      <button
        onClick={onClick}
        data-testid="side-item"
        data-label={label}
        onDragOver={
          onDropTask
            ? (e) => {
                e.preventDefault();
                if (!dragOver) setDragOver(true);
              }
            : undefined
        }
        onDragLeave={onDropTask ? () => setDragOver(false) : undefined}
        onDrop={
          onDropTask
            ? (e) => {
                e.preventDefault();
                setDragOver(false);
                const id = readTaskId(e.dataTransfer);
                if (id) onDropTask(id);
              }
            : undefined
        }
        className={`flex w-full items-center justify-between px-3 py-[5px] text-left text-[13px] ${
          dragOver
            ? "bg-accent/20 text-accent ring-1 ring-inset ring-accent/40"
            : active
              ? "bg-accent/10 font-medium text-accent"
              : "text-ink hover:bg-ink/[.04] dark:hover:bg-ink/[.07]"
        } ${mono ? "font-mono text-[12px]" : ""}`}
      >
        <span className="truncate">{label}</span>
        {count !== undefined && count > 0 && (
          <span className={`font-mono text-[11px] ${active ? "text-accent" : "text-faint"}`}>
            {count}
          </span>
        )}
      </button>
    </li>
  );
}
