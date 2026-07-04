import { useNow, useSnapshot, useTheme } from "../lib/hooks";
import { matchesView, sameView, viewTitle, type View } from "../lib/views";
import { chipParts } from "../lib/format";

// Everything below the fixed views is computed from the live replica —
// projects are the "project:" labels in use, sources are whatever syncers
// exist. Using a label makes it appear here; that is the whole feature.
export function Sidebar({
  view,
  onNavigate,
}: {
  view: View;
  onNavigate: (v: View) => void;
}) {
  const snap = useSnapshot();
  const now = useNow();
  const [dark, toggleTheme] = useTheme();
  const tasks = [...snap.tasks.values()];

  const count = (v: View) => tasks.filter((t) => matchesView(t, v, now)).length;

  const labelCounts = new Map<string, number>();
  const sourceCounts = new Map<string, number>();
  for (const t of tasks) {
    for (const l of t.labels) labelCounts.set(l, (labelCounts.get(l) ?? 0) + 1);
    if (t.source !== "") sourceCounts.set(t.source, (sourceCounts.get(t.source) ?? 0) + 1);
  }
  const projects = [...labelCounts.keys()].filter((l) => chipParts(l).ns === "project").sort();
  const plain = [...labelCounts.keys()].filter((l) => chipParts(l).ns !== "project").sort();
  const sources = [...sourceCounts.keys()].sort();

  const fixed: View[] = [
    { kind: "inbox" },
    { kind: "today" },
    { kind: "upcoming" },
    { kind: "all" },
    { kind: "completed" },
  ];

  return (
    <aside className="flex w-52 shrink-0 flex-col border-r border-line bg-surface">
      <div className="px-3 pb-3 pt-4 font-mono text-[15px] font-medium tracking-tight">
        taskd<span className="caret text-accent">_</span>
      </div>

      <nav className="min-h-0 flex-1 overflow-y-auto pb-4">
        <ul>
          {fixed.map((v) => (
            <SideItem
              key={viewTitle(v)}
              label={viewTitle(v)}
              count={v.kind === "completed" ? undefined : count(v)}
              active={sameView(view, v)}
              onClick={() => onNavigate(v)}
            />
          ))}
        </ul>

        {projects.length > 0 && (
          <SideSection title="projects">
            {projects.map((l) => (
              <SideItem
                key={l}
                label={chipParts(l).val}
                count={labelCounts.get(l)}
                active={sameView(view, { kind: "label", label: l })}
                onClick={() => onNavigate({ kind: "label", label: l })}
              />
            ))}
          </SideSection>
        )}

        {plain.length > 0 && (
          <SideSection title="labels">
            {plain.map((l) => (
              <SideItem
                key={l}
                label={l}
                count={labelCounts.get(l)}
                active={sameView(view, { kind: "label", label: l })}
                onClick={() => onNavigate({ kind: "label", label: l })}
              />
            ))}
          </SideSection>
        )}

        {sources.length > 0 && (
          <SideSection title="sources">
            {sources.map((s) => (
              <SideItem
                key={s}
                label={s}
                mono
                count={sourceCounts.get(s)}
                active={sameView(view, { kind: "source", source: s })}
                onClick={() => onNavigate({ kind: "source", source: s })}
              />
            ))}
          </SideSection>
        )}
      </nav>

      <button
        onClick={toggleTheme}
        className="border-t border-line px-3 py-2 text-left font-mono text-[11px] text-mute hover:text-ink"
      >
        theme: {dark ? "dusk" : "paper"}
      </button>
    </aside>
  );
}

function SideSection({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mt-4">
      <div className="px-3 pb-1 font-mono text-[10px] uppercase tracking-[0.16em] text-faint">
        {title}
      </div>
      <ul>{children}</ul>
    </div>
  );
}

function SideItem({
  label,
  count,
  active,
  mono,
  onClick,
}: {
  label: string;
  count?: number;
  active: boolean;
  mono?: boolean;
  onClick: () => void;
}) {
  return (
    <li>
      <button
        onClick={onClick}
        className={`flex w-full items-center justify-between px-3 py-[5px] text-left text-[13px] ${
          active
            ? "bg-accent/10 font-medium text-accent"
            : "text-ink hover:bg-ink/[.04] dark:hover:bg-ink/[.07]"
        } ${mono ? "font-mono text-[12px]" : ""}`}
      >
        <span className="truncate">{label}</span>
        {count !== undefined && count > 0 && (
          <span className={`font-mono text-[11px] ${active ? "text-accent" : "text-faint"}`}>
            {count}
          </span>
        )}
      </button>
    </li>
  );
}
