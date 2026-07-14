import type { ReactNode, RefObject } from "react";
import { useEffect, useMemo, useState } from "react";
import { registry, useRegistry } from "../lib/extensions";
import { mentionMarkup, type MentionItem } from "../lib/mentions";
import { chipParts } from "../lib/format";

// Inline autocomplete for title fields: typing "#" suggests labels, "@"
// suggests mentions from every registered provider (core tasks, extension
// sources like calendar events). Accept with Tab/Enter, move with the arrows
// or ctrl+j/k / ctrl+n/p, dismiss with Escape. A "#" query that matches no
// existing label offers a create row — the label springs into existence when
// the task is saved (labels are just strings on tasks).
//
// Usage: the caller owns the textarea; this hook owns the dropdown. Render
// `menu` inside the textarea's `relative` wrapper, and give the typeahead
// first refusal on keys: `if (typeahead.onKeyDown(e)) return;`.

interface ActiveToken {
  trigger: "#" | "@";
  query: string;
  start: number; // char offset of the trigger character
  end: number; // caret (completion replaces [start, end))
}

/** The #/@ token the caret is currently inside (touching its end), if any. */
export function activeTokenAt(value: string, caret: number): ActiveToken | null {
  const left = value.slice(0, caret);
  const m = /(^|\s)([#@]\S*)$/.exec(left);
  if (!m) return null;
  const tok = m[2];
  // Never re-trigger inside completed mention markup (it contains []()).
  if (/[[\]()]/.test(tok)) return null;
  return {
    trigger: tok[0] as "#" | "@",
    query: tok.slice(1),
    start: caret - tok.length,
    end: caret,
  };
}

interface Row {
  key: string;
  /** Section header this row falls under ("" = none). */
  group: string;
  node: ReactNode;
  hint?: string;
  pick: () => void;
}

export function useTitleTypeahead({
  ref,
  value,
  onChange,
  labels,
  triggers = ["#", "@"],
}: {
  ref: RefObject<HTMLTextAreaElement | null>;
  value: string;
  onChange: (v: string) => void;
  /** Existing labels, for "#" suggestions. */
  labels: string[];
  /** Which trigger characters are live (the edit surface only wants "@" —
   *  its labels are a structured field, not title tokens). */
  triggers?: ("#" | "@")[];
}): {
  menu: ReactNode;
  onKeyDown: (e: React.KeyboardEvent) => boolean;
  /** Wire this as the textarea's onChange — it forwards to the caller's
   *  onChange and tracks the caret in the same React event. */
  onChange: (e: React.ChangeEvent<HTMLTextAreaElement>) => void;
  open: boolean;
} {
  const [caret, setCaret] = useState(0);
  const [sel, setSel] = useState(0);
  const [focused, setFocused] = useState(false);
  // Escape parks the typeahead for the token being typed; moving to another
  // token (different start) re-arms it.
  const [dismissedAt, setDismissedAt] = useState<number | null>(null);
  const [mentionItems, setMentionItems] = useState<{ group: string; item: MentionItem }[]>([]);
  const regVersion = useRegistry();

  // Value edits report their caret through handleChange below (same React
  // event as the caller's state update — a native `input` listener would race
  // React's controlled-value bookkeeping). These listeners cover the caret
  // moving WITHOUT the value changing: arrows, clicks, focus.
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    const sync = () => setCaret(el.selectionStart ?? 0);
    const onFocus = () => {
      setFocused(true);
      sync();
    };
    // The menu must not outlive the field's focus — left open it would sit on
    // top of (and swallow clicks meant for) whatever renders below the title.
    const onBlur = () => setFocused(false);
    sync();
    setFocused(document.activeElement === el);
    el.addEventListener("keyup", sync);
    el.addEventListener("click", sync);
    el.addEventListener("focus", onFocus);
    el.addEventListener("blur", onBlur);
    return () => {
      el.removeEventListener("keyup", sync);
      el.removeEventListener("click", sync);
      el.removeEventListener("focus", onFocus);
      el.removeEventListener("blur", onBlur);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ref.current]);

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setCaret(e.target.selectionStart ?? e.target.value.length);
    onChange(e.target.value);
  };

  const token = useMemo(() => {
    const t = activeTokenAt(value, Math.min(caret, value.length));
    return t !== null && triggers.includes(t.trigger) ? t : null;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value, caret, triggers.join("")]);
  const active = token !== null && dismissedAt !== token.start;

  // Re-arm after Escape once the user moves on to a different token.
  useEffect(() => {
    if (dismissedAt !== null && (token === null || token.start !== dismissedAt)) {
      setDismissedAt(null);
    }
  }, [token, dismissedAt]);

  // "@" suggestions: ask every provider; async ones land when they land.
  useEffect(() => {
    if (!active || token.trigger !== "@") {
      setMentionItems([]);
      return;
    }
    let alive = true;
    const q = token.query;
    void Promise.all(
      registry.mentionProviders.map(async (p) => {
        try {
          const items = await p.search(q);
          return items.slice(0, 5).map((item) => ({ group: p.title, item }));
        } catch {
          return [];
        }
      }),
    ).then((groups) => {
      if (alive) setMentionItems(groups.flat());
    });
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, token?.trigger, token?.query, regVersion]);

  const insert = (replacement: string) => {
    if (!token) return;
    const next = value.slice(0, token.start) + replacement + value.slice(token.end);
    onChange(next);
    const pos = token.start + replacement.length;
    requestAnimationFrame(() => {
      const el = ref.current;
      if (el) {
        el.focus();
        el.setSelectionRange(pos, pos);
      }
      setCaret(pos);
    });
  };

  const rows = useMemo<Row[]>(() => {
    if (!active) return [];
    if (token.trigger === "#") {
      const q = token.query.toLowerCase();
      const matches = labels.filter((l) => l.toLowerCase().includes(q)).slice(0, 8);
      const out: Row[] = matches.map((l) => {
        const { ns, val } = chipParts(l);
        return {
          key: `label-${l}`,
          group: "Labels",
          node: (
            <span className="font-mono text-[12px]">
              {ns && <span className="text-faint">{ns}:</span>}
              {val}
            </span>
          ),
          pick: () => insert(`#${l} `),
        };
      });
      const exact = token.query.trim();
      if (exact !== "" && !labels.includes(exact)) {
        out.push({
          key: "label-new",
          group: "Labels",
          node: <span className="text-[12.5px]">Create “{exact}”</span>,
          hint: "new",
          pick: () => insert(`#${exact} `),
        });
      }
      return out;
    }
    return mentionItems.map(({ group, item }) => ({
      key: `m-${item.ref}`,
      group,
      node: <span className="truncate text-[12.5px]">{item.title}</span>,
      hint: item.hint,
      pick: () => insert(`${mentionMarkup(item)} `),
    }));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [active, token?.trigger, token?.query, labels, mentionItems, value]);

  useEffect(() => setSel(0), [token?.start, token?.trigger, token?.query, rows.length]);

  const open = active && focused && rows.length > 0;

  const onKeyDown = (e: React.KeyboardEvent): boolean => {
    if (!open) return false;
    const ctrl = e.ctrlKey && !e.metaKey && !e.altKey;
    const down = e.key === "ArrowDown" || (ctrl && (e.key === "j" || e.key === "n"));
    const up = e.key === "ArrowUp" || (ctrl && (e.key === "k" || e.key === "p"));
    if (down) {
      e.preventDefault();
      e.stopPropagation();
      setSel((s) => Math.min(s + 1, rows.length - 1));
      return true;
    }
    if (up) {
      e.preventDefault();
      e.stopPropagation();
      setSel((s) => Math.max(s - 1, 0));
      return true;
    }
    if (e.key === "Tab" || e.key === "Enter") {
      e.preventDefault();
      e.stopPropagation();
      rows[Math.min(sel, rows.length - 1)]?.pick();
      return true;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      e.stopPropagation();
      setDismissedAt(token!.start);
      return true;
    }
    return false;
  };

  // Group consecutive rows under section headers (only "@" has several groups).
  const menu = open ? (
    <div
      data-testid="typeahead-menu"
      className="absolute left-0 top-full z-50 mt-1 max-h-[40vh] w-[320px] max-w-full overflow-y-auto rounded-lg border border-line bg-surface p-1 shadow-xl"
      // The textarea keeps focus; picking must not blur it first.
      onMouseDown={(e) => e.preventDefault()}
    >
      {rows.map((r, i) => (
        <div key={r.key}>
          {(i === 0 || rows[i - 1].group !== r.group) && r.group !== "" && (
            <div className="px-2 pb-0.5 pt-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-faint">
              {r.group}
            </div>
          )}
          <button
            type="button"
            data-testid="typeahead-row"
            onMouseEnter={() => setSel(i)}
            onClick={r.pick}
            ref={(el) => {
              if (i === sel) el?.scrollIntoView({ block: "nearest" });
            }}
            className={`flex w-full items-center justify-between gap-3 rounded-md px-2 py-1.5 text-left ${
              i === sel ? "bg-accent/12 text-accent" : "text-ink"
            }`}
          >
            <span className="min-w-0 truncate">{r.node}</span>
            {r.hint && <span className="shrink-0 font-mono text-[10.5px] text-faint">{r.hint}</span>}
          </button>
        </div>
      ))}
      <div className="border-t border-line px-2 py-1 font-mono text-[10px] text-faint">
        tab/enter to accept · esc to dismiss
      </div>
    </div>
  ) : null;

  return { menu, onKeyDown, onChange: handleChange, open };
}
