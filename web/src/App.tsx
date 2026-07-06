import { useEffect, useMemo, useRef, useState } from "react";
import { useDueReminders, useNow, useSnapshot, useStore, useTheme, useView } from "./lib/hooks";
import {
  viewTitle,
  viewToSearch,
  readViewPrefs,
  writeViewPrefs,
  subscribeViewPrefs,
  countView,
  isPromotable,
  promotedSections,
  type SortMode,
  type BoardGroupBy,
} from "./lib/views";
import { endOfDay } from "./lib/format";
import {
  defaultSidebarCollapsed,
  writeSidebarCollapsed,
  readOnboardingDismissed,
  writeOnboardingDismissed,
  readSidebarWidth,
  writeSidebarWidth,
  readDetailWidth,
  writeDetailWidth,
  readPanelWidth,
  writePanelWidth,
  SIDEBAR_WIDTH,
  DETAIL_WIDTH,
  PANEL_WIDTH,
} from "./lib/layout";
import { useExtensions, isSourcePaused, refreshExtensions, refreshDaemonVersion } from "./lib/admin";
import { notify, readDueRemindersEnabled, writeDueRemindersEnabled } from "./lib/notify";
import { completeTask } from "./lib/actions";
import { undoLast } from "./lib/undo";
import { buildAPI, registry, uiBridge, useRegistry } from "./lib/extensions";
import type { CommandContext } from "./lib/commands";
import { savedViews, type SavedView } from "./lib/savedviews";
import { useSavedFilters, type SavedFilter } from "./lib/filters";
import { flattenSections, nestSections } from "./lib/nest";
import { Sidebar } from "./components/Sidebar";
import { FilterBuilder } from "./components/FilterBuilder";
import { TaskList, visibleTasks } from "./components/TaskList";
import { CompletedList } from "./components/CompletedList";
import { NewTaskOverlay, type NewTaskInitial } from "./components/NewTaskOverlay";
import { DetailPanel } from "./components/DetailPanel";
import { ResizeHandle, useResizable } from "./components/ResizeHandle";
import type { Panel } from "./lib/extensions";
import { BoardView } from "./components/BoardView";
import { ToastStack } from "./components/ToastStack";
import { CommandPalette } from "./components/CommandPalette";
import { ExtensionBoundary } from "./components/ExtensionBoundary";
import { ShortcutsHelp } from "./components/ShortcutsHelp";
import { Settings } from "./components/Settings";
import { BulkBar } from "./components/BulkBar";
import { ContextMenu, TaskContextMenu } from "./components/ContextMenu";
import { ViewMenu } from "./components/ViewMenu";
import { Icon } from "./components/icons";
import { completeMany } from "./lib/actions";

export default function App() {
  const store = useStore();
  const snap = useSnapshot();
  const now = useNow();
  const [, toggleTheme] = useTheme();
  const [view, navigate] = useView();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  // Presentation state is per-view and sticky: seed from this view's saved
  // prefs, then hydrate/persist as the view or the controls change (below).
  const [sort, setSort] = useState<SortMode>(() => readViewPrefs(view).sort);
  const [board, setBoard] = useState(() => readViewPrefs(view).board);
  const [groupBy, setGroupBy] = useState<BoardGroupBy>(() => readViewPrefs(view).groupBy);
  const [adding, setAdding] = useState<NewTaskInitial | null>(null);
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [helpOpen, setHelpOpen] = useState(false);
  // The "Save this view as…" naming modal (replaces a native window.prompt).
  const [saveViewOpen, setSaveViewOpen] = useState(false);
  const [sidebarCollapsed, setSidebarCollapsed] = useState(() =>
    defaultSidebarCollapsed(typeof window === "undefined" ? 1280 : window.innerWidth),
  );
  // Drag-resizable widths for the left sidebar and the detail panel; one shared
  // hook (useResizable) owns each one's persistence + viewport re-clamp. The
  // extension panel wrappers get theirs per-id inside ResizablePanel below.
  const sidebarResize = useResizable({
    read: readSidebarWidth,
    write: writeSidebarWidth,
    bounds: SIDEBAR_WIDTH,
  });
  const detailResize = useResizable({
    read: readDetailWidth,
    write: writeDetailWidth,
    bounds: DETAIL_WIDTH,
  });
  const [notifyDue, setNotifyDue] = useState(readDueRemindersEnabled);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [onboardingDismissed, setOnboardingDismissed] = useState(readOnboardingDismissed);
  // Shared extension state (drives source-view pause banners; also feeds the
  // sidebar's paused pills through the same store).
  const { exts } = useExtensions();
  // The filter builder overlay. null = closed; `{}` = new; `{ editing }` = edit.
  const [filterBuilder, setFilterBuilder] = useState<{ editing?: SavedFilter } | null>(null);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [lastClicked, setLastClicked] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  // Right-click context menu: which task, anchored at the cursor.
  const [ctxMenu, setCtxMenu] = useState<{ id: string; x: number; y: number } | null>(null);
  // Parents whose nested subtasks are hidden — session state only, on purpose.
  const [collapsedParents, setCollapsedParents] = useState<Set<string>>(new Set());
  const regVersion = useRegistry();

  const toggleCollapsed = (id: string) => {
    // Collapsing while the selection (or the open detail) sits on one of the
    // children being hidden would strand the keyboard — j/k would jump to the
    // first row and x would no-op. Follow to the parent instead.
    if (!collapsedParents.has(id)) {
      const strands = (tid: string | null) =>
        tid !== null && snap.tasks.get(tid)?.parentId === id;
      if (strands(selectedId)) setSelectedId(id);
      if (strands(openId)) setOpenId(id);
    }
    setCollapsedParents((cur) => {
      const next = new Set(cur);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });
  };

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

  // A RE-connect may mean the daemon restarted (a new build, changed extension
  // state) — refresh the admin stores on the disconnected→connected
  // transition. The initial connect is excluded on purpose: both stores
  // already fetch once per session on their first subscriber.
  const conn = useRef({ prev: false, everTrue: false });
  useEffect(() => {
    const c = conn.current;
    if (snap.connected && !c.prev && c.everTrue) {
      void refreshExtensions();
      void refreshDaemonVersion();
    }
    if (snap.connected) c.everTrue = true;
    c.prev = snap.connected;
  }, [snap.connected]);

  // Cold loads start disconnected for a few hundred ms; only after this mount
  // grace does an empty, never-connected replica earn the can't-reach state.
  const [graceElapsed, setGraceElapsed] = useState(false);
  useEffect(() => {
    const t = setTimeout(() => setGraceElapsed(true), 1500);
    return () => clearTimeout(t);
  }, []);

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

  const filters = useSavedFilters();
  // The base (local) working set, then the promoted filter sections for a
  // built-in list. The combined `tasks` array is what keyboard j/k, bulk-select
  // and board mode consume — so promoted tasks are reachable and grouped there
  // too; TaskList in list mode gets the base + sections separately so it can
  // render each promoted section under its own header.
  const baseTasks =
    view.kind === "completed" || view.kind === "ext"
      ? []
      : visibleTasks(snap.tasks.values(), view, now, sort);
  const promotedSecs = isPromotable(view)
    ? promotedSections([...snap.tasks.values()], view.kind, now, filters)
    : [];
  // Subtask nesting (lib/nest): the grouped list nests a child under its
  // in-view parent; manual sort and board mode stay flat. The SAME flattened
  // order feeds `tasks`, so j/k traversal matches the rendered order exactly —
  // and a collapsed parent's children leave the keyboard order too.
  const nestSecs = nestSections(baseTasks, {
    grouped: !board && sort !== "manual",
    now,
    collapsed: collapsedParents,
    titleOf: (id) => snap.tasks.get(id)?.title,
  });
  const tasks = [...flattenSections(nestSecs), ...promotedSecs.flatMap((s) => s.tasks)];
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
  useEffect(clearSelection, [
    view.kind,
    (view as { label?: string }).label,
    (view as { source?: string }).source,
    (view as { id?: string }).id,
  ]);

  // Sticky per-view presentation. `viewKey` uniquely identifies the current
  // view (same string as saved in localStorage). On entering a view, hydrate
  // its saved layout; whenever the controls change, persist them back.
  const viewKey = viewToSearch(view);
  useEffect(() => {
    const p = readViewPrefs(view);
    setSort(p.sort);
    setBoard(p.board);
    setGroupBy(p.groupBy);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewKey]);
  useEffect(() => {
    writeViewPrefs(view, { sort, board, groupBy });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewKey, sort, board, groupBy]);
  // Another tab/window rewrote the prefs map (cross-tab `storage` event):
  // re-hydrate this view's copy so both documents converge.
  useEffect(
    () =>
      subscribeViewPrefs(() => {
        const p = readViewPrefs(view);
        setSort(p.sort);
        setBoard(p.board);
        setGroupBy(p.groupBy);
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [viewKey],
  );
  useEffect(() => writeSidebarCollapsed(sidebarCollapsed), [sidebarCollapsed]);
  useEffect(() => writeDueRemindersEnabled(notifyDue), [notifyDue]);
  useDueReminders(notifyDue);

  const applySaved = (v: SavedView) => {
    setOpenId(null);
    // Write the target view's sticky prefs first, so the post-navigate
    // hydration reads these applied values rather than clobbering them.
    writeViewPrefs(v.view, { sort: v.sort, board: v.board, groupBy: v.groupBy });
    navigate(v.view);
    setSort(v.sort);
    setBoard(v.board);
    setGroupBy(v.groupBy);
  };
  const saveCurrentView = () => setSaveViewOpen(true);
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
      openSettings: () => setSettingsOpen(true),
      saveCurrentView,
      applySaved,
      extCommands: registry.commands,
      now,
    }),
    // regVersion covers extension command/panel registration; view/now/sel change often.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [snap, store, selectedId, view, now, regVersion, sort, board, groupBy],
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
      if (settingsOpen) {
        if (e.key === "Escape") setSettingsOpen(false);
        return;
      }
      if (filterBuilder) {
        // The builder owns the keyboard; its own card handles Escape, but catch
        // it here too so a blurred overlay still closes.
        if (e.key === "Escape") setFilterBuilder(null);
        return;
      }
      if (helpOpen) {
        if (e.key === "Escape") setHelpOpen(false);
        return;
      }
      if (saveViewOpen) {
        // The naming modal owns the keyboard; its card handles Enter/Escape,
        // but catch Escape here too so a blurred overlay still closes.
        if (e.key === "Escape") setSaveViewOpen(false);
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
      // ⌘Z / Ctrl+Z: global undo. Never while editing text — native undo must
      // keep working inside inputs/textareas/contenteditable — and never with
      // Shift (that's redo). Modal overlays already returned above, so this only
      // runs on the bare app surface (detail panel may be open, focus outside it).
      if ((e.metaKey || e.ctrlKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "z") {
        if (typing || el.isContentEditable) return;
        e.preventDefault();
        const undone = undoLast();
        notify.toast({ kind: "info", message: undone ? `Undid ${undone.label}` : "Nothing to undo" });
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
  }, [tasks, selectedId, openId, store, adding, paletteOpen, helpOpen, saveViewOpen, settingsOpen, filterBuilder, view, now, selected, selectedTasks]);

  const showSort = view.kind !== "completed" && view.kind !== "ext";
  // The header count comes from the same single source of truth as the
  // sidebar badge (countView over the full snapshot), so collapse and other
  // presentation state can never make the two disagree.
  const viewCount = countView([...snap.tasks.values()], view, now, filters);
  const emptyReplica = snap.tasks.size === 0;
  // An empty replica splits four ways: a startup blank (never connected, mount
  // grace still running — a cold load's first few hundred ms must not flash
  // "can't reach the daemon"), the explicit can't-reach state (a real drop, or
  // never connected once the grace lapses), the first-run onboarding card
  // (connected, never dismissed), or the plain empty state (connected,
  // onboarding dismissed). First-run keys on LOCAL tasks, not the whole
  // replica: installing extensions up front (the install.sh path) fills the
  // replica with synced items before the user has added a single task of
  // their own — exactly who the onboarding is for.
  const startupBlank = !snap.connected && emptyReplica && !snap.everConnected && !graceElapsed;
  const disconnectedEmpty = !snap.connected && emptyReplica && !startupBlank;
  // First-run surfaces claim only the built-in LOCAL lists: an explicitly
  // opened source/filter/label view must show its own content (a source view
  // full of synced items is exactly where onboarding must not sit on top).
  const firstRunSurface =
    view.kind === "inbox" || view.kind === "today" || view.kind === "upcoming" || view.kind === "all";
  const firstRun =
    firstRunSurface && snap.connected && ![...snap.tasks.values()].some((t) => t.source === "");
  // Source-view pause banner: this source's owning extension is disabled.
  const sourcePaused = view.kind === "source" && isSourcePaused(exts, view.source);
  // For a label view: how many synced tasks also carry the label. Drives the
  // header's escape-hatch chip ("+N synced" / "hide synced"); 0 → no chip.
  const labelSyncedCount =
    view.kind === "label"
      ? [...snap.tasks.values()].filter((t) => t.source !== "" && t.labels.includes(view.label)).length
      : 0;

  return (
    <div className="flex h-full flex-col">
      <div className="flex min-h-0 flex-1">
        <Sidebar
          view={view}
          onNavigate={(v) => (setOpenId(null), navigate(v))}
          onAddTask={openAdd}
          onApplySaved={applySaved}
          onNewFilter={() => setFilterBuilder({})}
          onEditFilter={(f) => setFilterBuilder({ editing: f })}
          onOpenSettings={() => setSettingsOpen(true)}
          collapsed={sidebarCollapsed}
          onToggleCollapsed={() => setSidebarCollapsed((c) => !c)}
          width={sidebarResize.width}
          resizeHandle={
            <ResizeHandle
              edge="right"
              label="Resize sidebar"
              width={sidebarResize.width}
              bounds={sidebarResize.bounds}
              onResize={sidebarResize.onResize}
              onCommit={sidebarResize.onCommit}
              onReset={sidebarResize.onReset}
            />
          }
        />

        <main className="flex min-w-0 flex-1 flex-col">
          <header
            className="flex items-center gap-2.5 px-3 pb-2 pt-4"
            data-view-sort={sort}
            data-view-board={board}
            data-view-group={groupBy}
          >
            <h1 className="text-[19px] font-semibold tracking-tight">
              {extView ? extView.title : viewTitle(view)}
            </h1>
            {showSort && (
              <span data-testid="view-count" className="font-mono text-[12px] text-faint">
                {viewCount}
              </span>
            )}
            {view.kind === "label" && labelSyncedCount > 0 && (
              <button
                onClick={() =>
                  (setOpenId(null),
                  navigate({ kind: "label", label: view.label, includeSynced: !view.includeSynced }))
                }
                data-testid="label-synced-toggle"
                className="rounded-full border border-line px-2 py-px font-mono text-[11px] text-mute hover:border-mute hover:text-ink"
              >
                {view.includeSynced ? "hide synced" : `+${labelSyncedCount} synced`}
              </button>
            )}
            <div className="ml-auto flex items-center gap-2">
              {showSort && (
                <ViewMenu
                  board={board}
                  onBoardChange={setBoard}
                  sort={sort}
                  onSortChange={setSort}
                  groupBy={groupBy}
                  onGroupByChange={setGroupBy}
                />
              )}
              {registry.panels.map((p) => (
                <button
                  key={p.id}
                  onClick={() => togglePanel(p.id)}
                  aria-label={`Toggle ${p.title.toLowerCase()} panel`}
                  title={p.title}
                  className={`rounded-md border p-1.5 ${
                    openPanels.has(p.id)
                      ? "border-accent/50 text-accent"
                      : "border-line text-mute hover:border-mute hover:text-ink"
                  }`}
                >
                  <Icon name="panel" size={14} />
                </button>
              ))}
            </div>
          </header>

          {sourcePaused && (
            <div
              data-testid="source-paused-banner"
              className="mx-3 mb-2 flex items-center gap-2 rounded-md border border-warn/40 bg-warn/[.06] px-3 py-1.5 text-[12.5px] text-mute"
            >
              <span className="min-w-0 flex-1">
                This source is paused — items are no longer syncing.
              </span>
              <button
                onClick={() => setSettingsOpen(true)}
                className="shrink-0 font-medium text-accent hover:underline"
              >
                Re-enable it in Settings
              </button>
            </div>
          )}

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
            ) : startupBlank ? (
              // Still inside the cold-load grace: render the plain empty area,
              // not a premature "can't reach the daemon".
              <div className="h-full" aria-hidden />
            ) : disconnectedEmpty ? (
              <DisconnectedState />
            ) : firstRun && !onboardingDismissed ? (
              <OnboardingCard
                onAdd={openAdd}
                onOpenSettings={() => setSettingsOpen(true)}
                onOpenShortcuts={() => setHelpOpen(true)}
                onDismiss={() => {
                  writeOnboardingDismissed(true);
                  setOnboardingDismissed(true);
                }}
              />
            ) : firstRun ? (
              <EmptyState onAdd={openAdd} />
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
                tasks={baseTasks}
                sections={nestSecs}
                view={view}
                now={now}
                sort={sort}
                selectedId={selectedId}
                bulkSelected={selected}
                editingId={editingId}
                extraSections={promotedSecs}
                collapsed={collapsedParents}
                onToggleCollapse={toggleCollapsed}
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
                onContextMenu={(id, x, y) => {
                  setSelectedId(id);
                  setCtxMenu({ id, x, y });
                }}
              />
            )}
          </div>
        </main>

        {/* Right-hand rails: an open task's detail sits adjacent to the main
            list; extension panels stay outermost right. On a wide viewport
            (≥1440px) BOTH show at once; below that the detail wins the slot and
            the panels hide (detail-exclusive, the historical behaviour).
            openPanels is untouched the whole time — the panels stay mounted (a
            CSS hide, not an unmount) so they reappear exactly as they were when
            detail closes or the viewport widens. */}
        {openTask && (
          <DetailPanel
            task={openTask}
            onClose={() => setOpenId(null)}
            onOpenTask={openTaskById}
            width={detailResize.width}
            resizeHandle={
              <ResizeHandle
                edge="left"
                label="Resize details panel"
                width={detailResize.width}
                bounds={detailResize.bounds}
                onResize={detailResize.onResize}
                onCommit={detailResize.onCommit}
                onReset={detailResize.onReset}
              />
            }
          />
        )}
        {panels.map((p) => (
          <ResizablePanel
            key={p.id}
            panel={p}
            api={api}
            visibilityClass={openTask ? "hidden min-[1440px]:block" : "hidden md:block"}
          />
        ))}
      </div>

      {selectedTasks.length > 0 && (
        <BulkBar tasks={selectedTasks} now={now} labelOptions={labelOptions} onClear={clearSelection} />
      )}

      <footer className="flex items-center justify-between border-t border-line bg-surface px-3 py-1.5 font-mono text-[11px] text-faint">
        <span
          className="flex items-center gap-1.5"
          data-testid="conn-status"
          data-connected={snap.connected}
        >
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

      {ctxMenu &&
        (() => {
          const t = snap.tasks.get(ctxMenu.id);
          if (!t) return null;
          return (
            <ContextMenu x={ctxMenu.x} y={ctxMenu.y} onClose={() => setCtxMenu(null)}>
              {(close) => (
                <TaskContextMenu
                  task={t}
                  now={now}
                  store={store}
                  labelOptions={labelOptions}
                  onOpen={() => {
                    openTaskById(t.id);
                    close();
                  }}
                  onEdit={() => {
                    setEditingId(t.id);
                    close();
                  }}
                  close={close}
                />
              )}
            </ContextMenu>
          );
        })()}

      {filterBuilder && (
        <FilterBuilder
          initial={filterBuilder.editing}
          onClose={() => setFilterBuilder(null)}
          onSaved={(f) => {
            setFilterBuilder(null);
            setOpenId(null);
            navigate({ kind: "filter", id: f.id });
          }}
        />
      )}
      {saveViewOpen && (
        <SaveViewDialog
          onClose={() => setSaveViewOpen(false)}
          onSave={(name) => {
            savedViews.add({ name, view, sort, board, groupBy });
            setSaveViewOpen(false);
          }}
        />
      )}
      {adding && <NewTaskOverlay now={now} initial={adding} onClose={() => setAdding(null)} />}
      {paletteOpen && <CommandPalette ctx={cmdCtx} onClose={() => setPaletteOpen(false)} />}
      {helpOpen && <ShortcutsHelp onClose={() => setHelpOpen(false)} />}
      {settingsOpen && (
        <Settings
          onClose={() => setSettingsOpen(false)}
          onOpenShortcuts={() => {
            setSettingsOpen(false);
            setHelpOpen(true);
          }}
          notifyDue={notifyDue}
          onToggleNotifyDue={() => setNotifyDue((v) => !v)}
        />
      )}
      <ToastStack />
    </div>
  );
}

// One extension panel wrapper, drag-resizable at its left edge. The host owns
// the width (the registered `width ?? 300` is only the default) and persists it
// per panel id — no extension-API change. `visibilityClass` carries the
// responsive show/hide rules unchanged (rail hidden below md; detail-exclusive
// below 1440px); the panel stays mounted, so a resize survives a hide/show.
function ResizablePanel({
  panel,
  api,
  visibilityClass,
}: {
  panel: Panel;
  api: unknown;
  visibilityClass: string;
}) {
  const defaultWidth = panel.width ?? PANEL_WIDTH.default;
  const resize = useResizable({
    read: () => readPanelWidth(panel.id, defaultWidth),
    write: (w) => writePanelWidth(panel.id, w),
    bounds: { ...PANEL_WIDTH, default: defaultWidth },
  });
  return (
    <div
      data-testid="panel-wrapper"
      data-panel-id={panel.id}
      className={`relative shrink-0 ${visibilityClass}`}
      style={{ width: resize.width }}
    >
      <ResizeHandle
        edge="left"
        label={`Resize ${panel.title} panel`}
        width={resize.width}
        bounds={resize.bounds}
        onResize={resize.onResize}
        onCommit={resize.onCommit}
        onReset={resize.onReset}
      />
      <ExtensionBoundary name={panel.id}>
        <panel.Component api={api} />
      </ExtensionBoundary>
    </div>
  );
}

// The "Save this view as…" naming modal. A small centered dialog matching
// FilterBuilder's overlay/dialog styling: autofocused name input, Enter saves,
// Escape cancels. App gates the global keyboard handler while it is open.
function SaveViewDialog({
  onSave,
  onClose,
}: {
  onSave: (name: string) => void;
  onClose: () => void;
}) {
  const [name, setName] = useState("");
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => inputRef.current?.focus(), []);
  const canSave = name.trim() !== "";
  const save = () => {
    if (canSave) onSave(name.trim());
  };
  return (
    <div
      className="fixed inset-0 z-40 flex items-start justify-center bg-black/30 px-4 pt-[12vh]"
      onMouseDown={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Save view"
        data-testid="save-view-dialog"
        className="w-[420px] max-w-full overflow-hidden rounded-xl border border-line bg-surface shadow-2xl"
        onMouseDown={(e) => e.stopPropagation()}
        onKeyDown={(e) => {
          if (e.key === "Escape") onClose();
          if (e.key === "Enter" && canSave) save();
        }}
      >
        <div className="flex items-baseline justify-between border-b border-line px-4 py-3">
          <h2 className="text-[15px] font-semibold">Save view</h2>
          <button onClick={onClose} className="font-mono text-[12px] text-mute hover:text-ink">
            esc
          </button>
        </div>
        <div className="p-4">
          <input
            ref={inputRef}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="View name (e.g. Focus)"
            aria-label="View name"
            className="w-full bg-transparent text-[17px] font-semibold leading-tight placeholder:text-faint focus:outline-none"
          />
        </div>
        <div className="flex items-center justify-end gap-2 border-t border-line px-4 py-2.5">
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
            Save
          </button>
        </div>
      </div>
    </div>
  );
}

// The plain empty state — the fallback once onboarding is dismissed, and the
// invitation to add a first task. Kept minimal; the onboarding card is the
// richer first-run surface built on top of it.
function EmptyState({ onAdd }: { onAdd: () => void }) {
  return (
    <div
      data-testid="empty-state"
      className="flex h-full flex-col items-center justify-center px-6 text-center"
    >
      <div className="font-mono text-[15px] text-accent">taskd_</div>
      <p className="mt-2 max-w-xs text-[13px] text-mute">
        No tasks yet. Add your first one — everything else (labels, projects, the calendar,
        extensions) grows from there.
      </p>
      <button
        onClick={onAdd}
        className="mt-4 rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white"
      >
        + Add your first task
      </button>
      <p className="mt-3 font-mono text-[11px] text-faint">
        or press <kbd className="rounded border border-line px-1">q</kbd> ·{" "}
        <kbd className="rounded border border-line px-1">?</kbd> for shortcuts
      </p>
    </div>
  );
}

// First-run onboarding: a dismissible three-step card. Dismissal persists
// (localStorage) and falls back to the plain EmptyState. Install copy stays
// browser-generic (Chrome "Install", Safari/others "Add to Dock").
function OnboardingCard({
  onAdd,
  onOpenSettings,
  onOpenShortcuts,
  onDismiss,
}: {
  onAdd: () => void;
  onOpenSettings: () => void;
  onOpenShortcuts: () => void;
  onDismiss: () => void;
}) {
  const steps: { n: number; title: string; body: React.ReactNode }[] = [
    {
      n: 1,
      title: "Add your first task",
      body: (
        <>
          Press <kbd className="rounded border border-line px-1 font-mono">q</kbd> — or use the
          button below.
        </>
      ),
    },
    {
      n: 2,
      title: "Install it as an app",
      body: <>Open your browser's menu → Install / Add to Dock for a standalone window.</>,
    },
    {
      n: 3,
      title: "Connect your sources",
      body: (
        <>
          Pull in calendars and issues from{" "}
          <button onClick={onOpenSettings} className="font-medium text-accent hover:underline">
            Settings → Extensions
          </button>
          .
        </>
      ),
    },
  ];
  return (
    <div className="flex h-full items-center justify-center px-6">
      <div
        data-testid="onboarding"
        className="relative w-full max-w-md rounded-xl border border-line bg-surface p-5 shadow-sm"
      >
        <button
          onClick={onDismiss}
          aria-label="Dismiss onboarding"
          data-testid="onboarding-dismiss"
          className="absolute right-2.5 top-2 rounded px-1.5 font-mono text-[15px] leading-none text-mute hover:text-ink"
        >
          ×
        </button>

        <div className="font-mono text-[15px] text-accent">taskd_</div>
        <h2 className="mt-1 text-[15px] font-semibold tracking-tight text-ink">
          Welcome — let's get you set up
        </h2>

        <ol className="mt-4 flex flex-col gap-3">
          {steps.map((s) => (
            <li key={s.n} className="flex gap-3">
              <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent/12 font-mono text-[11px] font-medium text-accent">
                {s.n}
              </span>
              <div className="min-w-0">
                <div className="text-[13px] font-medium text-ink">{s.title}</div>
                <div className="text-[12.5px] leading-5 text-mute">{s.body}</div>
              </div>
            </li>
          ))}
        </ol>

        <button
          onClick={onAdd}
          className="mt-5 rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white"
        >
          + Add your first task
        </button>

        <p className="mt-3 text-[11px] text-faint">
          Press{" "}
          <button onClick={onOpenShortcuts} className="font-mono text-accent hover:underline">
            ?
          </button>{" "}
          anytime for keyboard shortcuts.
        </p>
      </div>
    </div>
  );
}

// Explicit "the daemon is unreachable" state, shown when the replica is empty
// AND disconnected — instead of a silently blank list. The store reconnects on
// its own, hence the muted retry note.
function DisconnectedState() {
  const origin = typeof window !== "undefined" ? window.location.origin : "";
  return (
    <div
      data-testid="disconnected-empty"
      className="flex h-full flex-col items-center justify-center px-6 text-center"
    >
      <span className="inline-block h-2 w-2 rounded-full bg-warn" aria-hidden />
      <h2 className="mt-3 text-[14px] font-semibold text-ink">Can't reach the daemon</h2>
      <p className="mt-1.5 font-mono text-[12px] text-mute">{origin}</p>
      <p className="mt-2 max-w-xs text-[13px] text-mute">Is taskd running?</p>
      <p className="mt-3 font-mono text-[11px] text-faint">retrying…</p>
    </div>
  );
}
