import { useEffect, useState } from "react";
import type { Task } from "../gen/task/task_pb";
import { useStore } from "../lib/hooks";
import { fmtStamp, tsDate } from "../lib/format";
import { Chip } from "./Chip";

// The archive is server-paged, not replicated: it can grow without bound
// and nothing on this screen needs to be live.
export function CompletedList() {
  const store = useStore();
  const [tasks, setTasks] = useState<Task[]>([]);
  const [next, setNext] = useState("");
  const [loading, setLoading] = useState(true);

  const load = (token: string, replace: boolean) => {
    setLoading(true);
    store
      .listCompleted(token)
      .then((r) => {
        setTasks((cur) => (replace ? r.tasks : [...cur, ...r.tasks]));
        setNext(r.next);
      })
      .finally(() => setLoading(false));
  };

  useEffect(() => load("", true), []); // eslint-disable-line react-hooks/exhaustive-deps

  const reopen = (t: Task) => {
    store
      .update(t.id, { completed: false, expectedRevision: t.revision })
      .then(() => setTasks((cur) => cur.filter((x) => x.id !== t.id)))
      .catch(() => {});
  };

  if (!loading && tasks.length === 0) {
    return (
      <div className="px-3 py-16 text-center text-[13px] text-mute">
        Nothing completed yet. It'll feel great.
      </div>
    );
  }

  return (
    <div>
      {tasks.map((t) => {
        const doneAt = tsDate(t.completedTime);
        return (
          <div
            key={t.id}
            className="group flex items-center gap-2.5 border-b border-line/70 px-3 py-[7px]"
          >
            <span className="flex h-[16px] w-[16px] shrink-0 items-center justify-center rounded-full border border-accent bg-accent text-white">
              <svg width="9" height="9" viewBox="0 0 10 10" fill="none" aria-hidden>
                <path d="M1.5 5.5 4 8l4.5-6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />
              </svg>
            </span>
            <span className="min-w-0 flex-1 truncate text-[13.5px] text-mute line-through">
              {t.title}
            </span>
            <span className="hidden items-center gap-1 sm:flex">
              {/* Origin chip: a closed synced item (e.g. a github issue) is
                  otherwise indistinguishable from a local task in the archive. */}
              {t.source !== "" && (
                <span
                  data-testid="source-chip"
                  className="inline-flex items-center rounded-full border border-line bg-surface px-1.5 py-px font-mono text-[11px] leading-4 text-faint"
                >
                  {t.source}
                </span>
              )}
              {t.labels.slice(0, 3).map((l) => (
                <Chip key={l} label={l} />
              ))}
              {t.labels.length > 3 && (
                <span className="font-mono text-[11px] text-faint">+{t.labels.length - 3}</span>
              )}
            </span>
            {doneAt && (
              <span className="shrink-0 font-mono text-[11px] text-faint" title={fmtStamp(doneAt)}>
                {fmtStamp(doneAt).slice(0, 10)}
              </span>
            )}
            <button
              onClick={() => reopen(t)}
              className="invisible shrink-0 rounded border border-line px-1.5 py-px text-[11px] text-mute hover:text-ink group-hover:visible"
            >
              Reopen
            </button>
          </div>
        );
      })}
      {next && (
        <button
          onClick={() => load(next, false)}
          disabled={loading}
          className="mx-3 my-3 rounded border border-line px-2 py-1 text-[12px] text-mute hover:text-ink"
        >
          {loading ? "Loading…" : "Load more"}
        </button>
      )}
    </div>
  );
}
