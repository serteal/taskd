import { useSyncExternalStore } from "react";
import type { Task } from "../gen/task/task_pb";
import type { PromotableKind } from "./views";
import { dayDiff, tsDate } from "./format";
import { onStorageChange, readVersionedJSON, writeVersionedJSON } from "./storage";

// A SavedFilter is the promotion mechanism for the sources≠tasks model: a
// named predicate over the full replica that pulls a chosen subset — including
// synced items, which the built-in views now hide — onto its own sidebar
// surface. It is a distinct concept from a SavedView (a saved *presentation* of
// a built-in filter); filters define membership, views define layout.
//
// Client-only: persisted to localStorage, exposed via a useSyncExternalStore
// singleton — the same shape as savedviews.ts. No proto/server change is
// needed because `source` already distinguishes local ("") from synced tasks,
// so the predicate runs entirely over the watched replica.

export interface FilterPredicate {
  /** Task carries every one of these labels. */
  labelsAll?: string[];
  /** Task carries at least one of these labels. */
  labelsAny?: string[];
  /** Exact source match. Unset = any source (local and synced). */
  source?: string;
  /** Case-insensitive substring over title + notes. */
  text?: string;
  /** Task has a due date. */
  hasDue?: boolean;
  /** Task is due within N days (overdue counts). */
  dueWithinDays?: number;
}

export interface SavedFilter {
  id: string;
  name: string;
  predicate: FilterPredicate;
  /** Built-in lists this filter also promotes its matches into, as a named
   *  section below the local tasks. Additive and optional — absent on older
   *  saved filters, so the storage envelope stays v1. */
  showIn?: PromotableKind[];
}

/** Pure membership test — the same predicate the sidebar filter view applies
 *  over the replica. Every set constraint is AND-ed; an unset field imposes no
 *  constraint. */
export function matchesFilter(p: FilterPredicate, t: Task, now: Date): boolean {
  if (t.completedTime !== undefined) return false; // predicates cover active tasks only
  if (p.source !== undefined && t.source !== p.source) return false;
  if (p.labelsAll && p.labelsAll.length > 0 && !p.labelsAll.every((l) => t.labels.includes(l)))
    return false;
  if (p.labelsAny && p.labelsAny.length > 0 && !p.labelsAny.some((l) => t.labels.includes(l)))
    return false;
  if (p.text && p.text.trim() !== "") {
    const q = p.text.trim().toLowerCase();
    if (!`${t.title}\n${t.notes}`.toLowerCase().includes(q)) return false;
  }
  if (p.hasDue && t.dueTime === undefined) return false;
  if (p.dueWithinDays !== undefined) {
    const due = tsDate(t.dueTime);
    if (due === undefined) return false;
    if (dayDiff(due, now) > p.dueWithinDays) return false;
  }
  return true;
}

const KEY = "taskd-filters";
const VERSION = 1;

function load(): SavedFilter[] {
  // Existing users' pre-envelope raw array survives: read back as-is and
  // rewritten enveloped on first load (see lib/storage).
  return readVersionedJSON<SavedFilter[]>(KEY, { version: VERSION }) ?? [];
}

class FilterStore {
  private items: SavedFilter[] = load();
  private listeners = new Set<() => void>();

  constructor() {
    // Another tab/window wrote our key: reload from storage and re-emit, so
    // both documents converge — and our NEXT whole-array persist builds on
    // their edit instead of clobbering it with a stale in-memory copy.
    onStorageChange(KEY, () => {
      this.items = load();
      for (const fn of this.listeners) fn();
    });
  }

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };
  getSnapshot = (): SavedFilter[] => this.items;

  /** Synchronous lookup — matchesView resolves a filter view through this. */
  get(id: string): SavedFilter | undefined {
    return this.items.find((f) => f.id === id);
  }

  add(f: Omit<SavedFilter, "id">): SavedFilter {
    const id =
      typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : String(this.items.length + 1);
    const created: SavedFilter = { ...f, id };
    this.items = [...this.items, created];
    this.persist();
    return created;
  }

  update(id: string, patch: Partial<Omit<SavedFilter, "id">>): void {
    this.items = this.items.map((f) => (f.id === id ? { ...f, ...patch } : f));
    this.persist();
  }

  remove(id: string): void {
    this.items = this.items.filter((f) => f.id !== id);
    this.persist();
  }

  private persist(): void {
    writeVersionedJSON(KEY, VERSION, this.items); // storage errors swallowed inside
    for (const fn of this.listeners) fn();
  }
}

export const savedFilters = new FilterStore();

export function useSavedFilters(): SavedFilter[] {
  return useSyncExternalStore(savedFilters.subscribe, savedFilters.getSnapshot);
}
