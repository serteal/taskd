import { useSyncExternalStore } from "react";
import { newAdminClient } from "./client";

// The daemon control-plane client (admin.AdminService) plus a tiny module-level
// store so Settings AND the Sidebar share ONE source of truth for extension
// state — toggling in Settings updates the sidebar's "paused" pills live, with
// no reload. Same useSyncExternalStore pattern as the task/notify stores.

/** A plain view of one extension, decoupled from the protobuf message so the
 *  optimistic toggle update is trivial. */
export interface ExtInfo {
  name: string;
  hasSyncer: boolean;
  hasWeb: boolean;
  enabled: boolean;
}

const client = newAdminClient();

/** One-shot list of installed extensions. */
export async function listExtensions(): Promise<ExtInfo[]> {
  const res = await client.listExtensions({});
  return res.extensions.map((e) => ({
    name: e.name,
    hasSyncer: e.hasSyncer,
    hasWeb: e.hasWeb,
    enabled: e.enabled,
  }));
}

/** Enable/disable one extension by name. */
export async function setExtensionEnabled(
  name: string,
  enabled: boolean,
): Promise<{ restartRequired: boolean }> {
  const res = await client.setExtensionEnabled({ name, enabled });
  return { restartRequired: res.restartRequired };
}

// --- shared store -----------------------------------------------------------

interface ExtState {
  /** null until the first load resolves. */
  exts: ExtInfo[] | null;
  loading: boolean;
  error: boolean;
}

let state: ExtState = { exts: null, loading: false, error: false };
const listeners = new Set<() => void>();
let started = false;

function emit(): void {
  for (const fn of listeners) fn();
}

function setState(next: Partial<ExtState>): void {
  state = { ...state, ...next };
  emit();
}

/** (Re)fetch the extension list into the shared store. */
export async function refreshExtensions(): Promise<void> {
  setState({ loading: true, error: false });
  try {
    const exts = await listExtensions();
    setState({ exts, loading: false, error: false });
  } catch {
    setState({ loading: false, error: true });
  }
}

function subscribe(fn: () => void): () => void {
  listeners.add(fn);
  // Fetch lazily on the first subscriber, so a clean daemon with no Settings
  // open still populates the sidebar's source state.
  if (!started) {
    started = true;
    void refreshExtensions();
  }
  return () => {
    listeners.delete(fn);
  };
}

function getSnapshot(): ExtState {
  return state;
}

/** Flip one extension's enabled state, optimistically updating the shared store
 *  (so the sidebar pill and Settings row both move at once) and rolling back on
 *  failure. Returns the info Settings needs to decide whether a reload hint is
 *  warranted. */
export async function toggleExtension(
  name: string,
): Promise<{ restartRequired: boolean; hasWeb: boolean }> {
  const cur = state.exts?.find((e) => e.name === name);
  if (!cur) throw new Error(`unknown extension: ${name}`);
  const next = !cur.enabled;
  const flip = (enabled: boolean) =>
    setState({ exts: (state.exts ?? []).map((e) => (e.name === name ? { ...e, enabled } : e)) });
  flip(next); // optimistic
  try {
    const res = await setExtensionEnabled(name, next);
    return { restartRequired: res.restartRequired, hasWeb: cur.hasWeb };
  } catch (err) {
    flip(cur.enabled); // rollback
    throw err;
  }
}

export interface ExtensionsStore extends ExtState {
  refresh: () => Promise<void>;
}

/** Subscribe to the shared extension state. */
export function useExtensions(): ExtensionsStore {
  const snap = useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
  return { ...snap, refresh: refreshExtensions };
}

// --- source ↔ extension mapping ---------------------------------------------
//
// An extension `name` owns a task source when the source equals the name or is
// prefixed by `name + ":"` (e.g. extension `gcal` feeds source `gcal:personal`).

/** The extension that owns `source`, or undefined if none is installed for it. */
export function extensionForSource(
  exts: ExtInfo[] | null | undefined,
  source: string,
): ExtInfo | undefined {
  if (!exts) return undefined;
  return exts.find((e) => source === e.name || source.startsWith(`${e.name}:`));
}

/** Whether `source`'s owning extension is currently disabled (paused). Unknown
 *  sources (no matching installed extension) are never treated as paused. */
export function isSourcePaused(exts: ExtInfo[] | null | undefined, source: string): boolean {
  const e = extensionForSource(exts, source);
  return e ? !e.enabled : false;
}

// --- daemon build version ---------------------------------------------------
//
// GET /version → { name, version } on the same origin. Fetched once on the
// first subscriber and shared module-level ("dev" while loading or on any
// failure) — but refreshable: a daemon restart may be a new build, so App
// calls refreshDaemonVersion() on every reconnect.

async function fetchVersion(): Promise<string> {
  try {
    const res = await fetch("/version");
    if (!res.ok) return "dev";
    const data = (await res.json()) as { version?: unknown };
    return typeof data.version === "string" && data.version ? data.version : "dev";
  } catch {
    return "dev";
  }
}

const versionListeners = new Set<() => void>();
let versionValue = "dev";
let versionStarted = false;

/** Drop the cached daemon version and refetch it, notifying subscribers —
 *  paired with refreshExtensions after a reconnect (the daemon may have been
 *  restarted on a different build). */
export async function refreshDaemonVersion(): Promise<void> {
  versionStarted = true;
  versionValue = await fetchVersion();
  for (const fn of versionListeners) fn();
}

/** Shared version store — subscribe/get for useSyncExternalStore, exported so
 *  non-React callers (and tests) can read the same value the UI shows. */
export const daemonVersion = {
  subscribe(fn: () => void): () => void {
    versionListeners.add(fn);
    // Once per session on the first subscriber; reconnects refresh explicitly.
    if (!versionStarted) {
      versionStarted = true;
      void refreshDaemonVersion();
    }
    return () => {
      versionListeners.delete(fn);
    };
  },
  get: (): string => versionValue,
};

/** The daemon's build version, "dev" until it resolves (or on failure). */
export function useDaemonVersion(): string {
  return useSyncExternalStore(daemonVersion.subscribe, daemonVersion.get, daemonVersion.get);
}
