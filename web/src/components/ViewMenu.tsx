import { Popover } from "./Popover";
import { Icon, type IconName } from "./icons";
import { SORT_MODES, SORT_LABELS, type SortMode, type BoardGroupBy } from "../lib/views";

// The header's "View options" control — a single tidy button that opens a
// popover, replacing the old raw <select>s and list/board text toggle. It is
// pure presentation: it just reports the chosen layout / sort / group-by back
// to App, which owns the state (and its sticky persistence).

const GROUP_OPTIONS: { value: BoardGroupBy; label: string }[] = [
  { value: "priority", label: "Priority" },
  { value: "project", label: "Project" },
];

export function ViewMenu({
  board,
  onBoardChange,
  sort,
  onSortChange,
  groupBy,
  onGroupByChange,
}: {
  board: boolean;
  onBoardChange: (b: boolean) => void;
  sort: SortMode;
  onSortChange: (s: SortMode) => void;
  groupBy: BoardGroupBy;
  onGroupByChange: (g: BoardGroupBy) => void;
}) {
  return (
    <Popover
      align="right"
      trigger={({ open, toggle }) => (
        <button
          type="button"
          onClick={toggle}
          aria-label="View options"
          aria-haspopup="menu"
          aria-expanded={open}
          className={`flex items-center gap-1.5 rounded-md border px-2 py-1 text-[12.5px] ${
            open
              ? "border-mute text-ink"
              : "border-line text-mute hover:border-mute hover:text-ink"
          }`}
        >
          <Icon name={board ? "board" : "list"} size={14} />
          <span>View</span>
        </button>
      )}
    >
      {() => (
        <div className="w-56">
          {/* Layout: a segmented List / Board toggle. */}
          <div
            role="group"
            aria-label="Layout"
            className="flex gap-0.5 rounded-md bg-ink/[.04] p-0.5 dark:bg-ink/[.08]"
          >
            <SegButton active={!board} icon="list" label="List" onClick={() => onBoardChange(false)} />
            <SegButton active={board} icon="board" label="Board" onClick={() => onBoardChange(true)} />
          </div>

          <MenuGroup title="Sort by">
            {SORT_MODES.map((m) => (
              <OptionRow
                key={m}
                label={SORT_LABELS[m]}
                selected={sort === m}
                onClick={() => onSortChange(m)}
              />
            ))}
          </MenuGroup>

          {/* Group-by only applies to the board layout. */}
          {board && (
            <MenuGroup title="Group by">
              {GROUP_OPTIONS.map((g) => (
                <OptionRow
                  key={g.value}
                  label={g.label}
                  selected={groupBy === g.value}
                  onClick={() => onGroupByChange(g.value)}
                />
              ))}
            </MenuGroup>
          )}
        </div>
      )}
    </Popover>
  );
}

function SegButton({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean;
  icon: IconName;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      className={`flex flex-1 items-center justify-center gap-1.5 rounded px-2 py-1 text-[12.5px] ${
        active ? "bg-surface text-ink shadow-sm ring-1 ring-line" : "text-mute hover:text-ink"
      }`}
    >
      <Icon name={icon} size={14} />
      {label}
    </button>
  );
}

function MenuGroup({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mt-1.5">
      <div className="px-2 pb-0.5 pt-1 font-mono text-[10px] uppercase tracking-[0.14em] text-faint">
        {title}
      </div>
      {children}
    </div>
  );
}

function OptionRow({
  label,
  selected,
  onClick,
}: {
  label: string;
  selected: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      role="radio"
      aria-checked={selected}
      onClick={onClick}
      className={`flex w-full items-center justify-between gap-3 rounded-md px-2 py-1.5 text-left text-[13px] ${
        selected ? "text-accent" : "text-ink hover:bg-ink/[.05] dark:hover:bg-ink/[.08]"
      }`}
    >
      <span>{label}</span>
      {selected && <Icon name="check" size={14} />}
    </button>
  );
}
