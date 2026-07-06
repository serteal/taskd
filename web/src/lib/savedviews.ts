import { useSyncExternalStore } from "react";
import type { View, SortMode } from "./views";
import { onStorageChange, readVersionedJSON, writeVersionedJSON } from "./storage";

// Named view presets pinned to the sidebar — a saved combination of the
// filter (View), sort, board mode, and group-by. Client-only (localStorage),
// like the sort/board choices they capture.

export interface SavedView {
  id: string;
  name: string;
  view: View;
  sort: SortMode;
  board: boolean;
  groupBy: "priority" | "project";
}

const KEY = "taskd-saved-views";
const VERSION = 1;

function load(): SavedView[] {
  // A pre-envelope raw array (existing users) is read back unchanged and
  // rewritten enveloped on first load. Older entries may still carry a
  // now-removed `search` field; it's kept as an ignored extra property.
  return readVersionedJSON<SavedView[]>(KEY, { version: VERSION }) ?? [];
}

class SavedViewStore {
  private items: SavedView[] = load();
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
  getSnapshot = (): SavedView[] => this.items;

  add(v: Omit<SavedView, "id">): void {
    const id =
      typeof crypto !== "undefined" && crypto.randomUUID ? crypto.randomUUID() : String(this.items.length + 1);
    this.items = [...this.items, { ...v, id }];
    this.persist();
  }

  remove(id: string): void {
    this.items = this.items.filter((v) => v.id !== id);
    this.persist();
  }

  private persist(): void {
    writeVersionedJSON(KEY, VERSION, this.items); // storage errors swallowed inside
    for (const fn of this.listeners) fn();
  }
}

export const savedViews = new SavedViewStore();

export function useSavedViews(): SavedView[] {
  return useSyncExternalStore(savedViews.subscribe, savedViews.getSnapshot);
}
