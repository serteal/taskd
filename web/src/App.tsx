import { useEffect, useMemo, useRef, useState } from "react";
import { useNow, useSnapshot, useStore, useView } from "./lib/hooks";
import { viewTitle } from "./lib/views";
import { buildAPI, registry, uiBridge, useRegistry } from "./lib/extensions";
import { Sidebar } from "./components/Sidebar";
import { TaskList, visibleTasks } from "./components/TaskList";
import { CompletedList } from "./components/CompletedList";
import { QuickAdd } from "./components/QuickAdd";
import { DetailPanel } from "./components/DetailPanel";

export default function App() {
  const store = useStore();
  const snap = useSnapshot();
  const now = useNow();
  const [view, navigate] = useView();
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [openId, setOpenId] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const quickAddRef = useRef<HTMLInputElement>(null);

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
  useRegistry(); // re-render when extensions register

  useEffect(() => {
    if (toast === null) return;
    const t = setTimeout(() => setToast(null), 4000);
    return () => clearTimeout(t);
  }, [toast]);

  const tasks =
    view.kind === "completed" || view.kind === "ext"
      ? []
      : visibleTasks(snap.tasks.values(), view, now);
  const openTask = openId !== null ? snap.tasks.get(openId) : undefined;
  const extView = view.kind === "ext" ? registry.viewById(view.id) : undefined;
  const api = useMemo(() => buildAPI(store), [store]);

  // Keyboard: list navigation stays out of the way of typing.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      const typing = el.tagName === "INPUT" || el.tagName === "TEXTAREA";
      if (e.key === "Escape" && !typing) {
        setOpenId(null);
        return;
      }
      if (typing || e.metaKey || e.ctrlKey || e.altKey) return;
      switch (e.key) {
        case "/": {
          e.preventDefault();
          quickAddRef.current?.focus();
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
          document
            .querySelector(`[data-task-row="${id}"]`)
            ?.scrollIntoView({ block: "nearest" });
          return;
        }
        case "x": {
          const t = tasks.find((t) => t.id === selectedId);
          if (t) {
            void store
              .update(t.id, { completed: true, expectedRevision: t.revision })
              .catch(() => {});
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
  }, [tasks, selectedId, openId, store]);

  return (
    <div className="flex h-full flex-col">
      <div className="flex min-h-0 flex-1">
        <Sidebar view={view} onNavigate={(v) => (setOpenId(null), navigate(v))} />

        <main className="flex min-w-0 flex-1 flex-col">
          <header className="flex items-baseline gap-2.5 px-3 pb-2 pt-4">
            <h1 className="text-[19px] font-semibold tracking-tight">
              {extView ? extView.title : viewTitle(view)}
            </h1>
            {view.kind !== "completed" && view.kind !== "ext" && (
              <span className="font-mono text-[12px] text-faint">{tasks.length}</span>
            )}
          </header>

          {view.kind !== "completed" && view.kind !== "ext" && (
            <QuickAdd ref={quickAddRef} view={view} now={now} />
          )}

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
                selectedId={selectedId}
                onSelect={setSelectedId}
                onOpen={(id) => (setSelectedId(id), setOpenId(id))}
              />
            )}
          </div>
        </main>

        {openTask && <DetailPanel task={openTask} onClose={() => setOpenId(null)} />}
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
        <span className="hidden sm:block">
          j/k move · x done · ⏎ open · esc close · / add
        </span>
      </footer>

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
