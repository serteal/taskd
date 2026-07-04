import { useSyncExternalStore } from "react";
import type { View, SortMode } from "./views";

// Named view presets pinned to the sidebar — a saved combination of the
// filter (View), sort, board mode, and search. Client-only (localStorage),
// like the sort/board choices they capture.

export interface SavedView {
  id: string;
  name: string;
  view: View;
  sort: SortMode;
  board: boolean;
  groupBy: "priority" | "project";
  search: string;
}

const KEY = "taskd-saved-views";

function load(): SavedView[] {
  try {
    const raw = localStorage.getItem(KEY);
    return raw ? (JSON.parse(raw) as SavedView[]) : [];
  } catch {
    return [];
  }
}

class SavedViewStore {
  private items: SavedView[] = load();
  private listeners = new Set<() => void>();

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
    try {
      localStorage.setItem(KEY, JSON.stringify(this.items));
    } catch {
      // storage full/blocked — the in-memory list still works this session
    }
    for (const fn of this.listeners) fn();
  }
}

export const savedViews = new SavedViewStore();

export function useSavedViews(): SavedView[] {
  return useSyncExternalStore(savedViews.subscribe, savedViews.getSnapshot);
}
