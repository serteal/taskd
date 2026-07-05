import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore } from "react";
import { TaskStore, type Snapshot } from "./store";
import { parseView, viewToSearch, type View } from "./views";
import {
  applyTheme,
  resolveInitialThemeId,
  DEFAULT_DARK,
  DEFAULT_LIGHT,
  STORAGE_KEY,
  THEMES,
  THEME_BY_ID,
  THEME_GROUPS,
  type Theme,
  type ThemeGroup,
} from "./themes";

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

// A tiny module-level store for the active theme id. main.tsx applies the
// resolved theme to <html> synchronously before React mounts (no FOUC); this
// store just tracks the id and re-applies on change. `lastLightId`/`lastDarkId`
// remember the user's most recent theme in each mode so the binary toggle can
// flip back to it.

function initThemeId(): string {
  if (typeof document !== "undefined" && document.documentElement.dataset.theme) {
    return document.documentElement.dataset.theme;
  }
  return resolveInitialThemeId();
}

let currentThemeId = initThemeId();
let lastLightId = THEME_BY_ID[currentThemeId]?.mode === "dark" ? DEFAULT_LIGHT : currentThemeId;
let lastDarkId = THEME_BY_ID[currentThemeId]?.mode === "dark" ? currentThemeId : DEFAULT_DARK;

const themeListeners = new Set<() => void>();
function subscribeTheme(fn: () => void): () => void {
  themeListeners.add(fn);
  return () => themeListeners.delete(fn);
}
function getThemeId(): string {
  return currentThemeId;
}

/** Select a theme by id: apply its vars + `.dark` + `data-theme`, persist to
 *  localStorage, and notify subscribers. Unknown ids are ignored. */
export function setTheme(id: string): void {
  const theme = THEME_BY_ID[id];
  if (!theme) return;
  currentThemeId = id;
  if (theme.mode === "dark") lastDarkId = id;
  else lastLightId = id;
  applyTheme(id);
  try {
    localStorage.setItem(STORAGE_KEY, id);
  } catch {
    /* storage unavailable */
  }
  for (const fn of themeListeners) fn();
}

/** Flip between the user's last-used light theme and last-used dark theme. */
function toggleMode(): void {
  const cur = THEME_BY_ID[currentThemeId];
  setTheme(cur && cur.mode === "dark" ? lastLightId : lastDarkId);
}

/** Legacy binary control, kept for App/Sidebar/⌘K: reports whether the active
 *  theme is dark and flips between the last light and dark themes. */
export function useTheme(): [boolean, () => void] {
  const id = useSyncExternalStore(subscribeTheme, getThemeId, getThemeId);
  const theme = THEME_BY_ID[id] ?? THEME_BY_ID[DEFAULT_LIGHT];
  return [theme.mode === "dark", toggleMode];
}

export interface ThemeControl {
  /** id of the active theme, e.g. "mocha". */
  themeId: string;
  theme: Theme;
  mode: "light" | "dark";
  /** Select any theme by id. */
  setTheme: (id: string) => void;
  /** Flip light/dark, restoring the last theme used in that mode. */
  toggleMode: () => void;
  /** All themes, in catalog order. */
  themes: Theme[];
  /** Themes grouped ("Catppuccin", "Gruvbox", …) for a picker UI. */
  groups: ThemeGroup[];
}

/** Full theme API for the (later) Settings picker. Re-renders on theme change.
 *  The picker maps `groups` to sections and calls `setTheme(t.id)`, reading the
 *  active row from `themeId`. */
export function useThemeSelector(): ThemeControl {
  const id = useSyncExternalStore(subscribeTheme, getThemeId, getThemeId);
  const theme = THEME_BY_ID[id] ?? THEME_BY_ID[DEFAULT_LIGHT];
  return {
    themeId: theme.id,
    theme,
    mode: theme.mode,
    setTheme,
    toggleMode,
    themes: THEMES,
    groups: THEME_GROUPS,
  };
}
