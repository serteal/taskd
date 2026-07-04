import { useEffect, useMemo, useRef, useState } from "react";
import { useNow, useSnapshot, useStore, useTheme, useView } from "./lib/hooks";
import { viewTitle, type SortMode, SORT_LABELS } from "./lib/views";
import { endOfDay } from "./lib/format";
import { completeTask } from "./lib/actions";
import { buildAPI, registry, uiBridge, useRegistry } from "./lib/extensions";
import type { CommandContext } from "./lib/commands";
import { savedViews, type SavedView } from "./lib/savedviews";
import { Sidebar } from "./components/Sidebar";
import { TaskList, visibleTasks } from "./components/TaskList";
import { CompletedList } from "./components/CompletedList";
import { NewTaskOverlay, type NewTaskInitial } from "./components/NewTaskOverlay";
import { DetailPanel } from "./components/DetailPanel";
import { BoardView } from "./components/BoardView";
import { ToastStack } from "./components/ToastStack";
import { CommandPalette } from "./components/CommandPalette";
import { ExtensionBoundary } from "./components/ExtensionBoundary";
import { ShortcutsHelp } from "./components/ShortcutsHelp";
import { BulkBar } from "./components/BulkBar";
import { completeMany } from "./lib/actions";

const SORT_MODES: SortMode[] = ["smart", "manual", "created", "title"];

export default function App() {
  const store = useStore();
  const snap = useSnapshot();
  const now = useNow();
  const [dark, toggleTheme] = useTheme();
  const [view, navigate] = useView();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [sort, setSort] = useState<SortMode>("smart");
  const [board, setBoard] = useState(false);
  const [groupBy, setGroupBy] = useState<"priority" | "project">("priority");
  const [adding, setAdding] = useState<NewTaskInitial | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [lastClicked, setLastClicked] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const regVersion = useRegistry();

  // Which registered panels are open. Seeded from each panel's defaultOpen
  // the first time it appears; the user's toggle wins thereafter.
  const [openPanels, setOpenPanels] = useState<Set<string>>(new Set());
  const seenPanels = useRef<Set<string>>(new Set());
  useEffect(() => {
    setOpenPanels((cur) => {
      let next = cur;
      for (const p of registry.panels) {
        if (seenPanels.current.has(p.id)) continue;
        seenPanels.current.add(p.id);
        if (p.defaultOpen ?? true) {
          if (next === cur) next = new Set(cur);
          next.add(p.id);
        }
      }
      return next;
    });
  }, [regVersion]);

  // The replica loop lives exactly as long as the app.
  useEffect(() => {
    const ctl = new AbortController();
    void store.start(ctl.signal);
    return () => ctl.abort();
  }, [store]);

  // Extensions open the host detail panel through this bridge.
  useEffect(() => {
    uiBridge.openTask = (id) => {
      setSelectedId(id);
      setOpenId(id);
    };
    return () => {
      uiBridge.openTask = () => {};
    };
  }, []);

  const openTaskById = (id: string) => {
    setSelectedId(id);
    setOpenId(id);
  };

  const all = view.kind === "completed" || view.kind === "ext" ? [] : visibleTasks(snap.tasks.values(), view, now, sort);
  // Header search narrows the current view client-side (the replica is in memory).
  const q = search.trim().toLowerCase();
  const tasks = q ? all.filter((t) => `${t.title} ${t.notes}`.toLowerCase().includes(q)) : all;
  const selectedTasks = [...selected].map((id) => snap.tasks.get(id)).filter((t): t is NonNullable<typeof t> => !!t);

  // Clicking a row: plain = open detail (clears selection); ⌘/Ctrl = toggle in
  // the multi-select set; Shift = select the range from the last click.
  const activateRow = (id: string, mods: { meta: boolean; shift: boolean }) => {
    setLastClicked(id);
    if (mods.meta) {
      setSelected((s) => {
        const n = new Set(s);
        n.has(id) ? n.delete(id) : n.add(id);
        return n;
      });
      return;
    }
    if (mods.shift && lastClicked) {
      const ids = tasks.map((t) => t.id);
      const a = ids.indexOf(lastClicked);
      const b = ids.indexOf(id);
      if (a !== -1 && b !== -1) {
        const [lo, hi] = a < b ? [a, b] : [b, a];
        setSelected((s) => {
          const n = new Set(s);
          for (let i = lo; i <= hi; i++) n.add(ids[i]);
          return n;
        });
        return;
      }
    }
    setSelected(new Set());
    openTaskById(id);
  };

  const clearSelection = () => setSelected(new Set());
  // Selection is scoped to a view; leaving it clears the set.
  useEffect(clearSelection, [view.kind, (view as { label?: string }).label, (view as { source?: string }).source]);

  const applySaved = (v: SavedView) => {
    setOpenId(null);
    navigate(v.view);
    setSort(v.sort);
    setBoard(v.board);
    setGroupBy(v.groupBy);
    setSearch(v.search);
  };
  const saveCurrentView = () => {
    const name = window.prompt("Save this view as:");
    if (name?.trim()) savedViews.add({ name: name.trim(), view, sort, board, groupBy, search });
  };
  const openTask = openId !== null ? snap.tasks.get(openId) : undefined;
  const extView = view.kind === "ext" ? registry.viewById(view.id) : undefined;
  const api = useMemo(() => buildAPI(store), [store]);
  const panels = registry.panels.filter((p) => openPanels.has(p.id));
  const labelOptions = useMemo(() => {
    const s = new Set<string>();
    for (const t of snap.tasks.values()) for (const l of t.labels) s.add(l);
    return [...s].sort();
  }, [snap]);

  // The overlay opens scoped to the current view: a project view files there,
  // Today prefills today's date.
  const openAdd = () => {
    const initial: NewTaskInitial = {};
    if (view.kind === "label" && view.label.startsWith("project:")) initial.project = view.label;
    if (view.kind === "today") initial.due = endOfDay(now);
    setAdding(initial);
  };

  const togglePanel = (id: string) =>
    setOpenPanels((cur) => {
      const next = new Set(cur);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  const cmdCtx = useMemo<CommandContext>(
    () => ({
      tasks: [...snap.tasks.values()],
      store,
      selectedId,
      navigate: (v) => (setOpenId(null), navigate(v)),
      setSort,
      toggleTheme,
      panels: registry.panels.map((p) => ({ id: p.id, title: p.title })),
      togglePanel,
      openTask: openTaskById,
      openAdd,
      saveCurrentView,
      extCommands: registry.commands,
      now,
    }),
    // regVersion covers extension command/panel registration; view/now/sel change often.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [snap, store, selectedId, view, now, regVersion, sort, board, groupBy, search, dark],
  );

  // Keyboard: list navigation stays out of the way of typing.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPaletteOpen((o) => !o);
        return;
      }
      // Modal overlays own the keyboard while open; Escape always closes them
      // (even if focus has left their card).
      if (helpOpen) {
        if (e.key === "Escape") setHelpOpen(false);
        return;
      }
      if (adding) {
        if (e.key === "Escape") setAdding(null);
        return;
      }
      if (paletteOpen) {
        if (e.key === "Escape") setPaletteOpen(false);
        return;
      }
      const el = e.target as HTMLElement;
      const typing = el.tagName === "INPUT" || el.tagName === "TEXTAREA";
      if (e.key === "Escape" && !typing) {
        if (selected.size > 0) {
          clearSelection();
          return;
        }
        setOpenId(null);
        return;
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      switch (e.key) {
        case " ": {
          if (selectedId) {
            e.preventDefault();
            setLastClicked(selectedId);
            setSelected((s) => {
              const n = new Set(s);
              n.has(selectedId) ? n.delete(selectedId) : n.add(selectedId);
              return n;
            });
          }
          return;
        }
        case "q":
        case "c":
        case "/": {
          e.preventDefault();
          openAdd();
          return;
        }
        case "t": {
          if (registry.panels.length > 0) togglePanel(registry.panels[0].id);
          return;
        }
        case "?": {
          e.preventDefault();
          setHelpOpen(true);
          return;
        }
        case "j":
        case "k": {
          if (tasks.length === 0) return;
          const idx = tasks.findIndex((t) => t.id === selectedId);
          const next =
            idx === -1 ? 0 : Math.min(Math.max(idx + (e.key === "j" ? 1 : -1), 0), tasks.length - 1);
          const id = tasks[next].id;
          setSelectedId(id);
          if (openId !== null) setOpenId(id);
          document.querySelector(`[data-task-row="${id}"]`)?.scrollIntoView({ block: "nearest" });
          return;
        }
        case "x": {
          if (selected.size > 0) {
            completeMany(store, selectedTasks);
            clearSelection();
            return;
          }
          const t = tasks.find((t) => t.id === selectedId);
          if (t) completeTask(store, t);
          return;
        }
        case "e": {
          const t = tasks.find((t) => t.id === selectedId);
          if (t && t.source === "") {
            e.preventDefault();
            setEditingId(t.id);
          }
          return;
        }
        case "Enter": {
          if (selectedId !== null) setOpenId(selectedId);
          return;
        }
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [tasks, selectedId, openId, store, adding, paletteOpen, helpOpen, view, now, selected, selectedTasks]);

  const showSort = view.kind !== "completed" && view.kind !== "ext";
  const firstRun = snap.connected && snap.tasks.size === 0;

  return (
    <div className="flex h-full flex-col">
      <div className="flex min-h-0 flex-1">
        <Sidebar
          view={view}
          onNavigate={(v) => (setOpenId(null), navigate(v))}
          onAddTask={openAdd}
          onApplySaved={applySaved}
          dark={dark}
          onToggleTheme={toggleTheme}
        />

        <main className="flex min-w-0 flex-1 flex-col">
          <header className="flex items-center gap-2.5 px-3 pb-2 pt-4">
            <h1 className="text-[19px] font-semibold tracking-tight">
              {extView ? extView.title : viewTitle(view)}
            </h1>
            {showSort && <span className="font-mono text-[12px] text-faint">{tasks.length}</span>}
            <div className="ml-auto flex items-center gap-2">
              {showSort && (
                <input
                  value={search}
                  onChange={(e) => setSearch(e.target.value)}
                  onKeyDown={(e) => e.key === "Escape" && setSearch("")}
                  placeholder="Search…"
                  aria-label="Search this view"
                  className="w-28 rounded border border-line bg-surface px-2 py-0.5 text-[12px] text-ink placeholder:text-faint focus:w-40 focus:outline-none"
                />
              )}
              {showSort && !board && (
                <label className="flex items-center gap-1 font-mono text-[11px] text-faint">
                  <span className="hidden sm:inline">sort</span>
                  <select
                    value={sort}
                    onChange={(e) => setSort(e.target.value as SortMode)}
                    className="cursor-pointer rounded border border-line bg-surface px-1 py-0.5 text-[11px] text-ink focus:outline-none"
                  >
                    {SORT_MODES.map((m) => (
                      <option key={m} value={m}>
                        {SORT_LABELS[m]}
                      </option>
                    ))}
                  </select>
                </label>
              )}
              {showSort && board && (
                <label className="flex items-center gap-1 font-mono text-[11px] text-faint">
                  <span className="hidden sm:inline">group</span>
                  <select
                    value={groupBy}
                    onChange={(e) => setGroupBy(e.target.value as "priority" | "project")}
                    className="cursor-pointer rounded border border-line bg-surface px-1 py-0.5 text-[11px] text-ink focus:outline-none"
                  >
                    <option value="priority">Priority</option>
                    <option value="project">Project</option>
                  </select>
                </label>
              )}
              {showSort && (
                <button
                  onClick={() => setBoard((b) => !b)}
                  title={board ? "List view" : "Board view"}
                  className="rounded border border-line px-1.5 py-0.5 font-mono text-[11px] text-mute hover:text-ink"
                >
                  {board ? "list" : "board"}
                </button>
              )}
              {registry.panels.map((p) => (
                <button
                  key={p.id}
                  onClick={() => togglePanel(p.id)}
                  className={`rounded border px-1.5 py-0.5 font-mono text-[11px] ${
                    openPanels.has(p.id)
                      ? "border-accent/50 text-accent"
                      : "border-line text-mute hover:text-ink"
                  }`}
                >
                  {p.title}
                </button>
              ))}
            </div>
          </header>

          <div className={`min-h-0 flex-1 ${board && showSort ? "overflow-hidden" : "overflow-y-auto"}`}>
            {view.kind === "ext" ? (
              extView ? (
                <ExtensionBoundary name={extView.id}>
                  <extView.Component api={api} />
                </ExtensionBoundary>
              ) : (
                <div className="px-3 py-16 text-center text-[13px] text-mute">
                  No extension provides the view "{view.id}". Is it installed?
                </div>
              )
            ) : view.kind === "completed" ? (
              <CompletedList />
            ) : firstRun ? (
              <div className="flex h-full flex-col items-center justify-center px-6 text-center">
                <div className="font-mono text-[15px] text-accent">taskd_</div>
                <p className="mt-2 max-w-xs text-[13px] text-mute">
                  No tasks yet. Add your first one — everything else (labels, projects, the
                  calendar, extensions) grows from there.
                </p>
                <button
                  onClick={openAdd}
                  className="mt-4 rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white"
                >
                  + Add your first task
                </button>
                <p className="mt-3 font-mono text-[11px] text-faint">
                  or press <kbd className="rounded border border-line px-1">q</kbd> ·{" "}
                  <kbd className="rounded border border-line px-1">?</kbd> for shortcuts
                </p>
              </div>
            ) : board ? (
              <BoardView
                tasks={tasks}
                groupBy={groupBy}
                now={now}
                selectedId={selectedId}
                onSelect={setSelectedId}
                onOpen={openTaskById}
              />
            ) : (
              <TaskList
                tasks={tasks}
                view={view}
                now={now}
                sort={sort}
                selectedId={selectedId}
                bulkSelected={selected}
                editingId={editingId}
                onSelect={setSelectedId}
                onActivate={activateRow}
                onManualReorder={() => setSort("manual")}
                onStartEdit={setEditingId}
                onRename={(id, title) => {
                  const t = snap.tasks.get(id);
                  const clean = title.trim();
                  if (t && clean && clean !== t.title) {
                    void store.update(id, { title: clean, expectedRevision: t.revision }).catch(() => {});
                  }
                }}
                onEndEdit={() => setEditingId(null)}
              />
            )}
          </div>
        </main>

        {panels.map((p) => (
          <div key={p.id} className="hidden shrink-0 md:block" style={{ width: p.width ?? 300 }}>
            <ExtensionBoundary name={p.id}>
              <p.Component api={api} />
            </ExtensionBoundary>
          </div>
        ))}

        {/* The detail "peek" is its own column — it pushes the layout rather
            than overlapping the calendar panel. */}
        {openTask && <DetailPanel task={openTask} onClose={() => setOpenId(null)} />}
      </div>

      {selectedTasks.length > 0 && (
        <BulkBar tasks={selectedTasks} now={now} labelOptions={labelOptions} onClear={clearSelection} />
      )}

      <footer className="flex items-center justify-between border-t border-line bg-surface px-3 py-1.5 font-mono text-[11px] text-faint">
        <span className="flex items-center gap-1.5">
          <span
            className={`inline-block h-[7px] w-[7px] rounded-full ${
              snap.connected ? "bg-accent" : "bg-warn"
            }`}
            aria-hidden
          />
          {snap.connected ? "live" : "reconnecting…"}
        </span>
        <button onClick={() => setPaletteOpen(true)} className="hidden hover:text-ink sm:block">
          ⌘K commands · q add · j/k move · x done · ? help
        </button>
      </footer>

      {adding && <NewTaskOverlay now={now} initial={adding} onClose={() => setAdding(null)} />}
      {paletteOpen && <CommandPalette ctx={cmdCtx} onClose={() => setPaletteOpen(false)} />}
      {helpOpen && <ShortcutsHelp onClose={() => setHelpOpen(false)} />}
      <ToastStack />
    </div>
  );
}
