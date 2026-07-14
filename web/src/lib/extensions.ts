import type { ComponentType, ReactNode } from "react";
import { createElement, useSyncExternalStore } from "react";
import type { Task } from "../gen/task/task_pb";
import type { TaskStore, TaskPatch } from "./store";
import { chipParts, tsDate } from "./format";
import { TASK_DRAG_MIME, readTaskId } from "./dnd";
import { extensionNotify } from "./notify";
import { Icon, type IconName } from "../components/icons";
import type { Theme } from "./themes";
import type { MentionProvider } from "./mentions";

// The host side of the extension system: a registry the UI reads
// reactively, the api object handed to each extension's register(), and the
// loader that imports bundles the daemon lists at /ext/index.json.
// The full contract extensions program against lives in
// web/extension-api/index.d.ts.

export interface RowMeta {
  timeText?: string;
  subtitle?: string;
  extraChips?: string[];
  /** A core icon (api.icon(...)), a custom <svg>, or plain text/emoji. */
  icon?: ReactNode;
}

export interface Presenter {
  match(task: Task): boolean;
  rowMeta?(task: Task): RowMeta;
  DetailSection?: ComponentType<{ task: Task; api: unknown }>;
}

export interface ExtensionView {
  id: string;
  title: string;
  Component: ComponentType<{ api: unknown }>;
}

/** A persistent panel mounted beside the main view (e.g. a day timeline). */
export interface Panel {
  id: string;
  title: string;
  side?: "right";
  width?: number;
  defaultOpen?: boolean;
  Component: ComponentType<{ api: unknown }>;
}

/** A command-palette (⌘K) entry. */
export interface Command {
  id: string;
  title: string;
  group?: string;
  /** A core icon (api.icon(...)), a custom <svg>, or plain text/emoji. */
  icon?: ReactNode;
  /** Optional keywords to widen fuzzy matching beyond the title. */
  keywords?: string;
  /** Hidden when this returns false. */
  when?: () => boolean;
  run: () => void;
}

/** A quick-add token handler. Returns what a token contributes, or null. */
export interface QuickAddToken {
  match(token: string): { labels?: string[]; due?: Date } | null;
  /** Shown in help/hints, e.g. "@ctx". */
  hint?: string;
}

class ExtensionRegistry {
  presenters: Presenter[] = [];
  views: ExtensionView[] = [];
  panels: Panel[] = [];
  commands: Command[] = [];
  quickAddTokens: QuickAddToken[] = [];
  themes: Theme[] = [];
  mentionProviders: MentionProvider[] = [];
  private listeners = new Set<() => void>();
  private version = 0;

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  getVersion = (): number => this.version;

  bump(): void {
    this.version++;
    for (const fn of this.listeners) fn();
  }

  presenterFor(task: Task): Presenter | undefined {
    return this.presenters.find((p) => {
      try {
        return p.match(task);
      } catch {
        return false;
      }
    });
  }

  viewById(id: string): ExtensionView | undefined {
    return this.views.find((v) => v.id === id);
  }
}

export const registry = new ExtensionRegistry();

/** Re-render on extension (un)registration. Subscribe once, near the root. */
export function useRegistry(): number {
  return useSyncExternalStore(registry.subscribe, registry.getVersion);
}

// The host's detail panel, reachable from extension code. App installs the
// handler on mount.
export const uiBridge: { openTask: (id: string) => void } = { openTask: () => {} };

// The public extension patch contract (web/extension-api/index.d.ts's
// TaskPatch): only these user-owned fields may cross the bridge into the
// internal store. Everything else the internal TaskPatch also accepts —
// recurrence, parentId — is host-owned structure an extension must not smuggle
// in through the type-only boundary.
const PUBLIC_PATCH_KEYS: readonly string[] = [
  "title",
  "notes",
  "labels",
  "due",
  "completed",
  "userData",
  "expectedRevision",
];

// One-time warnings, keyed so a chatty extension can't flood the console.
const warnedPatchKeys = new Set<string>();
function warnOncePatch(key: string, message: string): void {
  if (warnedPatchKeys.has(key)) return;
  warnedPatchKeys.add(key);
  console.warn(message);
}

/**
 * Enforce the extension patch contract at the host boundary. The type-only
 * `.d.ts` can't stop a bundle from passing extra fields at runtime, so here we:
 *   - drop any key outside the public whitelist (recurrence/parentId included),
 *   - apply the same field-ownership rule the UI does: `completed: true` on a
 *     synced task is stripped (the next sync would revert it anyway).
 * Each dropped/stripped field logs once, naming the extension.
 */
function sanitizeExtPatch(
  extName: string,
  id: string,
  patch: TaskPatch,
  store: TaskStore,
): TaskPatch {
  const clean: TaskPatch = {};
  for (const key of Object.keys(patch)) {
    if (PUBLIC_PATCH_KEYS.includes(key)) {
      (clean as Record<string, unknown>)[key] = (patch as Record<string, unknown>)[key];
    } else {
      warnOncePatch(
        `${extName}:${key}`,
        `taskd: extension “${extName}” tried to set unsupported patch field “${key}”; dropped.`,
      );
    }
  }
  if (clean.completed === true) {
    const t = store.getSnapshot().tasks.get(id);
    if (t && t.source !== "") {
      warnOncePatch(
        `${extName}:completed-synced`,
        `taskd: extension “${extName}” tried to complete synced task ${id}; completion is source-owned and was dropped.`,
      );
      delete clean.completed;
    }
  }
  return clean;
}

export function buildAPI(store: TaskStore, extName = "extension") {
  const useTasks = (): Task[] => {
    const snap = useSyncExternalStore(store.subscribe, store.getSnapshot);
    return [...snap.tasks.values()];
  };
  const useNow = (): Date => {
    // Local import cycle avoidance: minimal inline clock (30s tick).
    const subscribe = (fn: () => void) => {
      const t = setInterval(fn, 30_000);
      return () => clearInterval(t);
    };
    return new Date(useSyncExternalStore(subscribe, () => Math.floor(Date.now() / 30_000)) * 30_000);
  };

  return {
    registerPresenter: (p: Presenter) => {
      registry.presenters.push(p);
      registry.bump();
    },
    registerView: (v: ExtensionView) => {
      registry.views.push(v);
      registry.bump();
    },
    registerPanel: (p: Panel) => {
      registry.panels.push(p);
      registry.bump();
    },
    registerCommand: (c: Command) => {
      registry.commands.push(c);
      registry.bump();
    },
    registerQuickAddToken: (t: QuickAddToken) => {
      registry.quickAddTokens.push(t);
      registry.bump();
    },
    /** Contribute @-mention suggestions (typing "@" in a title field asks
     *  every provider) — e.g. calendar events, documents. */
    registerMentionProvider: (p: MentionProvider) => {
      registry.mentionProviders.push(p);
      registry.bump();
    },
    /** Contribute a theme to Settings' picker — same idea as a panel or
     *  presenter, just for the theme catalog rather than a task view. */
    registerTheme: (t: Theme) => {
      registry.themes.push(t);
      registry.bump();
    },
    hooks: { useTasks, useNow },
    /** Non-hook snapshot of active tasks, for imperative code (e.g. commands). */
    getTasks: (): Task[] => [...store.getSnapshot().tasks.values()],
    store: {
      create: (f: { title: string; notes?: string; labels?: string[]; due?: Date }) => store.create(f),
      // The internal UpdateResult is NOT part of the extension contract
      // (index.d.ts resolves this to void); the host also enforces
      // field-ownership on the patch here rather than trusting the caller.
      update: (id: string, patch: TaskPatch): Promise<void> =>
        store.update(id, sanitizeExtPatch(extName, id, patch, store)).then(() => {}),
      delete: (id: string) => store.delete(id),
    },
    client: store.client,
    ui: { openTask: (id: string) => uiBridge.openTask(id) },
    // The drag payload contract: an extension panel reads the dragged task's
    // id off a drop event with dnd.readTaskId(e.dataTransfer).
    dnd: { mime: TASK_DRAG_MIME, readTaskId },
    // Transient toasts + browser notifications.
    notify: extensionNotify,
    // A core SVG icon by name, for use in RowMeta/Command icon fields (or
    // inline your own <svg> instead).
    icon: (name: IconName, opts?: { size?: number; className?: string }): ReactNode =>
      createElement(Icon, { name, ...opts }),
    format: { tsDate, chipParts },
  };
}

/**
 * Imports every extension bundle the daemon serves, plus an optional
 * ?ext-dev=<url> module for extension development against a running app.
 * One broken extension logs and is skipped; it cannot take the app down.
 */
export async function loadExtensions(store: TaskStore): Promise<void> {
  const urls: string[] = [];
  try {
    const res = await fetch("/ext/index.json");
    if (res.ok) {
      for (const e of (await res.json()) as { name: string }[]) {
        urls.push(`/ext/${e.name}/main.js`);
      }
    }
  } catch {
    // no daemon-side extensions (e.g. Vite dev without proxy target)
  }
  const dev = new URLSearchParams(window.location.search).get("ext-dev");
  if (dev) urls.push(dev);

  for (const url of urls) {
    try {
      const mod = await import(/* @vite-ignore */ url);
      const ext = mod.default;
      if (!ext || typeof ext.register !== "function") {
        throw new Error("default export is not a TaskdExtension");
      }
      // A per-extension API so the boundary's field-ownership warnings can
      // name the offending extension.
      ext.register(buildAPI(store, ext.name ?? url));
      console.info(`taskd: loaded extension ${ext.name ?? url}`);
    } catch (err) {
      console.error(`taskd: extension ${url} failed to load:`, err);
      extensionNotify.error(`An extension failed to load (${url.split("/").slice(-2, -1)[0] || url}).`);
    }
  }
}
