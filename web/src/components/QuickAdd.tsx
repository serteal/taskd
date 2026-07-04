import { forwardRef, useState } from "react";
import { useStore } from "../lib/hooks";
import { parseQuickAdd } from "../lib/quickadd";
import { humanDue } from "../lib/format";
import { Chip } from "./Chip";
import type { View } from "../lib/views";

// One input, the whole capture flow. Tokens are parsed live and shown as
// chips before anything is created, so the grammar teaches itself.
export const QuickAdd = forwardRef<HTMLInputElement, { view: View; now: Date }>(
  function QuickAdd({ view, now }, ref) {
    const store = useStore();
    const [text, setText] = useState("");
    const parsed = parseQuickAdd(text, now);
    const hasTokens = parsed.labels.length > 0 || parsed.due !== undefined;

    const submit = async () => {
      if (parsed.title.trim() === "") return;
      // Adding inside a label/project view files the task there.
      const labels = [...parsed.labels];
      if (view.kind === "label" && !labels.includes(view.label)) labels.push(view.label);
      const due =
        parsed.due ?? (view.kind === "today" ? parseQuickAdd("x today", now).due : undefined);
      setText("");
      try {
        await store.create({ title: parsed.title, labels, due });
      } catch {
        setText(text); // give the input back; the toast explains
      }
    };

    return (
      <div className="border-b border-line px-3 py-2">
        <div className="flex items-center gap-2">
          <span className="font-mono text-[13px] text-accent" aria-hidden>
            +
          </span>
          <input
            ref={ref}
            value={text}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void submit();
              if (e.key === "Escape") (e.target as HTMLInputElement).blur();
            }}
            placeholder='Add a task — try "review the PR #project:taskd p1 tomorrow"'
            aria-label="Add a task"
            className="w-full bg-transparent py-0.5 text-[13.5px] placeholder:text-faint focus:outline-none"
          />
        </div>
        {hasTokens && (
          <div className="mt-1.5 flex items-center gap-1.5 pl-5">
            {parsed.labels.map((l) => (
              <Chip key={l} label={l} />
            ))}
            {parsed.due && (
              <span className="font-mono text-[11px] text-accent">
                due {humanDue(parsed.due, now).text}
              </span>
            )}
          </div>
        )}
      </div>
    );
  },
);
