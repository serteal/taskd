import { useState } from "react";
import { useNow, useSnapshot, useStore } from "../lib/hooks";
import { countView, sameView, viewTitle, type View } from "../lib/views";
import { chipParts } from "../lib/format";
import { registry, useRegistry } from "../lib/extensions";
import { addLabel, setProject } from "../lib/actions";
import { readTaskId } from "../lib/dnd";
import { savedViews, useSavedViews, type SavedView } from "../lib/savedviews";
import { savedFilters, useSavedFilters, type SavedFilter } from "../lib/filters";
import { useExtensions, isSourcePaused } from "../lib/admin";
import { Icon } from "./icons";
import { GearIcon } from "./Settings";

// Everything below the fixed views is computed from the live replica —
// projects are the "project:" labels in use, sources are whatever syncers
// exist. Using a label makes it appear here; that is the whole feature.
export function Sidebar({
  view,
  onNavigate,
  onAddTask,
  onApplySaved,
  onNewFilter,
  onEditFilter,
  onOpenSettings,
  collapsed,
  onToggleCollapsed,
  width,
  resizeHandle,
}: {
  view: View;
  onNavigate: (v: View) => void;
  onAddTask: () => void;
  onApplySaved: (v: SavedView) => void;
  onNewFilter: () => void;
  onEditFilter: (f: SavedFilter) => void;
  onOpenSettings: () => void;
  collapsed: boolean;
  onToggleCollapsed: () => void;
  /** Expanded width in px (the drag-resizable preference). */
  width: number;
  /** The drag handle on the right edge, rendered only while expanded. */
  resizeHandle?: React.ReactNode;
}) {
  const snap = useSnapshot();
  const store = useStore();
  const now = useNow();
  const saved = useSavedViews();
  const filters = useSavedFilters();
  const { exts } = useExtensions();
  useRegistry(); // re-render as extensions register views
  const tasks = [...snap.tasks.values()];

  // Drop a dragged task onto a project → move it there; onto a label → add it.
  // The action itself now shows the toast (with Undo, ⌘Z-reachable) — no
  // separate, non-undoable notice here.
  const dropOnProject = (project: string) => (taskId: string) => {
    const t = snap.tasks.get(taskId);
    if (!t) return;
    setProject(store, t, project);
  };
  const dropOnLabel = (label: string) => (taskId: string) => {
    const t = snap.tasks.get(taskId);
    if (!t || t.labels.includes(label)) return;
    addLabel(store, t, label);
  };

  // Built-in list badges include promoted (showIn) tasks — one shared helper
  // with App's header count so the two can't drift.
  const count = (v: View) => countView(tasks, v, now, filters);

  // Labels + projects are LOCAL-only: a syncer-applied label (e.g. `bug`,
  // `calendar`) must not dominate these sections. Synced items stay reachable
  // via SOURCES and via filters; a label with no local task simply disappears.
  const labelCounts = new Map<string, number>();
  const sourceCounts = new Map<string, number>();
  for (const t of tasks) {
    if (t.source === "") {
      for (const l of t.labels) labelCounts.set(l, (labelCounts.get(l) ?? 0) + 1);
    } else {
      sourceCounts.set(t.source, (sourceCounts.get(t.source) ?? 0) + 1);
    }
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

  // Collapsed: a slim icon rail — just enough to expand back and start a
  // task. Dynamic content (projects, labels, filters, sources) needs real
  // width to be legible, so it simply isn't shown until expanded again,
  // rather than trying to force it into icons that don't exist for it.
  if (collapsed) {
    return (
      <aside
        data-testid="sidebar-collapsed"
        className="flex w-12 shrink-0 flex-col items-center gap-2 border-r border-line bg-surface py-3"
      >
        <button
          onClick={onToggleCollapsed}
          aria-label="Expand sidebar"
          title="Expand sidebar"
          className="rounded-md p-1.5 text-mute hover:bg-ink/[.05] hover:text-ink dark:hover:bg-ink/[.08]"
        >
          <Icon name="chevron-right" size={14} />
        </button>
        <button
          onClick={onAddTask}
          aria-label="Add task"
          title="Add task"
          className="flex h-[26px] w-[26px] items-center justify-center rounded-full bg-accent text-white hover:bg-accent/90"
        >
          <Icon name="plus" size={13} strokeWidth={2.5} />
        </button>
      </aside>
    );
  }

  return (
    <aside
      data-testid="sidebar"
      style={{ width }}
      className="relative flex shrink-0 flex-col border-r border-line bg-surface"
    >
      {resizeHandle}
      <div className="flex items-center justify-between px-3 pb-3 pt-4">
        <span className="font-mono text-[15px] font-medium tracking-tight">
          taskd<span className="caret text-accent">_</span>
        </span>
        <button
          onClick={onToggleCollapsed}
          aria-label="Collapse sidebar"
          title="Collapse sidebar"
          className="rounded-md p-1 text-faint hover:bg-ink/[.05] hover:text-ink dark:hover:bg-ink/[.08]"
        >
          <Icon name="chevron-right" size={13} className="rotate-180" />
        </button>
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

        {/* Filters are the promotion mechanism: a saved predicate over the
            whole replica, pinned as a surface. Unlike the built-in lists it
            can pull synced items in. Always shown so "+ New filter" is
            discoverable. */}
        <SideSection title="filters">
          {filters.map((f) => (
            <li
              key={f.id}
              data-testid="saved-filter"
              data-filter-name={f.name}
              className="group/fl flex items-center"
            >
              <button
                onClick={() => onNavigate({ kind: "filter", id: f.id })}
                className={`flex-1 truncate px-3 py-[5px] text-left text-[13px] ${
                  sameView(view, { kind: "filter", id: f.id })
                    ? "bg-accent/10 font-medium text-accent"
                    : "text-ink hover:bg-ink/[.04] dark:hover:bg-ink/[.07]"
                }`}
              >
                {f.name}
              </button>
              <button
                onClick={() => onEditFilter(f)}
                aria-label={`Edit filter ${f.name}`}
                className="invisible px-1 text-mute hover:text-ink group-hover/fl:visible"
              >
                <Icon name="pencil" size={12} />
              </button>
              <button
                onClick={() => savedFilters.remove(f.id)}
                aria-label={`Remove filter ${f.name}`}
                className="invisible px-2 text-mute hover:text-warn group-hover/fl:visible"
              >
                ×
              </button>
            </li>
          ))}
          <li>
            <button
              onClick={onNewFilter}
              data-testid="new-filter"
              className="flex w-full items-center gap-1.5 px-3 py-[5px] text-left text-[13px] text-mute hover:bg-ink/[.04] hover:text-ink dark:hover:bg-ink/[.07]"
            >
              <Icon name="plus" size={12} strokeWidth={2.5} />
              New filter
            </button>
          </li>
        </SideSection>

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

        {/* Sources are quarantined feeds: synced items (calendar events,
            issues) live here, not in the built-in lists. A source appears once
            it has items; clicking it opens that feed. */}
        {sources.length > 0 && (
          <SideSection title="sources" caption="synced feeds">
            {sources.map((s) => (
              <SideItem
                key={s}
                label={s}
                mono
                count={sourceCounts.get(s)}
                paused={isSourcePaused(exts, s)}
                active={sameView(view, { kind: "source", source: s })}
                onClick={() => onNavigate({ kind: "source", source: s })}
              />
            ))}
          </SideSection>
        )}
      </nav>

      <div className="border-t border-line">
        <button
          onClick={onOpenSettings}
          data-testid="open-settings"
          className="flex w-full items-center gap-2 px-3 py-2 text-left text-[13px] text-mute hover:bg-ink/[.04] hover:text-ink dark:hover:bg-ink/[.07]"
        >
          <GearIcon size={15} />
          Settings
        </button>
      </div>
    </aside>
  );
}

function SideSection({
  title,
  caption,
  children,
}: {
  title: string;
  caption?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="mt-4" data-testid={`sidebar-section-${title}`}>
      <div className="flex items-baseline gap-2 px-3 pb-1">
        <span className="font-mono text-[10px] uppercase tracking-[0.16em] text-faint">{title}</span>
        {caption && <span className="text-[10px] lowercase tracking-normal text-faint/70">{caption}</span>}
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
  paused,
  onClick,
  onDropTask,
}: {
  label: string;
  count?: number;
  active: boolean;
  mono?: boolean;
  /** A source whose extension is disabled: shows a muted pill + dimmed count. */
  paused?: boolean;
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
        data-paused={paused ? "true" : undefined}
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
        className={`flex w-full items-center justify-between gap-2 px-3 py-[5px] text-left text-[13px] ${
          dragOver
            ? "bg-accent/20 text-accent ring-1 ring-inset ring-accent/40"
            : active
              ? "bg-accent/10 font-medium text-accent"
              : "text-ink hover:bg-ink/[.04] dark:hover:bg-ink/[.07]"
        } ${mono ? "font-mono text-[12px]" : ""}`}
      >
        <span className="flex min-w-0 items-center gap-1.5">
          <span className="truncate">{label}</span>
          {paused && (
            <span
              data-testid="source-paused-pill"
              className="shrink-0 rounded border border-line px-1 py-px font-sans text-[9px] uppercase tracking-wide text-faint"
            >
              paused
            </span>
          )}
        </span>
        {count !== undefined && count > 0 && (
          <span
            className={`font-mono text-[11px] ${
              paused ? "text-faint/50" : active ? "text-accent" : "text-faint"
            }`}
          >
            {count}
          </span>
        )}
      </button>
    </li>
  );
}
