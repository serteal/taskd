import type { Task } from "../gen/task/task_pb";
import type { TaskStore } from "./store";
import type { Command } from "./extensions";
import type { View, SortMode } from "./views";
import { chipParts, endOfDay } from "./format";
import { SORT_LABELS } from "./views";
import { completeTask, deleteTaskWithUndo, rescheduleTask } from "./actions";

// Builds the ⌘K palette's command list from the current app state. The palette
// filters/ranks these by the query; task search is added separately from the
// query since it depends on it.

export interface PaletteCommand {
  id: string;
  title: string;
  group: string;
  icon?: string;
  keywords?: string;
  run: () => void;
}

export interface CommandContext {
  tasks: Task[];
  store: TaskStore;
  selectedId: string | null;
  navigate: (v: View) => void;
  setSort: (s: SortMode) => void;
  toggleTheme: () => void;
  panels: { id: string; title: string }[];
  togglePanel: (id: string) => void;
  openTask: (id: string) => void;
  openAdd: () => void;
  saveCurrentView: () => void;
  extCommands: Command[];
  now: Date;
}

// Static candidates (navigation, global actions, task actions, extensions).
// Task-search results are appended by the palette from the live query.
export function buildStaticCommands(ctx: CommandContext): PaletteCommand[] {
  const out: PaletteCommand[] = [];

  // Global
  out.push({ id: "new-task", title: "New task", group: "Actions", icon: "+", keywords: "add create", run: ctx.openAdd });
  out.push({ id: "save-view", title: "Save current view…", group: "Actions", icon: "★", keywords: "pin bookmark filter", run: ctx.saveCurrentView });
  out.push({ id: "theme", title: "Toggle theme", group: "Actions", icon: "◐", keywords: "dark light", run: ctx.toggleTheme });
  for (const p of ctx.panels) {
    out.push({
      id: `panel-${p.id}`,
      title: `Toggle ${p.title.toLowerCase()} panel`,
      group: "Actions",
      icon: "▦",
      run: () => ctx.togglePanel(p.id),
    });
  }
  (["smart", "manual", "created", "title"] as SortMode[]).forEach((s) =>
    out.push({
      id: `sort-${s}`,
      title: `Sort: ${SORT_LABELS[s]}`,
      group: "Actions",
      icon: "↕",
      run: () => ctx.setSort(s),
    }),
  );

  // Navigation — fixed views
  const fixed: [string, View][] = [
    ["Inbox", { kind: "inbox" }],
    ["Today", { kind: "today" }],
    ["Upcoming", { kind: "upcoming" }],
    ["All tasks", { kind: "all" }],
    ["Completed", { kind: "completed" }],
  ];
  for (const [title, v] of fixed) {
    out.push({ id: `go-${title}`, title: `Go to ${title}`, group: "Go to", icon: "→", run: () => ctx.navigate(v) });
  }
  // Navigation — computed projects / labels / sources
  const labels = new Set<string>();
  const sources = new Set<string>();
  for (const t of ctx.tasks) {
    for (const l of t.labels) labels.add(l);
    if (t.source) sources.add(t.source);
  }
  for (const l of [...labels].sort()) {
    const parts = chipParts(l);
    out.push({
      id: `go-label-${l}`,
      title: parts.ns === "project" ? `Go to project ${parts.val}` : `Go to label ${l}`,
      group: "Go to",
      icon: parts.ns === "project" ? "#" : "🏷",
      keywords: l,
      run: () => ctx.navigate({ kind: "label", label: l }),
    });
  }
  for (const s of [...sources].sort()) {
    out.push({
      id: `go-source-${s}`,
      title: `Go to ${s}`,
      group: "Go to",
      icon: "⇄",
      run: () => ctx.navigate({ kind: "source", source: s }),
    });
  }

  // Actions on the currently selected task
  const sel = ctx.selectedId ? ctx.tasks.find((t) => t.id === ctx.selectedId) : undefined;
  if (sel) {
    const label = sel.title.length > 24 ? sel.title.slice(0, 23) + "…" : sel.title;
    out.push({ id: "sel-open", title: `Open “${label}”`, group: "Selected task", icon: "↳", run: () => ctx.openTask(sel.id) });
    out.push({ id: "sel-done", title: `Complete “${label}”`, group: "Selected task", icon: "✓", run: () => completeTask(ctx.store, sel) });
    out.push({
      id: "sel-today",
      title: `Schedule “${label}” today`,
      group: "Selected task",
      icon: "📅",
      run: () => rescheduleTask(ctx.store, sel, endOfDay(ctx.now)),
    });
    out.push({ id: "sel-delete", title: `Delete “${label}”`, group: "Selected task", icon: "🗑", run: () => deleteTaskWithUndo(ctx.store, sel) });
  }

  // Extension-contributed commands
  for (const c of ctx.extCommands) {
    if (c.when && !c.when()) continue;
    out.push({
      id: `ext-${c.id}`,
      title: c.title,
      group: c.group ?? "Extensions",
      icon: c.icon ?? "◆",
      keywords: c.keywords,
      run: c.run,
    });
  }

  return out;
}

// Case-insensitive, all-words-must-appear match; lower score ranks first.
export function scoreMatch(text: string, query: string): number {
  const t = text.toLowerCase();
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (words.length === 0) return 0;
  let score = 0;
  for (const w of words) {
    const i = t.indexOf(w);
    if (i === -1) return -1;
    score += i;
  }
  return score;
}

// Tasks matching the query, as "open" commands (only when a query is present).
export function searchTaskCommands(ctx: CommandContext, query: string): PaletteCommand[] {
  if (query.trim() === "") return [];
  return ctx.tasks
    .map((t) => ({ t, s: scoreMatch(`${t.title} ${t.notes} ${t.labels.join(" ")}`, query) }))
    .filter((x) => x.s >= 0)
    .sort((a, b) => a.s - b.s)
    .slice(0, 8)
    .map(({ t }) => ({
      id: `task-${t.id}`,
      title: t.title,
      group: "Tasks",
      icon: "○",
      run: () => ctx.openTask(t.id),
    }));
}
