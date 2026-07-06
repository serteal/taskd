import { createContext, useContext, useEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { TaskStore, type Snapshot } from "./store";
import { parseView, viewToSearch, type View } from "./views";
import { registry, useRegistry } from "./extensions";
import { notify, readRemindersWatermark, writeRemindersWatermark } from "./notify";
import { dueTaskIds, missedSince } from "./reminders";
import {
  applyThemeVars,
  readSavedThemeId,
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

/** While enabled, fires a browser notification the moment a local task's due
 *  time arrives — checked on every clock tick and on every replica change (so
 *  a task created or rescheduled into "already due" fires right away, not up
 *  to 30s later). Mount once, near the root (see App.tsx).
 *
 *  A persistent watermark (taskd-reminders-last-seen) lets it catch up on tasks
 *  that became due while the app was closed: on the first evaluation after load
 *  with reminders enabled, if a watermark exists and anything slipped through it
 *  emits ONE summary ("N tasks became due while you were away"). If no watermark
 *  exists yet (the very first enable), it primes silently — no backlog storm on
 *  opt-in — matching the tasks-already-due-at-startup behaviour. The per-task
 *  transition logic then takes over on subsequent ticks. The watermark advances
 *  whenever reminders fire and on each evaluation tick.
 *
 *  The first evaluation waits for `snap.connected`, so priming happens against
 *  the loaded replica (not the empty pre-fetch snapshot) — otherwise the initial
 *  backlog would leak into the per-task path and storm. */
export function useDueReminders(enabled: boolean): void {
  const snap = useSnapshot();
  const now = useNow();
  const primedRef = useRef(false);
  const prevDueRef = useRef<Set<string>>(new Set());

  useEffect(() => {
    const tasks = [...snap.tasks.values()];
    const nowDue = dueTaskIds(tasks, now);

    // First evaluation after load: prime the baseline against the loaded
    // replica and, if enabled, catch up on the closed-app backlog.
    if (!primedRef.current) {
      if (!snap.connected) return; // wait for the initial replica load
      if (enabled) {
        const watermark = readRemindersWatermark();
        if (watermark !== null) {
          const missed = missedSince(tasks, watermark, now);
          if (missed.length > 0) {
            const n = missed.length;
            const msg = `${n} task${n === 1 ? "" : "s"} became due while you were away`;
            void notify.browser("Tasks due", { body: msg });
            notify.toast({ message: msg });
          }
        }
        writeRemindersWatermark(now.getTime());
      }
      prevDueRef.current = nowDue;
      primedRef.current = true;
      return;
    }

    // Subsequent ticks: notify for each id newly entering the due set.
    if (enabled) {
      for (const id of nowDue) {
        if (prevDueRef.current.has(id)) continue;
        const t = snap.tasks.get(id);
        if (t) void notify.browser("Task due", { body: t.title, tag: t.id });
      }
      writeRemindersWatermark(now.getTime());
    }
    prevDueRef.current = nowDue;
  }, [enabled, snap, now]);
}

// --- theme -------------------------------------------------------------

// A tiny module-level store for the active theme id. main.tsx applies the
// resolved theme to <html> synchronously before React mounts (no FOUC); this
// store just tracks the id and re-applies on change. `lastLightId`/`lastDarkId`
// remember the user's most recent theme in each mode so the binary toggle can
// flip back to it.
//
// Themes can come from the built-in catalog (themes.ts) or from an extension
// (registerTheme, like a panel or presenter) — themeById/allThemes/allGroups
// merge both. An extension's bundle loads asynchronously after main.tsx's
// synchronous first-paint applyTheme(), so a saved id naming an
// extension theme isn't resolvable yet at that point; desiredThemeId remembers
// it and the registry.subscribe below re-applies it once the extension
// actually registers it.

function themeById(id: string): Theme | undefined {
  return THEME_BY_ID[id] ?? registry.themes.find((t) => t.id === id);
}

function allThemes(): Theme[] {
  return registry.themes.length ? [...THEMES, ...registry.themes] : THEMES;
}

function allGroups(): ThemeGroup[] {
  if (!registry.themes.length) return THEME_GROUPS;
  const order = THEME_GROUPS.map((g) => g.name);
  const byGroup = new Map<string, Theme[]>(THEME_GROUPS.map((g) => [g.name, [...g.themes]]));
  for (const t of registry.themes) {
    let bucket = byGroup.get(t.group);
    if (!bucket) {
      bucket = [];
      byGroup.set(t.group, bucket);
      order.push(t.group);
    }
    bucket.push(t);
  }
  return order.map((name) => ({ name, themes: byGroup.get(name)! }));
}

function initThemeId(): string {
  if (typeof document !== "undefined" && document.documentElement.dataset.theme) {
    return document.documentElement.dataset.theme;
  }
  return resolveInitialThemeId();
}

let currentThemeId = initThemeId();
let desiredThemeId = readSavedThemeId() ?? currentThemeId;
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
  const theme = themeById(id);
  if (!theme) return;
  currentThemeId = id;
  desiredThemeId = id;
  if (theme.mode === "dark") lastDarkId = id;
  else lastLightId = id;
  applyThemeVars(theme);
  try {
    localStorage.setItem(STORAGE_KEY, id);
  } catch {
    /* storage unavailable */
  }
  for (const fn of themeListeners) fn();
}

// A saved theme naming an extension theme resolves late — once any extension
// registers (bump fires for every registration kind, not just themes; the
// check is cheap enough not to bother filtering to only theme registrations).
registry.subscribe(() => {
  if (desiredThemeId !== currentThemeId && themeById(desiredThemeId)) {
    setTheme(desiredThemeId);
  }
});

/** Flip between the user's last-used light theme and last-used dark theme. */
function toggleMode(): void {
  const cur = themeById(currentThemeId);
  setTheme(cur && cur.mode === "dark" ? lastLightId : lastDarkId);
}

/** Legacy binary control, kept for App/Sidebar/⌘K: reports whether the active
 *  theme is dark and flips between the last light and dark themes. */
export function useTheme(): [boolean, () => void] {
  const id = useSyncExternalStore(subscribeTheme, getThemeId, getThemeId);
  const theme = themeById(id) ?? THEME_BY_ID[DEFAULT_LIGHT];
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

/** Full theme API for the Settings picker. Re-renders on theme change and as
 *  extensions register their own themes. The picker maps `groups` to
 *  sections and calls `setTheme(t.id)`, reading the active row from
 *  `themeId`. */
export function useThemeSelector(): ThemeControl {
  useRegistry(); // re-render as extension themes register
  const id = useSyncExternalStore(subscribeTheme, getThemeId, getThemeId);
  const theme = themeById(id) ?? THEME_BY_ID[DEFAULT_LIGHT];
  return {
    themeId: theme.id,
    theme,
    mode: theme.mode,
    setTheme,
    toggleMode,
    themes: allThemes(),
    groups: allGroups(),
  };
}
