import type { Task } from "../gen/task/task_pb";
import type { TaskStore } from "./store";

// @-mentions: a task's title (or notes) can reference other things — another
// task, a calendar event, a doc — as an inline markup token the UI renders as
// a chip. The markup is plain text on the wire (the Task proto stays
// string-only): `@[Display title](ref)`, where ref is either
//   task:<id>      — opens that task's detail
//   https://…      — opens the URL in a new tab
// Suggestions come from MENTION PROVIDERS: core registers a "task" provider,
// and extensions can contribute their own (api.registerMentionProvider) — a
// calendar can offer its events, a docs integration its documents. Whatever a
// provider returns resolves to one of the two ref shapes above, so rendering
// never needs the provider again.

export interface MentionItem {
  /** What the chip shows (and what lands in the markup). */
  title: string;
  /** `task:<id>` or an absolute URL. */
  ref: string;
  /** Small muted text in the suggestion row (e.g. a date, a source name). */
  hint?: string;
}

export interface MentionProvider {
  /** Stable id, e.g. "task", "gcal". */
  id: string;
  /** Section label in the suggestion menu, e.g. "Tasks", "Calendar events". */
  title: string;
  /** Items matching the query (empty query = a sensible default set). May be
   *  async; a throwing/rejecting provider contributes nothing. */
  search(query: string): MentionItem[] | Promise<MentionItem[]>;
}

/** One parsed mention occurrence in a text. */
export interface MentionSpan {
  start: number;
  end: number;
  title: string;
  ref: string;
}

// `@[title](ref)` — title may contain anything but `]`, ref anything but
// parens/whitespace (task ids and URLs both qualify).
const MENTION_RE = /@\[([^\]\n]*)\]\(([^()\s]+)\)/g;

export function mentionMarkup(item: Pick<MentionItem, "title" | "ref">): string {
  // Strip the two structural characters so the markup always round-trips.
  return `@[${item.title.replace(/[[\]]/g, "")}](${item.ref})`;
}

export function parseMentions(text: string): MentionSpan[] {
  const out: MentionSpan[] = [];
  for (const m of text.matchAll(MENTION_RE)) {
    out.push({ start: m.index, end: m.index + m[0].length, title: m[1], ref: m[2] });
  }
  return out;
}

/** The text with each mention reduced to "@title" — for plain-text surfaces
 *  (notifications, document.title) that can't render chips. */
export function stripMentions(text: string): string {
  return text.replace(MENTION_RE, (_, title: string) => `@${title}`);
}

/** ref → task id, when the ref is a task ref. */
export function mentionTaskId(ref: string): string | undefined {
  return ref.startsWith("task:") ? ref.slice("task:".length) : undefined;
}

export function isMentionURL(ref: string): boolean {
  return /^https?:\/\//.test(ref);
}

// --- the core "task" provider ------------------------------------------------

const rank = (title: string, q: string): number => {
  const t = title.toLowerCase();
  const i = t.indexOf(q);
  return i === -1 ? Infinity : i;
};

/** Mention other tasks by title, from the live replica. */
export function taskMentionProvider(store: TaskStore): MentionProvider {
  return {
    id: "task",
    title: "Tasks",
    search(query: string): MentionItem[] {
      const q = query.trim().toLowerCase();
      const tasks = [...store.getSnapshot().tasks.values()];
      const matched = q === "" ? tasks : tasks.filter((t) => rank(t.title, q) !== Infinity);
      return matched
        .sort((a, b) => rank(a.title, q) - rank(b.title, q))
        .slice(0, 6)
        .map((t: Task) => ({
          title: t.title,
          ref: `task:${t.id}`,
          hint: t.source !== "" ? t.source : undefined,
        }));
    },
  };
}
