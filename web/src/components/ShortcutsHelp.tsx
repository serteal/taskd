import { ACTIONS, prettyBinding, useKeymap } from "../lib/keymap";

// A keyboard cheat sheet, opened with `?` (by default). Rendered live from the
// keymap so rebinds in Settings → Keybindings show here immediately; the
// fixed (non-rebindable) keys are appended below.
const FIXED: [string, string][] = [
  ["Esc", "Close / clear selection"],
  ["⌘/⇧-click", "Multi-select rows"],
  ["↑↓ · ^j ^k · ^n ^p", "Move inside menus & dialogs"],
  ["Tab / ⏎", "Accept the highlighted suggestion"],
];

export function ShortcutsHelp({
  onClose,
  onOpenKeybindings,
}: {
  onClose: () => void;
  /** Jump to Settings → Keybindings to customize. */
  onOpenKeybindings?: () => void;
}) {
  const keymap = useKeymap();
  const groups = [...new Set(ACTIONS.map((a) => a.group))];
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 px-4"
      onMouseDown={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Keyboard shortcuts"
        className="flex max-h-[80vh] w-[460px] max-w-full flex-col rounded-xl border border-line bg-surface p-4 shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="mb-2 flex items-baseline justify-between">
          <h2 className="text-[15px] font-semibold">Keyboard shortcuts</h2>
          <button onClick={onClose} className="font-mono text-[12px] text-mute hover:text-ink">
            esc
          </button>
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">
          {groups.map((g) => (
            <div key={g} className="mb-2">
              <div className="pb-0.5 pt-1 font-mono text-[10px] uppercase tracking-[0.16em] text-faint">
                {g}
              </div>
              <dl className="divide-y divide-line/60">
                {ACTIONS.filter((a) => a.group === g).map((a) => {
                  const bindings = keymap[a.id] ?? [];
                  return (
                    <div key={a.id} className="flex items-center justify-between gap-4 py-1.5">
                      <dt className="text-[13px] text-ink">{a.title}</dt>
                      <dd className="shrink-0 font-mono text-[11px] text-mute">
                        {bindings.length === 0
                          ? "—"
                          : bindings.map((b) => prettyBinding(b)).join(" · ")}
                      </dd>
                    </div>
                  );
                })}
              </dl>
            </div>
          ))}
          <div className="mb-1">
            <div className="pb-0.5 pt-1 font-mono text-[10px] uppercase tracking-[0.16em] text-faint">
              Fixed
            </div>
            <dl className="divide-y divide-line/60">
              {FIXED.map(([keys, desc]) => (
                <div key={keys} className="flex items-center justify-between gap-4 py-1.5">
                  <dt className="text-[13px] text-ink">{desc}</dt>
                  <dd className="shrink-0 font-mono text-[11px] text-mute">{keys}</dd>
                </div>
              ))}
            </dl>
          </div>
        </div>
        {onOpenKeybindings && (
          <button
            onClick={onOpenKeybindings}
            data-testid="customize-keybindings"
            className="mt-2 self-start text-[12.5px] font-medium text-accent hover:underline"
          >
            Customize in Settings → Keybindings
          </button>
        )}
      </div>
    </div>
  );
}
