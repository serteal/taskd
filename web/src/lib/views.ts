import type { Task } from "../gen/task/task_pb";
import { chipParts, dayDiff, tsDate } from "./format";
import { matchesFilter, savedFilters, type SavedFilter } from "./filters";
import { onStorageChange, readVersionedJSON, writeVersionedJSON } from "./storage";

// A view is a pure filter over the replica (Completed is the exception: it
// pages the server, since the archive isn't replicated). Views live in the
// URL so they survive reloads and can be linked.
//
// "Inbox" follows the label model, not a schema: it is the active tasks
// that belong to no project — i.e. carry no "project:" label.
//
// The built-in lists (inbox/today/upcoming/all) are LOCAL-by-default: they
// show only tasks you created (`source === ""`). Synced items live in their
// Source view (an explicit request) and can be promoted onto a named surface
// by a `filter` view — or, with `showIn`, directly into Today/Inbox/Upcoming
// as a named section (see promotedSections). This is the sources≠tasks model —
// see DESIGN §5b.
//
// Today = local, due on or before today (overdue counts). Upcoming = local,
// due strictly after today — so the two lists partition dated tasks rather
// than overlapping.

export type View =
  | { kind: "inbox" }
  | { kind: "today" }
  | { kind: "upcoming" }
  | { kind: "all" }
  | { kind: "completed" }
  // Label views are local-by-default, matching the built-in lists. `synced=1`
  // (includeSynced) is the explicit escape hatch that also folds in the synced
  // tasks carrying the label — set from the view header's "+N synced" chip.
  | { kind: "label"; label: string; includeSynced?: boolean }
  | { kind: "source"; source: string }
  | { kind: "filter"; id: string }
  | { kind: "ext"; id: string };

// The three built-in lists a SavedFilter can promote its matches into. Kept
// narrow (not "all") so promotion targets a dated/time-pressure surface.
export type PromotableKind = "today" | "inbox" | "upcoming";

export function isPromotable(v: View): v is { kind: PromotableKind } {
  return v.kind === "today" || v.kind === "inbox" || v.kind === "upcoming";
}

export function parseView(search: string): View {
  const p = new URLSearchParams(search);
  const ext = p.get("ext");
  if (ext) return { kind: "ext", id: ext };
  const filter = p.get("filter");
  if (filter) return { kind: "filter", id: filter };
  const label = p.get("label");
  if (label) return { kind: "label", label, includeSynced: p.get("synced") === "1" };
  const source = p.get("source");
  if (source) return { kind: "source", source };
  switch (p.get("view")) {
    case "today":
      return { kind: "today" };
    case "upcoming":
      return { kind: "upcoming" };
    case "all":
      return { kind: "all" };
    case "completed":
      return { kind: "completed" };
    case "inbox":
      return { kind: "inbox" };
    default:
      // A bare load with no view param at all (bookmark, PWA launch, first
      // run) opens to the user's chosen startup view instead of a hardcoded one.
      return { kind: readDefaultView() };
  }
}

// --- default (startup) view -------------------------------------------------
//
// Which of the 5 fixed views a bare load (no query params) opens to. A plain
// client-side preference — like the sidebar-collapse state elsewhere in
// lib/ — not a per-view prefs entry (it's about which view you land on, not
// how that view is displayed).

export type DefaultView = "inbox" | "today" | "upcoming" | "all" | "completed";
export const DEFAULT_VIEWS: DefaultView[] = ["inbox", "today", "upcoming", "all", "completed"];

const DEFAULT_VIEW_KEY = "taskd-default-view";

export function readDefaultView(): DefaultView {
  try {
    const raw = localStorage.getItem(DEFAULT_VIEW_KEY);
    return (DEFAULT_VIEWS as string[]).includes(raw ?? "") ? (raw as DefaultView) : "today";
  } catch {
    return "today";
  }
}

export function writeDefaultView(v: DefaultView): void {
  try {
    localStorage.setItem(DEFAULT_VIEW_KEY, v);
  } catch {
    // storage full/blocked — preference stays session-local this run
  }
}

export function viewToSearch(v: View): string {
  const p = new URLSearchParams();
  switch (v.kind) {
    case "label":
      p.set("label", v.label);
      if (v.includeSynced) p.set("synced", "1");
      break;
    case "source":
      p.set("source", v.source);
      break;
    case "filter":
      p.set("filter", v.id);
      break;
    case "ext":
      p.set("ext", v.id);
      break;
    default:
      p.set("view", v.kind);
  }
  return `?${p.toString()}`;
}

export function viewTitle(v: View): string {
  switch (v.kind) {
    case "inbox":
      return "Inbox";
    case "today":
      return "Today";
    case "upcoming":
      return "Upcoming";
    case "all":
      return "All tasks";
    case "completed":
      return "Completed";
    case "label": {
      // Strip the "project:" namespace the same way the sidebar and the
      // command palette's "Go to project X" title already do, so the page
      // heading doesn't show the raw label after navigating there.
      const { ns, val } = chipParts(v.label);
      return ns === "project" ? val : v.label;
    }
    case "source":
      return v.source;
    case "filter":
      return savedFilters.get(v.id)?.name ?? "Filter";
    case "ext":
      return v.id;
  }
}

export function matchesView(t: Task, v: View, now: Date): boolean {
  switch (v.kind) {
    // The built-in lists are local-by-default: synced items (source != "")
    // are quarantined to their Source view and pulled in only by an explicit
    // filter. This is the sources≠tasks model.
    case "all":
      return t.source === "";
    case "inbox":
      return t.source === "" && !t.labels.some((l) => l.startsWith("project:"));
    case "today": {
      if (t.source !== "") return false;
      const due = tsDate(t.dueTime);
      return due !== undefined && dayDiff(due, now) <= 0;
    }
    case "upcoming": {
      // Strictly future: a local task dated after today. Today/overdue live in
      // the Today list, so Upcoming no longer double-counts them.
      if (t.source !== "") return false;
      const due = tsDate(t.dueTime);
      return due !== undefined && dayDiff(due, now) > 0;
    }
    // A label view is local-by-default like the built-in lists; `includeSynced`
    // (the header's "+N synced" chip) is the explicit opt-in that also shows the
    // synced tasks carrying the label. A source view is always an explicit
    // request, so it shows its synced feed unconditionally.
    case "label":
      return t.labels.includes(v.label) && (v.includeSynced === true || t.source === "");
    case "source":
      return t.source === v.source;
    case "filter": {
      // Resolve the SavedFilter and run its predicate over the full replica —
      // this is what lets a filter promote a chosen subset of synced items.
      const f = savedFilters.get(v.id);
      return f ? matchesFilter(f.predicate, t, now) : false;
    }
    case "completed":
      return false; // served by the server, not the replica
    case "ext":
      return false; // extension views render their own content
  }
}

export function sameView(a: View, b: View): boolean {
  return viewToSearch(a) === viewToSearch(b);
}

// --- promotion into built-in lists -----------------------------------------
//
// A SavedFilter with `showIn` including a built-in list kind promotes its
// matches onto that list as a named section, below the base (local) tasks.
// This is how a synced subset (e.g. reviews from github) becomes reachable in
// Today/Inbox/Upcoming without polluting the whole quarantine.

export interface PromotedSection {
  filter: SavedFilter;
  tasks: Task[];
}

/** For each saved filter whose `showIn` includes `kind`, the tasks matching its
 *  predicate that do NOT already match the base view (dedup). A task matching
 *  several promoting filters appears only in the first (stable by filter
 *  order), and never twice within a section. Empty sections are dropped. */
export function promotedSections(
  tasks: Task[],
  kind: PromotableKind,
  now: Date,
  filters: SavedFilter[] = savedFilters.getSnapshot(),
): PromotedSection[] {
  // Anything already in the base list — or already claimed by an earlier
  // promoting filter — is off the table for later sections.
  const claimed = new Set<string>();
  for (const t of tasks) if (matchesView(t, { kind }, now)) claimed.add(t.id);

  const out: PromotedSection[] = [];
  for (const f of filters) {
    if (!f.showIn?.includes(kind)) continue;
    const matched: Task[] = [];
    for (const t of tasks) {
      if (claimed.has(t.id)) continue;
      if (matchesFilter(f.predicate, t, now)) {
        matched.push(t);
        claimed.add(t.id);
      }
    }
    if (matched.length > 0) out.push({ filter: f, tasks: matched });
  }
  return out;
}

/** Count of promoted (non-base, deduped) tasks a built-in list gains. */
export function promotedCount(
  tasks: Task[],
  kind: PromotableKind,
  now: Date,
  filters?: SavedFilter[],
): number {
  return promotedSections(tasks, kind, now, filters).reduce((n, s) => n + s.tasks.length, 0);
}

/** The badge count for any view: base-view members plus, for a promotable
 *  built-in list, its promoted tasks. Single source of truth so the Sidebar
 *  badge and the header count can't drift. */
export function countView(tasks: Task[], v: View, now: Date, filters?: SavedFilter[]): number {
  const base = tasks.reduce((n, t) => n + (matchesView(t, v, now) ? 1 : 0), 0);
  return isPromotable(v) ? base + promotedCount(tasks, v.kind, now, filters) : base;
}

// How a list is ordered. "smart" is the default (soonest due, then newest);
// "manual" enables drag-to-reorder against user_data.order.
export type SortMode = "smart" | "manual" | "created" | "title";

export const SORT_MODES: SortMode[] = ["smart", "manual", "created", "title"];

export const SORT_LABELS: Record<SortMode, string> = {
  smart: "Smart",
  manual: "Manual",
  created: "Created",
  title: "Title",
};

// --- per-view presentation prefs (sticky across reloads) -------------------
//
// The list/board layout, sort, and group-by are a property of *how you look at
// a view*, not of the task data — so they live client-side in localStorage,
// keyed by the view's URL (viewToSearch). Each view remembers its own choice;
// a reload restores it. Applying a saved view overwrites the target view's
// stored prefs.

export type BoardGroupBy = "priority" | "project";

export interface ViewPrefs {
  sort: SortMode;
  board: boolean;
  groupBy: BoardGroupBy;
}

export const DEFAULT_VIEW_PREFS: ViewPrefs = { sort: "smart", board: false, groupBy: "priority" };

const VIEW_PREFS_KEY = "taskd-view-prefs";
const VIEW_PREFS_VERSION = 1;

type AllPrefs = Record<string, Partial<ViewPrefs>>;

function loadAllPrefs(): AllPrefs {
  // A legacy pre-envelope map is read back as-is and rewritten enveloped.
  return readVersionedJSON<AllPrefs>(VIEW_PREFS_KEY, { version: VIEW_PREFS_VERSION }) ?? {};
}

export function readViewPrefs(v: View): ViewPrefs {
  const stored = loadAllPrefs()[viewToSearch(v)];
  // Merge over defaults so a partial/old entry never yields undefined fields.
  return { ...DEFAULT_VIEW_PREFS, ...stored };
}

export function writeViewPrefs(v: View, prefs: ViewPrefs): void {
  const all = loadAllPrefs();
  all[viewToSearch(v)] = prefs;
  writeVersionedJSON(VIEW_PREFS_KEY, VIEW_PREFS_VERSION, all); // storage errors swallowed inside
}

// Cross-tab convergence for the prefs map. Reads always hit localStorage
// fresh (loadAllPrefs above), so all that's missing is a nudge for components
// holding a hydrated copy (App's sort/board/groupBy state): when another
// tab/window writes the key, tell subscribers to re-read.
const prefsListeners = new Set<() => void>();
onStorageChange(VIEW_PREFS_KEY, () => {
  for (const fn of prefsListeners) fn();
});

/** Subscribe to another document's writes of the view-prefs map. The callback
 *  should re-read via readViewPrefs (re-reading is idempotent, so a synthetic
 *  self-write event is harmless). Returns the unsubscribe. */
export function subscribeViewPrefs(fn: () => void): () => void {
  prefsListeners.add(fn);
  return () => {
    prefsListeners.delete(fn);
  };
}
