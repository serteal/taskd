const SHORTCUTS: [string, string][] = [
  ["⌘K", "Command palette & search"],
  ["q", "New task"],
  ["j / k", "Move selection"],
  ["x", "Complete (or the selection)"],
  ["e", "Edit title inline"],
  ["⏎", "Open details"],
  ["Space", "Toggle selection"],
  ["⌘/⇧-click", "Multi-select rows"],
  ["t", "Toggle the timeline panel"],
  ["Esc", "Close / clear selection"],
  ["?", "This help"],
];

// A keyboard cheat sheet, opened with `?`.
export function ShortcutsHelp({ onClose }: { onClose: () => void }) {
  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 px-4"
      onMouseDown={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Keyboard shortcuts"
        className="w-[420px] max-w-full rounded-xl border border-line bg-surface p-4 shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
      >
        <div className="mb-2 flex items-baseline justify-between">
          <h2 className="text-[15px] font-semibold">Keyboard shortcuts</h2>
          <button onClick={onClose} className="font-mono text-[12px] text-mute hover:text-ink">
            esc
          </button>
        </div>
        <dl className="divide-y divide-line/60">
          {SHORTCUTS.map(([keys, desc]) => (
            <div key={keys} className="flex items-center justify-between py-1.5">
              <dt className="text-[13px] text-ink">{desc}</dt>
              <dd className="font-mono text-[11px] text-mute">{keys}</dd>
            </div>
          ))}
        </dl>
      </div>
    </div>
  );
}
