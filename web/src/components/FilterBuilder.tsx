import { useEffect, useMemo, useRef, useState } from "react";
import { useNow, useSnapshot } from "../lib/hooks";
import {
  matchesFilter,
  savedFilters,
  type FilterPredicate,
  type SavedFilter,
} from "../lib/filters";
import { Chip } from "./Chip";
import { Popover } from "./Popover";
import { LabelMenu } from "./pickers";
import { Icon } from "./icons";

// The filter builder is the authoring surface for the promotion mechanism: it
// composes a FilterPredicate over labels / source / text / due, then persists
// it as a SavedFilter that pins to the sidebar's Filters section. It reads the
// live replica for its label and source options, so "the sources present" are
// exactly what you can pick from. Modeled on the New-task overlay (backdrop +
// Escape + autofocus); App swallows global keys while it is open.

const ANY = "__any__"; // sentinel select value for "any source"

type DueMode = "any" | "has" | "within";

export function FilterBuilder({
  initial,
  onClose,
  onSaved,
}: {
  /** When set, edit this filter in place instead of creating a new one. */
  initial?: SavedFilter;
  onClose: () => void;
  onSaved: (f: SavedFilter) => void;
}) {
  const snap = useSnapshot();
  const now = useNow();
  const nameRef = useRef<HTMLInputElement>(null);
  useEffect(() => nameRef.current?.focus(), []);

  const p = initial?.predicate ?? {};
  const [name, setName] = useState(initial?.name ?? "");
  const [labelsAny, setLabelsAny] = useState<string[]>(p.labelsAny ?? []);
  const [labelsAll, setLabelsAll] = useState<string[]>(p.labelsAll ?? []);
  const [source, setSource] = useState<string>(p.source ?? ANY);
  const [text, setText] = useState(p.text ?? "");
  const [dueMode, setDueMode] = useState<DueMode>(
    p.dueWithinDays !== undefined ? "within" : p.hasDue ? "has" : "any",
  );
  const [dueDays, setDueDays] = useState<number>(p.dueWithinDays ?? 7);
  const [includeCompleted, setIncludeCompleted] = useState(p.includeCompleted ?? false);

  // Label + source options come straight from the live replica (synced tasks
  // included — promoting them is the point).
  const { labelOptions, sourceOptions } = useMemo(() => {
    const labels = new Set<string>();
    const sources = new Set<string>();
    for (const t of snap.tasks.values()) {
      for (const l of t.labels) labels.add(l);
      if (t.source !== "") sources.add(t.source);
    }
    return { labelOptions: [...labels].sort(), sourceOptions: [...sources].sort() };
  }, [snap]);

  const predicate = useMemo<FilterPredicate>(() => {
    const out: FilterPredicate = {};
    if (labelsAll.length > 0) out.labelsAll = labelsAll;
    if (labelsAny.length > 0) out.labelsAny = labelsAny;
    if (source !== ANY) out.source = source;
    if (text.trim() !== "") out.text = text.trim();
    if (dueMode === "has") out.hasDue = true;
    if (dueMode === "within") out.dueWithinDays = dueDays;
    if (includeCompleted) out.includeCompleted = true;
    return out;
  }, [labelsAll, labelsAny, source, text, dueMode, dueDays, includeCompleted]);

  // Live preview of how many active tasks this predicate would surface.
  const matchCount = useMemo(
    () => [...snap.tasks.values()].filter((t) => matchesFilter(predicate, t, now)).length,
    [snap, predicate, now],
  );

  const canSave = name.trim() !== "";
  const save = () => {
    if (!canSave) return;
    const clean = name.trim();
    if (initial) {
      savedFilters.update(initial.id, { name: clean, predicate });
      onSaved({ ...initial, name: clean, predicate });
    } else {
      onSaved(savedFilters.add({ name: clean, predicate }));
    }
  };

  return (
    <div
      className="fixed inset-0 z-40 flex items-start justify-center bg-black/30 px-4 pt-[12vh]"
      onMouseDown={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Filter builder"
        data-testid="filter-builder"
        className="w-[520px] max-w-full overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose();
          if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) save();
        }}
      >
        <div className="flex items-baseline justify-between border-b border-line px-4 py-3">
          <h2 className="text-[15px] font-semibold">{initial ? "Edit filter" : "New filter"}</h2>
          <button onClick={onClose} className="font-mono text-[12px] text-mute hover:text-ink">
            esc
          </button>
        </div>

        <div className="space-y-4 p-4">
          <div>
            <input
              ref={nameRef}
              value={name}
              onChange={(e) => setName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && canSave) save();
              }}
              placeholder="Filter name (e.g. Reviews)"
              aria-label="Filter name"
              className="w-full bg-transparent text-[17px] font-semibold leading-tight placeholder:text-faint focus:outline-none"
            />
          </div>

          <FieldRow label="Labels — any of">
            <div className="flex flex-wrap items-center gap-1.5">
              {labelsAny.map((l) => (
                <Chip key={l} label={l} onRemove={() => setLabelsAny(labelsAny.filter((x) => x !== l))} />
              ))}
              <ChipAdd
                aria="Add any-of label"
                options={labelOptions}
                chosen={labelsAny}
                onAdd={(l) => setLabelsAny([...labelsAny, l])}
              />
            </div>
          </FieldRow>

          <FieldRow label="Labels — all of">
            <div className="flex flex-wrap items-center gap-1.5">
              {labelsAll.map((l) => (
                <Chip key={l} label={l} onRemove={() => setLabelsAll(labelsAll.filter((x) => x !== l))} />
              ))}
              <ChipAdd
                aria="Add all-of label"
                options={labelOptions}
                chosen={labelsAll}
                onAdd={(l) => setLabelsAll([...labelsAll, l])}
              />
            </div>
          </FieldRow>

          <FieldRow label="Source">
            <select
              aria-label="Source"
              value={source}
              onChange={(e) => setSource(e.target.value)}
              className="rounded-md border border-line bg-paper px-2 py-1 text-[13px] focus:outline-none"
            >
              <option value={ANY}>Any source</option>
              {sourceOptions.map((s) => (
                <option key={s} value={s}>
                  {s}
                </option>
              ))}
            </select>
          </FieldRow>

          <FieldRow label="Text">
            <input
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder="Matches title or notes"
              aria-label="Text contains"
              className="w-full rounded-md border border-line bg-paper px-2 py-1 text-[13px] focus:outline-none"
            />
          </FieldRow>

          <FieldRow label="Due">
            <div className="flex items-center gap-1.5">
              {(["any", "has", "within"] as const).map((m) => (
                <button
                  key={m}
                  onClick={() => setDueMode(m)}
                  aria-pressed={dueMode === m}
                  className={`rounded-md border px-2 py-1 text-[12.5px] ${
                    dueMode === m
                      ? "border-accent/50 bg-accent/10 text-accent"
                      : "border-line text-mute hover:border-mute hover:text-ink"
                  }`}
                >
                  {m === "any" ? "Any" : m === "has" ? "Has due date" : "Within N days"}
                </button>
              ))}
              {dueMode === "within" && (
                <input
                  type="number"
                  min={0}
                  value={dueDays}
                  onChange={(e) => setDueDays(Math.max(0, Number(e.target.value) || 0))}
                  aria-label="Days"
                  className="w-16 rounded-md border border-line bg-paper px-2 py-1 font-mono text-[12.5px] focus:outline-none"
                />
              )}
            </div>
          </FieldRow>

          <label className="flex cursor-pointer select-none items-center gap-2 text-[13px] text-ink">
            <input
              type="checkbox"
              checked={includeCompleted}
              onChange={(e) => setIncludeCompleted(e.target.checked)}
            />
            Include completed
          </label>
        </div>

        <div className="flex items-center justify-between border-t border-line px-4 py-2.5">
          <span className="font-mono text-[11px] text-faint" data-testid="filter-preview">
            {matchCount} matching
          </span>
          <div className="flex items-center gap-2">
            <button
              onClick={onClose}
              className="rounded-md bg-ink/[.06] px-3 py-1.5 text-[13px] font-medium text-ink hover:bg-ink/[.1] dark:bg-ink/[.1] dark:hover:bg-ink/[.16]"
            >
              Cancel
            </button>
            <button
              onClick={save}
              disabled={!canSave}
              className="rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white disabled:cursor-not-allowed disabled:opacity-40"
            >
              Save filter
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

function FieldRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-faint">{label}</span>
      {children}
    </div>
  );
}

// A "+ label" pill that opens the shared LabelMenu, filtered to labels not yet
// chosen for this group.
function ChipAdd({
  aria,
  options,
  chosen,
  onAdd,
}: {
  aria: string;
  options: string[];
  chosen: string[];
  onAdd: (label: string) => void;
}) {
  return (
    <Popover
      trigger={({ toggle }) => (
        <button
          type="button"
          aria-label={aria}
          onClick={toggle}
          className="flex items-center gap-1 rounded-full border border-dashed border-line px-2 py-px text-[11px] text-mute hover:border-mute hover:text-ink"
        >
          <Icon name="plus" size={11} strokeWidth={2.5} />
          label
        </button>
      )}
    >
      {(close) => (
        <LabelMenu
          labels={chosen}
          options={options}
          onAdd={(l) => {
            onAdd(l);
            close();
          }}
        />
      )}
    </Popover>
  );
}
