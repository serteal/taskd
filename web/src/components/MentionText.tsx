import type { ReactNode } from "react";
import { isMentionURL, mentionTaskId, parseMentions } from "../lib/mentions";
import { uiBridge } from "../lib/extensions";
import { Icon } from "./icons";

// Renders a text that may contain @-mention markup (`@[title](ref)`) as plain
// text with inline chips. A task mention opens that task's detail; a URL
// mention opens the link. Used anywhere a stored title is DISPLAYED (rows,
// detail header); edit surfaces keep the raw markup.
export function MentionText({ text }: { text: string }) {
  const mentions = parseMentions(text);
  if (mentions.length === 0) return <>{text}</>;

  const nodes: ReactNode[] = [];
  let cursor = 0;
  mentions.forEach((m, i) => {
    if (m.start > cursor) nodes.push(text.slice(cursor, m.start));
    const taskId = mentionTaskId(m.ref);
    const url = isMentionURL(m.ref) ? m.ref : undefined;
    nodes.push(
      <button
        key={i}
        data-testid="mention-chip"
        title={url ?? m.title}
        onClick={(e) => {
          e.stopPropagation();
          if (taskId) uiBridge.openTask(taskId);
          else if (url) window.open(url, "_blank", "noreferrer");
        }}
        className="mx-0.5 inline-flex max-w-[220px] items-center gap-1 rounded-full border border-accent/35 bg-accent/[.08] px-1.5 py-0 align-baseline text-[0.92em] leading-[1.35] text-accent hover:bg-accent/[.16]"
      >
        <Icon name={url ? "open" : "circle"} size={10} className="shrink-0 opacity-70" />
        <span className="truncate">{m.title}</span>
      </button>,
    );
    cursor = m.end;
  });
  if (cursor < text.length) nodes.push(text.slice(cursor));
  return <>{nodes}</>;
}
