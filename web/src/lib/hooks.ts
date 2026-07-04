import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { TaskStore, type Snapshot } from "./store";
import { parseView, viewToSearch, type View } from "./views";

export const StoreContext = createContext<TaskStore | null>(null);

export function useStore(): TaskStore {
  const s = useContext(StoreContext);
  if (!s) throw new Error("StoreContext missing");
  return s;
}

export function useSnapshot(): Snapshot {
  const store = useStore();
  return useSyncExternalStore(store.subscribe, store.getSnapshot);
}

// --- URL-backed view state -------------------------------------------------

const locListeners = new Set<() => void>();
let locWired = false;

function subscribeLoc(fn: () => void): () => void {
  if (!locWired) {
    locWired = true;
    window.addEventListener("popstate", notifyLoc);
  }
  locListeners.add(fn);
  return () => locListeners.delete(fn);
}

function notifyLoc(): void {
  for (const fn of locListeners) fn();
}

export function useView(): [View, (v: View) => void] {
  const search = useSyncExternalStore(subscribeLoc, () => window.location.search);
  const view = useMemo(() => parseView(search), [search]);
  const navigate = (v: View) => {
    history.pushState(null, "", viewToSearch(v));
    notifyLoc();
  };
  return [view, navigate];
}

/** A slow clock so relative dates ("today", "2d ago") stay honest. */
export function useNow(intervalMs = 30_000): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const t = setInterval(() => setNow(new Date()), intervalMs);
    return () => clearInterval(t);
  }, [intervalMs]);
  return now;
}

// --- theme -------------------------------------------------------------

export function useTheme(): [boolean, () => void] {
  const [dark, setDark] = useState(() => {
    const saved = localStorage.getItem("taskd-theme");
    if (saved) return saved === "dark";
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  });
  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
    localStorage.setItem("taskd-theme", dark ? "dark" : "light");
  }, [dark]);
  return [dark, () => setDark((d) => !d)];
}
