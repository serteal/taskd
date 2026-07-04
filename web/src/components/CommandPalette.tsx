import { useEffect, useMemo, useRef, useState } from "react";
import {
  buildStaticCommands,
  scoreMatch,
  searchTaskCommands,
  type CommandContext,
  type PaletteCommand,
} from "../lib/commands";
import { notify } from "../lib/notify";

// ⌘K palette: fuzzy input over navigation, actions, the selected task,
// extension commands, and live task search. Keyboard-first (↑/↓/Enter/Esc).
export function CommandPalette({ ctx, onClose }: { ctx: CommandContext; onClose: () => void }) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  useEffect(() => inputRef.current?.focus(), []);

  const results = useMemo<PaletteCommand[]>(() => {
    const statics = buildStaticCommands(ctx);
    const filtered =
      query.trim() === ""
        ? statics
        : statics
            .map((c) => ({ c, s: scoreMatch(`${c.title} ${c.group} ${c.keywords ?? ""}`, query) }))
            .filter((x) => x.s >= 0)
            .sort((a, b) => a.s - b.s)
            .map((x) => x.c);
    return [...filtered, ...searchTaskCommands(ctx, query)];
  }, [ctx, query]);

  useEffect(() => setActive(0), [query]);

  const run = (c: PaletteCommand | undefined) => {
    if (!c) return;
    onClose();
    // A command (often an extension's) throwing must not crash the app — an
    // error boundary can't catch this event-handler path.
    try {
      c.run();
    } catch (err) {
      notify.error("That command failed.");
      console.error("taskd: command failed:", err);
    }
  };

  // Group consecutive results by their group for section headers.
  const groups = useMemo(() => {
    const g: { name: string; items: { cmd: PaletteCommand; index: number }[] }[] = [];
    results.forEach((cmd, index) => {
      const last = g[g.length - 1];
      if (last && last.name === cmd.group) last.items.push({ cmd, index });
      else g.push({ name: cmd.group, items: [{ cmd, index }] });
    });
    return g;
  }, [results]);

  return (
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/30 px-4 pt-[12vh]"
      onMouseDown={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Command palette"
        className="flex max-h-[70vh] w-[560px] max-w-full flex-col overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose();
          else if (e.key === "ArrowDown") {
            e.preventDefault();
            setActive((a) => Math.min(a + 1, results.length - 1));
          } else if (e.key === "ArrowUp") {
            e.preventDefault();
            setActive((a) => Math.max(a - 1, 0));
          } else if (e.key === "Enter") {
            e.preventDefault();
            run(results[active]);
          }
        }}
      >
        <input
          ref={inputRef}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search tasks, jump to a view, run a command…"
          aria-label="Command palette"
          className="border-b border-line bg-transparent px-4 py-3 text-[14px] placeholder:text-faint focus:outline-none"
        />
        <div ref={listRef} className="min-h-0 flex-1 overflow-y-auto p-1">
          {results.length === 0 && (
            <div className="px-3 py-6 text-center text-[13px] text-mute">No matches</div>
          )}
          {groups.map((g) => (
            <div key={g.name} className="mb-1">
              <div className="px-2 pb-0.5 pt-1.5 font-mono text-[10px] uppercase tracking-[0.14em] text-faint">
                {g.name}
              </div>
              {g.items.map(({ cmd, index }) => (
                <button
                  key={cmd.id}
                  onMouseEnter={() => setActive(index)}
                  onClick={() => run(cmd)}
                  ref={(el) => {
                    if (index === active) el?.scrollIntoView({ block: "nearest" });
                  }}
                  className={`flex w-full items-center gap-2.5 rounded-md px-2 py-1.5 text-left text-[13px] ${
                    index === active ? "bg-accent/12 text-accent" : "text-ink"
                  }`}
                >
                  <span
                    className={`flex w-5 shrink-0 items-center justify-center ${
                      index === active ? "text-accent" : "text-mute"
                    }`}
                  >
                    {cmd.icon}
                  </span>
                  <span className="truncate">{cmd.title}</span>
                </button>
              ))}
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}
