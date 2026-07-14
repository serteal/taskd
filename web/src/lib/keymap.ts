import { useSyncExternalStore } from "react";
import { onStorageChange, readVersionedJSON, writeVersionedJSON } from "./storage";

// The keymap: every global shortcut is a named ACTION with one or more
// BINDINGS, user-rebindable in Settings → Keybindings. App.tsx's keydown
// handler matches events against this map instead of a hardcoded switch, and
// ShortcutsHelp/the footer render from it — so a rebind shows up everywhere.
//
// A binding is a string of 1–2 STROKES separated by a space ("q", "ctrl+j",
// "g i"). A stroke is modifiers + key: "ctrl+", "alt+", "meta+" prefixes in
// that order; "mod+" means the platform primary (⌘ on macOS, Ctrl elsewhere)
// and is expanded at match time. Keys are lowercase; named keys are "enter",
// "space", "arrowup"…; a shifted printable char is stored as the char itself
// ("?"), while "shift+" is written out only alongside another modifier or a
// named key ("mod+shift+z"). Escape and Tab are reserved (close/menus) and
// never bindable.
//
// Only OVERRIDES are persisted (localStorage, versioned envelope) — defaults
// can evolve without stale copies pinning users to old maps.

export const IS_MAC =
  typeof navigator !== "undefined" && /Mac|iP(hone|ad|od)/.test(navigator.platform);

export interface ActionDef {
  id: string;
  title: string;
  /** Section heading in Settings/help. */
  group: string;
  defaults: string[];
  /** Match even while focus is in an input/textarea (the palette). */
  whileTyping?: boolean;
}

// The catalog. Order here is the display order in Settings and the help.
export const ACTIONS: ActionDef[] = [
  { id: "palette", title: "Command palette & search", group: "App", defaults: ["mod+k"], whileTyping: true },
  { id: "add-task", title: "New task", group: "App", defaults: ["q", "c", "/"] },
  { id: "undo", title: "Undo", group: "App", defaults: ["mod+z"] },
  { id: "toggle-panel", title: "Toggle the side panel", group: "App", defaults: ["t"] },
  { id: "help", title: "Keyboard shortcuts help", group: "App", defaults: ["?"] },

  { id: "go-inbox", title: "Go to Inbox", group: "Navigation", defaults: ["g i"] },
  { id: "go-today", title: "Go to Today", group: "Navigation", defaults: ["g t"] },
  { id: "go-upcoming", title: "Go to Upcoming", group: "Navigation", defaults: ["g u"] },
  { id: "go-all", title: "Go to All tasks", group: "Navigation", defaults: ["g a"] },
  { id: "go-completed", title: "Go to Completed", group: "Navigation", defaults: ["g c"] },

  {
    id: "move-down",
    title: "Move selection down",
    group: "Tasks",
    defaults: ["j", "ctrl+j", "ctrl+n"],
  },
  {
    id: "move-up",
    title: "Move selection up",
    group: "Tasks",
    // On non-mac, ctrl+k belongs to the palette (mod+k) — leave it out there.
    defaults: IS_MAC ? ["k", "ctrl+k", "ctrl+p"] : ["k", "ctrl+p"],
  },
  { id: "complete", title: "Complete (or the selection)", group: "Tasks", defaults: ["x"] },
  { id: "edit-title", title: "Edit title inline", group: "Tasks", defaults: ["e"] },
  { id: "open-details", title: "Open details", group: "Tasks", defaults: ["enter"] },
  { id: "toggle-select", title: "Toggle selection", group: "Tasks", defaults: ["space"] },
];

export const ACTION_BY_ID = new Map(ACTIONS.map((a) => [a.id, a]));

/** Keys that can never be (part of) a binding — they have fixed meanings. */
const RESERVED = new Set(["escape", "tab"]);

// --- stroke normalization ----------------------------------------------------

/** The normalized stroke a keyboard event produces, or null when it's a bare
 *  modifier press or a reserved key. */
export function strokeOf(e: Pick<KeyboardEvent, "key" | "ctrlKey" | "altKey" | "metaKey" | "shiftKey">): string | null {
  let key = e.key;
  if (key === " ") key = "space";
  if (key.length > 1) key = key.toLowerCase();
  if (["control", "alt", "meta", "shift"].includes(key)) return null;
  if (RESERVED.has(key)) return null;

  const mods: string[] = [];
  if (e.ctrlKey) mods.push("ctrl");
  if (e.altKey) mods.push("alt");
  if (e.metaKey) mods.push("meta");
  // A bare shifted printable already IS its char ("?"); spell shift out only
  // when it modifies a named key or rides along other modifiers ("mod+shift+z").
  if (e.shiftKey && (mods.length > 0 || key.length > 1)) mods.push("shift");

  if (key.length === 1) key = key.toLowerCase();
  return [...mods, key].join(mods.length ? "+" : "");
}

/** Expand "mod+" to the platform primary so bindings compare against strokes. */
export function resolveBinding(binding: string, isMac = IS_MAC): string {
  return binding.replaceAll("mod+", isMac ? "meta+" : "ctrl+");
}

const MOD_ORDER = ["ctrl", "alt", "meta", "shift"];

/** Canonicalize a captured stroke sequence into a stored binding: the platform
 *  primary modifier is stored portably as "mod+". */
export function toStoredBinding(strokes: string[], isMac = IS_MAC): string {
  const primary = isMac ? "meta" : "ctrl";
  return strokes
    .map((s) => {
      const parts = s.split("+");
      const key = parts.pop()!;
      const mods = parts
        .map((m) => (m === primary ? "mod" : m))
        .sort((a, b) => (a === "mod" ? -1 : b === "mod" ? 1 : MOD_ORDER.indexOf(a) - MOD_ORDER.indexOf(b)));
      return [...mods, key].join(mods.length ? "+" : "");
    })
    .join(" ");
}

// --- pretty-printing ----------------------------------------------------------

const KEY_GLYPHS: Record<string, string> = {
  enter: "⏎",
  space: "Space",
  arrowup: "↑",
  arrowdown: "↓",
  arrowleft: "←",
  arrowright: "→",
  backspace: "⌫",
};

function prettyStroke(stroke: string, isMac: boolean): string {
  const parts = resolveBinding(stroke, isMac).split("+");
  const key = parts.pop()!;
  const mods = parts.map((m) =>
    m === "ctrl" ? (isMac ? "⌃" : "Ctrl") : m === "meta" ? "⌘" : m === "alt" ? (isMac ? "⌥" : "Alt") : "⇧",
  );
  const k = KEY_GLYPHS[key] ?? (key.length === 1 ? key : key[0].toUpperCase() + key.slice(1));
  return isMac ? [...mods, k].join("") : [...mods, k].join("+");
}

/** Human-readable form: "⌘K", "Ctrl+J", "g then i". */
export function prettyBinding(binding: string, isMac = IS_MAC): string {
  const strokes = binding.split(" ");
  return strokes.map((s) => prettyStroke(s, isMac)).join(" then ");
}

// --- the store -----------------------------------------------------------------

const KEYMAP_KEY = "taskd-keymap";
const KEYMAP_VERSION = 1;

type Overrides = Record<string, string[]>;

let overrides: Overrides = loadOverrides();
let snapshot: Record<string, string[]> = buildSnapshot();
const listeners = new Set<() => void>();

function loadOverrides(): Overrides {
  return readVersionedJSON<Overrides>(KEYMAP_KEY, { version: KEYMAP_VERSION }) ?? {};
}

function buildSnapshot(): Record<string, string[]> {
  const out: Record<string, string[]> = {};
  for (const a of ACTIONS) out[a.id] = overrides[a.id] ?? a.defaults;
  return out;
}

function persist(): void {
  writeVersionedJSON(KEYMAP_KEY, KEYMAP_VERSION, overrides);
  snapshot = buildSnapshot();
  for (const fn of listeners) fn();
}

onStorageChange(KEYMAP_KEY, () => {
  overrides = loadOverrides();
  snapshot = buildSnapshot();
  for (const fn of listeners) fn();
});

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

/** Live map of actionId → bindings. Re-renders on any rebind. */
export function useKeymap(): Record<string, string[]> {
  return useSyncExternalStore(subscribe, () => snapshot, () => snapshot);
}

export function getKeymap(): Record<string, string[]> {
  return snapshot;
}

export function isOverridden(actionId: string): boolean {
  return actionId in overrides;
}

/**
 * Give `actionId` an extra binding. If another action holds the same binding
 * (compared platform-resolved), it loses it there — returns that action's id
 * so the UI can say so. No-op (returns undefined) if the action already has it.
 */
export function addBinding(actionId: string, binding: string): string | undefined {
  const resolved = resolveBinding(binding);
  let movedFrom: string | undefined;
  for (const a of ACTIONS) {
    const current = snapshot[a.id];
    const has = current.some((b) => resolveBinding(b) === resolved);
    if (!has) continue;
    if (a.id === actionId) return undefined; // already bound here
    overrides[a.id] = current.filter((b) => resolveBinding(b) !== resolved);
    movedFrom = a.id;
  }
  overrides[actionId] = [...snapshot[actionId], binding];
  persist();
  return movedFrom;
}

export function removeBinding(actionId: string, binding: string): void {
  overrides[actionId] = snapshot[actionId].filter((b) => b !== binding);
  persist();
}

export function resetAction(actionId: string): void {
  // Restoring defaults may re-claim a binding another action took — mirror
  // addBinding's steal-on-conflict so the invariant (a binding lives on at
  // most one action) holds.
  const def = ACTION_BY_ID.get(actionId);
  if (!def) return;
  delete overrides[actionId];
  const claimed = new Set(def.defaults.map((b) => resolveBinding(b)));
  for (const a of ACTIONS) {
    if (a.id === actionId) continue;
    const current = overrides[a.id] ?? a.defaults;
    const kept = current.filter((b) => !claimed.has(resolveBinding(b)));
    if (kept.length !== current.length) overrides[a.id] = kept;
  }
  persist();
}

export function resetAllBindings(): void {
  overrides = {};
  persist();
}

// --- matching -----------------------------------------------------------------

export interface MatchResult {
  /** The matched action id, if the (pending +) stroke completed a binding. */
  action?: string;
  /** True when the stroke starts a multi-stroke binding — hold it as pending. */
  prefix?: boolean;
}

/**
 * Match a stroke against the keymap, chord-aware. `pending` is the previous
 * stroke when a chord is in flight ("" = none). `allow` filters which actions
 * are eligible (e.g. exclude !whileTyping actions inside inputs).
 */
export function matchStroke(
  map: Record<string, string[]>,
  pending: string,
  stroke: string,
  allow: (a: ActionDef) => boolean = () => true,
): MatchResult {
  const seq = pending !== "" ? `${pending} ${stroke}` : stroke;
  for (const a of ACTIONS) {
    if (!allow(a)) continue;
    for (const b of map[a.id] ?? []) {
      if (resolveBinding(b) === seq) return { action: a.id };
    }
  }
  // Only a FIRST stroke can open a chord (chords are two strokes long).
  if (pending === "") {
    for (const a of ACTIONS) {
      if (!allow(a)) continue;
      for (const b of map[a.id] ?? []) {
        const r = resolveBinding(b);
        if (r.includes(" ") && r.split(" ")[0] === stroke) return { prefix: true };
      }
    }
  }
  return {};
}
