import { useMemo, useState } from "react";
import { endOfDay } from "../lib/format";
import { MenuItem } from "./Popover";

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
            <span aria-hidden style={{ color: p.color }}>
              ⚑
            </span>
            {p.label}
          </span>
        </MenuItem>
      ))}
      <MenuItem onClick={() => pick(null)}>No priority</MenuItem>
    </div>
  );
}

// Add-a-label menu with autocomplete over existing labels.
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
  const matches = useMemo(
    () => options.filter((l) => !labels.includes(l) && l.toLowerCase().includes(query.toLowerCase())),
    [options, labels, query],
  );
  const add = (l: string) => {
    onAdd(l);
    setQuery("");
  };
  return (
    <div className="min-w-[200px]">
      <input
        autoFocus
        value={query}
        onChange={(e) => setQuery(e.target.value)}
        onKeyDown={(e) => e.key === "Enter" && query.trim() && add(query.trim())}
        placeholder="Type a label…"
        className="mb-1 w-full rounded border border-line bg-paper px-1.5 py-1 text-[13px] focus:outline-none"
      />
      {query.trim() && !options.includes(query.trim()) && (
        <MenuItem onClick={() => add(query.trim())} hint="new">
          Create “{query.trim()}”
        </MenuItem>
      )}
      {matches.slice(0, 8).map((l) => (
        <MenuItem key={l} onClick={() => add(l)}>
          {l}
        </MenuItem>
      ))}
      {matches.length === 0 && !query && (
        <div className="px-2 py-1.5 text-[12px] text-faint">No labels yet</div>
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
