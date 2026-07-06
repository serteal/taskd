import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { createClient, createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import {
  TaskService,
  TaskSchema,
  WatchTasksResponseSchema,
  type Task,
  type WatchTasksResponse,
} from "../gen/task/task_pb";
import { TaskStore } from "./store";

// An async queue we push watch events into from the test body.
class EventQueue {
  private buf: WatchTasksResponse[] = [];
  private wakeups: Array<(v: WatchTasksResponse | null) => void> = [];
  push(ev: WatchTasksResponse) {
    const w = this.wakeups.shift();
    if (w) w(ev);
    else this.buf.push(ev);
  }
  close() {
    for (const w of this.wakeups.splice(0)) w(null);
  }
  async next(): Promise<WatchTasksResponse | null> {
    const b = this.buf.shift();
    if (b) return b;
    return new Promise((resolve) => this.wakeups.push(resolve));
  }
}

function mkTask(over: Partial<Task> & { id: string }): Task {
  return Object.assign(create(TaskSchema, { title: "t", revision: 1n }), over);
}

function taskEvent(t: Task): WatchTasksResponse {
  return create(WatchTasksResponseSchema, { change: { case: "task", value: t } });
}

function deleteEvent(id: string): WatchTasksResponse {
  return create(WatchTasksResponseSchema, { change: { case: "deletedTaskId", value: id } });
}

const handshake = () => create(WatchTasksResponseSchema, {});

interface Fixture {
  store: TaskStore;
  queue: EventQueue;
  stop: () => void;
  started: Promise<void>;
  calls: string[];
  serverTasks: Map<string, Task>;
}

// A fake TaskService: listTasks serves `serverTasks`, watchTasks yields the
// handshake then whatever the test pushes. Mutation RPCs mutate serverTasks
// the way the real server would (revision bump etc.).
function fixture(initial: Task[] = []): Fixture {
  const queue = new EventQueue();
  const calls: string[] = [];
  const serverTasks = new Map(initial.map((t) => [t.id, t]));

  const transport = createRouterTransport(({ service }) => {
    service(TaskService, {
      listTasks(req) {
        calls.push("listTasks");
        const all = [...serverTasks.values()].filter((t) =>
          req.filter?.completed === false ? !t.completedTime : true,
        );
        return { tasks: all, nextPageToken: "" };
      },
      getTask(req) {
        calls.push("getTask");
        const t = serverTasks.get(req.id);
        if (!t) throw new ConnectError("not found", Code.NotFound);
        return { task: t };
      },
      createTask(req) {
        calls.push("createTask");
        const t = mkTask({
          id: `srv-${serverTasks.size + 1}`,
          title: req.title,
          labels: req.labels,
          dueTime: req.dueTime,
          recurrence: req.recurrence,
          parentId: req.parentId,
        });
        serverTasks.set(t.id, t);
        return { task: t };
      },
      updateTask(req) {
        calls.push("updateTask");
        const cur = serverTasks.get(req.id);
        if (!cur) throw new ConnectError("not found", Code.NotFound);
        if (req.expectedRevision !== 0n && req.expectedRevision !== cur.revision) {
          throw new ConnectError("revision mismatch", Code.Aborted);
        }
        const paths = req.updateMask?.paths ?? [];
        // Recurring roll-forward, the way the real server behaves: completing
        // a recurring task archives a frozen copy and advances the live task's
        // due instead of completing it.
        if (paths.includes("completed_time") && req.task?.completedTime && cur.recurrence !== "") {
          const spawned = mkTask({
            id: `${req.id}-arch`,
            title: cur.title,
            completedTime: req.task.completedTime,
          });
          const next = {
            ...cur,
            revision: cur.revision + 1n,
            dueTime: timestampFromDate(new Date(Date.now() + 86_400_000)),
          };
          serverTasks.set(req.id, next);
          serverTasks.set(spawned.id, spawned);
          return { task: next, spawnedOccurrence: spawned };
        }
        const next = { ...cur, revision: cur.revision + 1n };
        for (const p of paths) {
          if (p === "title") next.title = req.task?.title ?? "";
          if (p === "labels") next.labels = req.task?.labels ?? [];
          if (p === "completed_time") next.completedTime = req.task?.completedTime;
          if (p === "due_time") next.dueTime = req.task?.dueTime;
          if (p === "notes") next.notes = req.task?.notes ?? "";
          if (p === "recurrence") next.recurrence = req.task?.recurrence ?? "";
          if (p === "parent_id") next.parentId = req.task?.parentId ?? "";
        }
        serverTasks.set(req.id, next);
        return { task: next };
      },
      deleteTask(req) {
        calls.push("deleteTask");
        serverTasks.delete(req.id);
        return {};
      },
      async *watchTasks() {
        calls.push("watchTasks");
        yield handshake();
        for (;;) {
          const ev = await queue.next();
          if (ev === null) return;
          yield ev;
        }
      },
    });
  });

  const store = new TaskStore(createClient(TaskService, transport), {
    minBackoffMs: 1,
    maxBackoffMs: 1,
  });
  const ctl = new AbortController();
  const runner = store.start(ctl.signal);

  const started = waitFor(store, (s) => s.connected);
  return {
    store,
    queue,
    calls,
    serverTasks,
    started,
    stop: () => {
      ctl.abort();
      queue.close();
      void runner;
    },
  };
}

function waitFor(store: TaskStore, pred: (s: ReturnType<TaskStore["getSnapshot"]>) => boolean): Promise<void> {
  return new Promise((resolve, reject) => {
    const t = setTimeout(() => reject(new Error("waitFor timed out")), 3000);
    const check = () => {
      if (pred(store.getSnapshot())) {
        clearTimeout(t);
        unsub();
        resolve();
      }
    };
    const unsub = store.subscribe(check);
    check();
  });
}

describe("TaskStore", () => {
  it("loads the snapshot after the handshake and applies live events", async () => {
    const f = fixture([mkTask({ id: "a", title: "first" })]);
    try {
      await f.started;
      expect(f.store.getSnapshot().tasks.get("a")?.title).toBe("first");

      f.queue.push(taskEvent(mkTask({ id: "b", title: "second", revision: 1n })));
      await waitFor(f.store, (s) => s.tasks.has("b"));
      expect(f.store.getSnapshot().tasks.size).toBe(2);
    } finally {
      f.stop();
    }
  });

  it("ignores events older than the replica (revision guard)", async () => {
    const f = fixture([mkTask({ id: "a", title: "v3", revision: 3n })]);
    try {
      await f.started;
      f.queue.push(taskEvent(mkTask({ id: "a", title: "stale", revision: 2n })));
      f.queue.push(taskEvent(mkTask({ id: "z", title: "marker", revision: 1n })));
      await waitFor(f.store, (s) => s.tasks.has("z")); // ordering fence
      expect(f.store.getSnapshot().tasks.get("a")?.title).toBe("v3");
    } finally {
      f.stop();
    }
  });

  it("removes tasks on completion and deletion events", async () => {
    const f = fixture([mkTask({ id: "a" }), mkTask({ id: "b" })]);
    try {
      await f.started;
      f.queue.push(
        taskEvent(mkTask({ id: "a", revision: 2n, completedTime: timestampFromDate(new Date()) })),
      );
      f.queue.push(deleteEvent("b"));
      await waitFor(f.store, (s) => s.tasks.size === 0);
    } finally {
      f.stop();
    }
  });

  it("pulses external changes but not echoes of its own writes", async () => {
    const f = fixture([mkTask({ id: "a", revision: 1n })]);
    try {
      await f.started;

      await f.store.update("a", { title: "mine" });
      // The watch echo of our own write arrives...
      f.queue.push(taskEvent(f.serverTasks.get("a")!));
      // ...and an unrelated external change follows.
      f.queue.push(taskEvent(mkTask({ id: "x", title: "from the CLI", revision: 1n })));
      await waitFor(f.store, (s) => s.tasks.has("x"));

      const pulses = f.store.getSnapshot().pulses;
      expect(pulses.has("a")).toBe(false);
      expect(pulses.has("x")).toBe(true);
    } finally {
      f.stop();
    }
  });

  it("applies updates optimistically and keeps the authoritative revision", async () => {
    const f = fixture([mkTask({ id: "a", title: "before", revision: 1n })]);
    try {
      await f.started;
      const p = f.store.update("a", { title: "after", expectedRevision: 1n });
      // Optimistic state is visible before the RPC resolves.
      expect(f.store.getSnapshot().tasks.get("a")?.title).toBe("after");
      await p;
      expect(f.store.getSnapshot().tasks.get("a")?.revision).toBe(2n);
    } finally {
      f.stop();
    }
  });

  it("rolls back to server truth when a mutation is rejected", async () => {
    const f = fixture([mkTask({ id: "a", title: "truth", revision: 5n })]);
    try {
      await f.started;
      await expect(
        f.store.update("a", { title: "doomed", expectedRevision: 1n }),
      ).rejects.toThrow();
      await waitFor(f.store, (s) => s.tasks.get("a")?.title === "truth");
      expect(f.store.getSnapshot().tasks.get("a")?.revision).toBe(5n);
    } finally {
      f.stop();
    }
  });

  it("reconnects after a dropped stream and refetches", async () => {
    const f = fixture([mkTask({ id: "a" })]);
    try {
      await f.started;
      f.serverTasks.set("b", mkTask({ id: "b", title: "added while away" }));
      f.queue.close(); // server drops the stream
      await waitFor(f.store, (s) => s.tasks.has("b"));
      expect(f.calls.filter((c) => c === "watchTasks").length).toBeGreaterThanOrEqual(2);
    } finally {
      f.stop();
    }
  });

  it("everConnected turns true on the first handshake and survives a drop", async () => {
    const f = fixture([mkTask({ id: "a" })]);
    try {
      // Cold start: neither connected nor everConnected — the UI's grace
      // window keys on this to avoid flashing "can't reach the daemon".
      expect(f.store.getSnapshot().connected).toBe(false);
      expect(f.store.getSnapshot().everConnected).toBe(false);

      await f.started;
      expect(f.store.getSnapshot().everConnected).toBe(true);

      // A dropped stream flips connected off but everConnected STAYS true —
      // that's what identifies a real drop vs a cold load.
      f.queue.close();
      await waitFor(f.store, (s) => !s.connected);
      expect(f.store.getSnapshot().everConnected).toBe(true);
    } finally {
      f.stop();
    }
  });

  it("completing a task removes it from the active replica", async () => {
    const f = fixture([mkTask({ id: "a", revision: 1n })]);
    try {
      await f.started;
      await f.store.update("a", { completed: true });
      expect(f.store.getSnapshot().tasks.has("a")).toBe(false);
    } finally {
      f.stop();
    }
  });

  it("completing a RECURRING task is NOT optimistic and applies the advanced live task", async () => {
    const originalDue = timestampFromDate(new Date(2026, 6, 6));
    const f = fixture([
      mkTask({ id: "r", revision: 1n, recurrence: "FREQ=DAILY", dueTime: originalDue }),
    ]);
    try {
      await f.started;
      const p = f.store.update("r", { completed: true });

      // No optimistic apply: before the RPC resolves the replica still holds
      // the task unchanged — present, active, with the ORIGINAL due (the
      // client can't compute the next occurrence).
      const before = f.store.getSnapshot().tasks.get("r");
      expect(before).toBeDefined();
      expect(before!.completedTime).toBeUndefined();
      expect(before!.dueTime).toEqual(originalDue);
      expect(before!.revision).toBe(1n);

      // The awaited result carries the advanced live task + the archive copy.
      const res = await p;
      expect(res.spawnedOccurrence?.id).toBe("r-arch");
      expect(res.task?.revision).toBe(2n);

      // The live task landed immediately: still active, due advanced.
      const after = f.store.getSnapshot().tasks.get("r");
      expect(after).toBeDefined();
      expect(after!.completedTime).toBeUndefined();
      expect(after!.dueTime).not.toEqual(originalDue);
      expect(after!.revision).toBe(2n);
    } finally {
      f.stop();
    }
  });

  it("update returns an empty result for a plain (non-roll-forward) write", async () => {
    const f = fixture([mkTask({ id: "a", revision: 1n })]);
    try {
      await f.started;
      const res = await f.store.update("a", { title: "renamed" });
      expect(res.task?.title).toBe("renamed");
      expect(res.spawnedOccurrence).toBeUndefined();
    } finally {
      f.stop();
    }
  });

  it("create passes recurrence and parentId through to the server", async () => {
    const f = fixture([]);
    try {
      await f.started;
      const t = await f.store.create({ title: "sub", recurrence: "FREQ=DAILY", parentId: "p1" });
      const stored = f.serverTasks.get(t.id)!;
      expect(stored.recurrence).toBe("FREQ=DAILY");
      expect(stored.parentId).toBe("p1");
    } finally {
      f.stop();
    }
  });
});
