import { useEffect, useMemo, useRef, useState } from "react";
import { useNow, useSnapshot, useStore, useView } from "./lib/hooks";
import { viewTitle, type SortMode, SORT_LABELS } from "./lib/views";
import { endOfDay } from "./lib/format";
import { buildAPI, registry, uiBridge, useRegistry } from "./lib/extensions";
import { Sidebar } from "./components/Sidebar";
import { TaskList, visibleTasks } from "./components/TaskList";
import { CompletedList } from "./components/CompletedList";
import { NewTaskOverlay, type NewTaskInitial } from "./components/NewTaskOverlay";
import { DetailPanel } from "./components/DetailPanel";

const SORT_MODES: SortMode[] = ["smart", "manual", "created", "title"];

export default function App() {
  const store = useStore();
  const snap = useSnapshot();
  const now = useNow();
  const [view, navigate] = useView();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [sort, setSort] = useState<SortMode>("smart");
  const [adding, setAdding] = useState<NewTaskInitial | null>(null);
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

  useEffect(() => {
    store.onNotice = (m) => setToast(m);
    return () => {
      store.onNotice = undefined;
    };
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

  useEffect(() => {
    if (toast === null) return;
    const t = setTimeout(() => setToast(null), 4000);
    return () => clearTimeout(t);
  }, [toast]);

  const tasks =
    view.kind === "completed" || view.kind === "ext"
      ? []
      : visibleTasks(snap.tasks.values(), view, now, sort);
  const openTask = openId !== null ? snap.tasks.get(openId) : undefined;
  const extView = view.kind === "ext" ? registry.viewById(view.id) : undefined;
  const api = useMemo(() => buildAPI(store), [store]);
  const panels = registry.panels.filter((p) => openPanels.has(p.id));

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

  // Keyboard: list navigation stays out of the way of typing.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      const typing = el.tagName === "INPUT" || el.tagName === "TEXTAREA";
      if (adding) return; // the overlay owns keys while open
      if (e.key === "Escape" && !typing) {
        setOpenId(null);
        return;
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      switch (e.key) {
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
        case "j":
        case "k": {
          if (tasks.length === 0) return;
          const idx = tasks.findIndex((t) => t.id === selectedId);
          const next =
            idx === -1
              ? 0
              : Math.min(Math.max(idx + (e.key === "j" ? 1 : -1), 0), tasks.length - 1);
          const id = tasks[next].id;
          setSelectedId(id);
          if (openId !== null) setOpenId(id);
          document.querySelector(`[data-task-row="${id}"]`)?.scrollIntoView({ block: "nearest" });
          return;
        }
        case "x": {
          const t = tasks.find((t) => t.id === selectedId);
          if (t) void store.update(t.id, { completed: true, expectedRevision: t.revision }).catch(() => {});
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
  }, [tasks, selectedId, openId, store, adding, view, now]);

  const showSort = view.kind !== "completed" && view.kind !== "ext";

  return (
    <div className="flex h-full flex-col">
      <div className="relative flex min-h-0 flex-1">
        <Sidebar
          view={view}
          onNavigate={(v) => (setOpenId(null), navigate(v))}
          onAddTask={openAdd}
        />

        <main className="flex min-w-0 flex-1 flex-col">
          <header className="flex items-center gap-2.5 px-3 pb-2 pt-4">
            <h1 className="text-[19px] font-semibold tracking-tight">
              {extView ? extView.title : viewTitle(view)}
            </h1>
            {showSort && <span className="font-mono text-[12px] text-faint">{tasks.length}</span>}
            <div className="ml-auto flex items-center gap-2">
              {showSort && (
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

          <div className="min-h-0 flex-1 overflow-y-auto">
            {view.kind === "ext" ? (
              extView ? (
                <extView.Component api={api} />
              ) : (
                <div className="px-3 py-16 text-center text-[13px] text-mute">
                  No extension provides the view "{view.id}". Is it installed?
                </div>
              )
            ) : view.kind === "completed" ? (
              <CompletedList />
            ) : (
              <TaskList
                tasks={tasks}
                view={view}
                now={now}
                sort={sort}
                selectedId={selectedId}
                onSelect={setSelectedId}
                onOpen={(id) => (setSelectedId(id), setOpenId(id))}
                onManualReorder={() => setSort("manual")}
              />
            )}
          </div>
        </main>

        {panels.map((p) => (
          <div key={p.id} className="hidden shrink-0 md:block" style={{ width: p.width ?? 300 }}>
            <p.Component api={api} />
          </div>
        ))}

        {openTask && (
          <div className="absolute inset-y-0 right-0 z-20 shadow-2xl">
            <DetailPanel task={openTask} onClose={() => setOpenId(null)} />
          </div>
        )}
      </div>

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
        <span className="hidden sm:block">q add · j/k move · x done · ⏎ open · t timeline</span>
      </footer>

      {adding && (
        <NewTaskOverlay now={now} initial={adding} onClose={() => setAdding(null)} />
      )}

      {toast && (
        <div
          role="status"
          className="fixed bottom-10 left-1/2 -translate-x-1/2 rounded border border-line bg-surface px-3 py-1.5 text-[12.5px] shadow-sm"
        >
          {toast}
        </div>
      )}
    </div>
  );
}
