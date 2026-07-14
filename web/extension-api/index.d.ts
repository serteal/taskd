// @taskd/extension-api — the contract between the taskd web app and its
// extensions. An extension is an ESM bundle at <extension>/web/main.js whose
// default export is a TaskdExtension; the host imports it at startup and
// calls register(api).
//
// Build recipe (esbuild): bundle with react and react/jsx-runtime aliased
// to the shims in this package, so extension components share the host's
// React instance (a second React copy would break hooks):
//
//   esbuild src/main.tsx --bundle --format=esm --outfile=web/main.js \
//     --alias:react=@taskd/extension-api/react-shim.js \
//     --alias:react/jsx-runtime=@taskd/extension-api/jsx-shim.js
//
// Styling: the host's Tailwind classes are NOT part of the contract (the
// core bundle only contains classes core uses). Style with inline styles
// and the host's CSS custom properties, which are theme-aware:
//   --bg --surface --ink --muted --faint --line --accent --warn
// and fonts: "IBM Plex Sans" (UI) / "IBM Plex Mono" (data).

import type { ComponentType, ReactNode } from "react";

// The Task message, exactly as protoc-gen-es generates it from
// proto/task/task.proto — re-exported so extensions get the real type (with
// the proto Timestamp/Struct field types) and it can never drift from the
// wire contract. Source-owned data is `externalData`; user/client-owned is
// `userData` (namespace your keys). This is a type-only re-export; nothing is
// imported at runtime.
export type { Task } from "../src/gen/task/task_pb";
import type { Task } from "../src/gen/task/task_pb";

/** Extra presentation a matching task's list row gets. */
export interface RowMeta {
  /** Short mono text shown where the due date normally is (e.g. "9:30–9:45"). */
  timeText?: string;
  /** Small muted text after the title (e.g. "taskd#41"). */
  subtitle?: string;
  /** Additional label-style chips (plain strings). */
  extraChips?: string[];
  /** Icon before the title: a core icon `api.icon("...")`, your own <svg>,
   *  or plain text/emoji. */
  icon?: ReactNode;
}

export interface Presenter {
  /** Which tasks this presenter handles, e.g. t.source.startsWith("gcal"). */
  match(task: Task): boolean;
  /** Row customization; return {} for none. */
  rowMeta?(task: Task): RowMeta;
  /** Rendered inside the detail panel for matching tasks. */
  DetailSection?: ComponentType<{ task: Task; api: ExtensionAPI }>;
}

/** A whole view contributed to the app: sidebar entry + URL (?ext=<id>). */
export interface ExtensionView {
  id: string;
  title: string;
  Component: ComponentType<{ api: ExtensionAPI }>;
}

/**
 * A persistent panel mounted beside the main view — visible on top of
 * whatever view the user is in (unlike a full-screen ExtensionView). Use it
 * for always-available surfaces like a day timeline. A panel is an ordinary
 * drop target: read a dropped task's id with api.dnd.readTaskId.
 */
export interface Panel {
  id: string;
  title: string;
  /** Which edge to dock on. Only "right" is supported today. */
  side?: "right";
  /** Panel width in px (default 300). */
  width?: number;
  /** Whether it starts open (default true). */
  defaultOpen?: boolean;
  Component: ComponentType<{ api: ExtensionAPI }>;
}

/** A command-palette (⌘K) entry contributed by an extension. */
export interface Command {
  id: string;
  title: string;
  /** Section heading in the palette. */
  group?: string;
  /** A core icon `api.icon("...")`, your own <svg>, or text/emoji. */
  icon?: ReactNode;
  /** Extra words to match on beyond the title. */
  keywords?: string;
  /** Hidden when this returns false. */
  when?: () => boolean;
  run(): void;
}

/**
 * A quick-add token handler. The new-task field splits input into words; for
 * words that aren't a built-in token (#label, p1-3, a date), each handler is
 * asked to interpret it. Return what the token contributes, or null to pass.
 */
export interface QuickAddToken {
  match(token: string): { labels?: string[]; due?: Date } | null;
  /** Shown in hints, e.g. "@ctx". */
  hint?: string;
}

/** One @-mention suggestion. `ref` is what the markup stores and must be
 *  either `task:<id>` (opens that task's detail) or an absolute URL (opens in
 *  a new tab) — chips render from the ref alone, no provider round-trip. */
export interface MentionItem {
  /** What the chip shows (and what lands in the `@[title](ref)` markup). */
  title: string;
  /** `task:<id>` or an absolute URL. */
  ref: string;
  /** Small muted text in the suggestion row (e.g. a date, a source name). */
  hint?: string;
}

/**
 * An @-mention source. Typing "@" in a title field queries every registered
 * provider and lists the results under `title` section headers — e.g. a
 * calendar offering its events, a docs integration its documents.
 */
export interface MentionProvider {
  /** Stable id, e.g. "gcal". */
  id: string;
  /** Section label in the suggestion menu, e.g. "Calendar events". */
  title: string;
  /** Items matching the query (empty query = a sensible default set). May be
   *  async; a throwing/rejecting provider contributes nothing. */
  search(query: string): MentionItem[] | Promise<MentionItem[]>;
}

/** The 8 CSS custom properties a theme sets (see index.css `@theme inline`). */
export interface ThemeVars {
  bg: string;
  surface: string;
  ink: string;
  muted: string;
  faint: string;
  line: string;
  accent: string;
  warn: string;
}

/**
 * A theme contributed by an extension — same idea as a panel or presenter,
 * just for the Settings theme picker rather than a task view. Appears
 * alongside the built-in catalog, grouped under `group` (e.g. your
 * extension's name).
 */
export interface Theme {
  id: string;
  label: string;
  group: string;
  mode: "light" | "dark";
  vars: ThemeVars;
}

/** Transient toast; `action` renders a button (e.g. Undo). */
export interface ToastOptions {
  kind?: "info" | "success" | "error";
  message: string;
  action?: { label: string; onClick(): void };
  /** ms; <= 0 keeps it until dismissed. */
  duration?: number;
}

export interface TaskPatch {
  title?: string;
  notes?: string;
  labels?: string[];
  /** undefined = untouched; null = clear. */
  due?: Date | null;
  completed?: boolean;
  /** Whole-field replace (merge by spreading task.userData). null clears. */
  userData?: Record<string, unknown> | null;
  expectedRevision?: bigint;
}

export interface ExtensionAPI {
  registerPresenter(p: Presenter): void;
  registerView(v: ExtensionView): void;
  registerPanel(p: Panel): void;
  /** Add an entry to the ⌘K command palette. */
  registerCommand(c: Command): void;
  /** Interpret a quick-add token (e.g. "@home" → a label). */
  registerQuickAddToken(t: QuickAddToken): void;
  /** Contribute @-mention suggestions (typing "@" in a title field). */
  registerMentionProvider(p: MentionProvider): void;
  /** Contribute a theme to Settings' picker, grouped under `t.group`. */
  registerTheme(t: Theme): void;

  /** Live replica of ACTIVE tasks — hooks usable in extension components. */
  hooks: {
    /** All active tasks; re-renders on any change (watch-fed). */
    useTasks(): Task[];
    /** A slow clock (default 30s tick) for relative-time rendering. */
    useNow(): Date;
  };

  /** A one-shot snapshot of active tasks for imperative code (e.g. a
   *  command's run()), where a hook can't be used. */
  getTasks(): Task[];

  /** Mutations, optimistic against the replica. */
  store: {
    create(fields: { title: string; notes?: string; labels?: string[]; due?: Date }): Promise<Task>;
    /**
     * Apply a patch to a task. Resolves once the write is accepted; the
     * update's result payload is NOT part of this contract (do not rely on a
     * value). The host enforces field ownership on the patch: keys outside
     * TaskPatch are dropped, and `completed: true` on a synced task is ignored
     * (its completion follows the source).
     */
    update(id: string, patch: TaskPatch): Promise<void>;
    delete(id: string): Promise<void>;
  };

  /** The full generated TaskService client, for anything else. */
  client: unknown;

  ui: {
    /** Opens the host's detail panel for a task. */
    openTask(id: string): void;
  };

  /**
   * Drag-and-drop bridge. Core task rows are drag sources; a panel that
   * wants to accept them handles onDragOver (preventDefault) + onDrop and
   * reads the task id here. `mime` is the dataTransfer type used.
   */
  dnd: {
    mime: string;
    readTaskId(dataTransfer: DataTransfer): string | null;
  };

  /** Transient toasts and native browser notifications. */
  notify: {
    toast(opts: ToastOptions): number;
    error(message: string): number;
    /** Native browser notification; requests permission on first use. */
    browser(title: string, options?: NotificationOptions): Promise<void>;
  };

  /**
   * A crisp SVG icon from the core set, for RowMeta/Command icon fields — or
   * inline your own <svg> instead. Icons use `currentColor`, so they take the
   * surrounding text color. Names: plus, star, moon, panel, sort,
   * chevron-right, hash, tag, swap, open, check, calendar, trash, diamond,
   * circle, flag, inbox, search, list, board, x.
   */
  icon(name: string, opts?: { size?: number; className?: string }): ReactNode;

  format: {
    /** proto Timestamp → Date (undefined-safe). */
    tsDate(ts?: { seconds: bigint; nanos: number }): Date | undefined;
    /** "project:home" → { ns: "project", val: "home" }. */
    chipParts(label: string): { ns?: string; val: string };
  };
}

export interface TaskdExtension {
  name: string;
  register(api: ExtensionAPI): void;
}
