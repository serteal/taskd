import type { Task } from "../gen/task/task_pb";
import { dayDiff, tsDate } from "./format";

// A view is a pure filter over the replica (Completed is the exception: it
// pages the server, since the archive isn't replicated). Views live in the
// URL so they survive reloads and can be linked.
//
// "Inbox" follows the label model, not a schema: it is the active tasks
// that belong to no project — i.e. carry no "project:" label.

export type View =
  | { kind: "inbox" }
  | { kind: "today" }
  | { kind: "upcoming" }
  | { kind: "all" }
  | { kind: "completed" }
  | { kind: "label"; label: string }
  | { kind: "source"; source: string }
  | { kind: "ext"; id: string };

export function parseView(search: string): View {
  const p = new URLSearchParams(search);
  const ext = p.get("ext");
  if (ext) return { kind: "ext", id: ext };
  const label = p.get("label");
  if (label) return { kind: "label", label };
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
      return { kind: "today" };
  }
}

export function viewToSearch(v: View): string {
  const p = new URLSearchParams();
  switch (v.kind) {
    case "label":
      p.set("label", v.label);
      break;
    case "source":
      p.set("source", v.source);
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
    case "label":
      return v.label;
    case "source":
      return v.source;
    case "ext":
      return v.id;
  }
}

export function matchesView(t: Task, v: View, now: Date): boolean {
  switch (v.kind) {
    case "all":
      return true;
    case "inbox":
      return !t.labels.some((l) => l.startsWith("project:"));
    case "today": {
      const due = tsDate(t.dueTime);
      return due !== undefined && dayDiff(due, now) <= 0;
    }
    case "upcoming":
      return t.dueTime !== undefined;
    case "label":
      return t.labels.includes(v.label);
    case "source":
      return t.source === v.source;
    case "completed":
      return false; // served by the server, not the replica
    case "ext":
      return false; // extension views render their own content
  }
}

export function sameView(a: View, b: View): boolean {
  return viewToSearch(a) === viewToSearch(b);
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

function loadAllPrefs(): Record<string, Partial<ViewPrefs>> {
  try {
    const raw = localStorage.getItem(VIEW_PREFS_KEY);
    return raw ? (JSON.parse(raw) as Record<string, Partial<ViewPrefs>>) : {};
  } catch {
    return {};
  }
}

export function readViewPrefs(v: View): ViewPrefs {
  const stored = loadAllPrefs()[viewToSearch(v)];
  // Merge over defaults so a partial/old entry never yields undefined fields.
  return { ...DEFAULT_VIEW_PREFS, ...stored };
}

export function writeViewPrefs(v: View, prefs: ViewPrefs): void {
  try {
    const all = loadAllPrefs();
    all[viewToSearch(v)] = prefs;
    localStorage.setItem(VIEW_PREFS_KEY, JSON.stringify(all));
  } catch {
    // storage full/blocked — prefs stay session-local this run
  }
}
