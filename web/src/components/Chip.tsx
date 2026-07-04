import { chipParts } from "../lib/format";

// The two-tone chip is the label model made visible: the namespace
// ("project:", "context:") is convention, so it renders quiet; the value is
// the meaning, so it renders in ink. Priorities get the accent.
export function Chip({ label, onRemove }: { label: string; onRemove?: () => void }) {
  const { ns, val } = chipParts(label);
  const priority = /^p[1-3]$/.test(label);
  return (
    <span
      className={`inline-flex items-center gap-0.5 rounded-full border border-line bg-surface px-1.5 py-px font-mono text-[11px] leading-4 ${
        priority ? "text-accent border-accent/40" : "text-ink"
      }`}
    >
      {ns && <span className="text-faint">{ns}:</span>}
      <span>{val}</span>
      {onRemove && (
        <button
          onClick={onRemove}
          aria-label={`Remove label ${label}`}
          className="ml-0.5 -mr-0.5 rounded-full px-0.5 text-mute hover:text-warn"
        >
          ×
        </button>
      )}
    </span>
  );
}
