import { useMemo, useState } from "react";
import { endOfDay } from "../lib/format";
import { MenuItem } from "./Popover";
import { Icon } from "./icons";

// Menu bodies shared by the new-task overlay, row hover actions, inline
// reschedule, and the bulk bar. Each renders inside a Popover and calls back
// with the chosen value; `close` dismisses the popover.

function addDays(d: Date, n: number): Date {
  const out = new Date(d);
  out.setDate(out.getDate() + n);
  return out;
}
// Next occurrence of weekday `dow` (0=Sun); today counts when includeToday.
function nextDow(now: Date, dow: number, includeToday: boolean): Date {
  let delta = (dow - now.getDay() + 7) % 7;
  if (delta === 0 && !includeToday) delta = 7;
  return endOfDay(addDays(now, delta));
}

export const PRIORITIES: { label: string; value: string; color: string }[] = [
  { label: "Priority 1", value: "p1", color: "var(--warn)" },
  { label: "Priority 2", value: "p2", color: "#d98a2b" },
  { label: "Priority 3", value: "p3", color: "var(--accent)" },
];

export function ScheduleMenu({
  now,
  onChange,
  close,
}: {
  now: Date;
  onChange: (d: Date | null) => void;
  close: () => void;
}) {
  const pick = (d: Date | null) => {
    onChange(d);
    close();
  };
  return (
    <div>
      <MenuItem onClick={() => pick(endOfDay(now))} hint="today">
        Today
      </MenuItem>
      <MenuItem onClick={() => pick(endOfDay(addDays(now, 1)))} hint="tomorrow">
        Tomorrow
      </MenuItem>
      <MenuItem onClick={() => pick(nextDow(now, 6, true))} hint="sat">
        This weekend
      </MenuItem>
      <MenuItem onClick={() => pick(nextDow(now, 1, false))} hint="mon">
        Next week
      </MenuItem>
      <MenuItem onClick={() => pick(null)}>No date</MenuItem>
      <div className="mt-1 border-t border-line px-2 pb-1 pt-2">
        <input
          type="datetime-local"
          onChange={(e) => e.target.value && pick(new Date(e.target.value))}
          aria-label="Custom date and time"
          className="w-full rounded border border-line bg-paper px-1.5 py-1 font-mono text-[12px] focus:outline-none"
        />
      </div>
    </div>
  );
}

export function PriorityMenu({
  onChange,
  close,
}: {
  onChange: (p: string | null) => void;
  close: () => void;
}) {
  const pick = (p: string | null) => {
    onChange(p);
    close();
  };
  return (
    <div>
      {PRIORITIES.map((p) => (
        <MenuItem key={p.value} onClick={() => pick(p.value)} hint={p.value}>
          <span className="inline-flex items-center gap-2">
            <span style={{ color: p.color }}>
              <Icon name="flag" size={13} />
            </span>
            {p.label}
          </span>
        </MenuItem>
      ))}
      <MenuItem onClick={() => pick(null)}>No priority</MenuItem>
    </div>
  );
}

// Add-a-label menu with autocomplete over existing labels. Fully
// keyboard-driven: ↑/↓ (or ctrl+j/k, ctrl+n/p) move, Tab/Enter accept the
// highlighted row, and a query matching no existing label offers a create row
// (the label exists once it's on a task — labels are just strings).
export function LabelMenu({
  labels,
  options,
  onAdd,
}: {
  labels: string[];
  options: string[];
  onAdd: (label: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const matches = useMemo(
    () =>
      options
        .filter((l) => !labels.includes(l) && l.toLowerCase().includes(query.toLowerCase()))
        .slice(0, 8),
    [options, labels, query],
  );
  // The pickable rows in display order: the create row (when the query names
  // a new label) first, then the matches.
  const creating = query.trim() !== "" && !options.includes(query.trim());
  const rows: { key: string; label: string; hint?: string }[] = [
    ...(creating ? [{ key: "__new__", label: query.trim(), hint: "new" }] : []),
    ...matches.map((l) => ({ key: l, label: l })),
  ];
  const add = (l: string) => {
    onAdd(l);
    setQuery("");
    setActive(0);
  };
  return (
    <div className="min-w-[200px]">
      <input
        autoFocus
        value={query}
        onChange={(e) => {
          setQuery(e.target.value);
          setActive(0);
        }}
        onKeyDown={(e) => {
          const ctrl = e.ctrlKey && !e.metaKey && !e.altKey;
          const down = e.key === "ArrowDown" || (ctrl && (e.key === "j" || e.key === "n"));
          const up = e.key === "ArrowUp" || (ctrl && (e.key === "k" || e.key === "p"));
          // Handled keys must not leak to the surface behind the popover
          // (ctrl+j/k also steps the detail modal through the list).
          if (down) {
            e.preventDefault();
            e.stopPropagation();
            setActive((a) => Math.min(a + 1, rows.length - 1));
          } else if (up) {
            e.preventDefault();
            e.stopPropagation();
            setActive((a) => Math.max(a - 1, 0));
          } else if ((e.key === "Enter" || e.key === "Tab") && rows.length > 0) {
            e.preventDefault();
            e.stopPropagation();
            add(rows[Math.min(active, rows.length - 1)].label);
          }
        }}
        placeholder="Type a label…"
        aria-label="Label search"
        className="mb-1 w-full rounded border border-line bg-paper px-1.5 py-1 text-[13px] focus:outline-none"
      />
      {rows.map((r, i) => (
        <button
          key={r.key}
          type="button"
          onMouseEnter={() => setActive(i)}
          onClick={() => add(r.label)}
          className={`flex w-full items-center justify-between gap-3 rounded-md px-2 py-1.5 text-left text-[13px] ${
            i === active ? "bg-accent/12 text-accent" : "text-ink"
          }`}
        >
          <span className="truncate">{r.key === "__new__" ? `Create “${r.label}”` : r.label}</span>
          {r.hint && <span className="font-mono text-[11px] text-faint">{r.hint}</span>}
        </button>
      ))}
      {rows.length === 0 && (
        <div className="px-2 py-1.5 text-[12px] text-faint">
          {query ? "Already added" : "No labels yet"}
        </div>
      )}
    </div>
  );
}

export function ProjectMenu({
  options,
  onChange,
  close,
}: {
  options: string[];
  onChange: (project: string | null) => void;
  close: () => void;
}) {
  const pick = (p: string | null) => {
    onChange(p);
    close();
  };
  const projVal = (l: string) => l.slice("project:".length);
  return (
    <div>
      <MenuItem onClick={() => pick(null)}>Inbox</MenuItem>
      {options.map((p) => (
        <MenuItem key={p} onClick={() => pick(p)}>
          {projVal(p)}
        </MenuItem>
      ))}
    </div>
  );
}
