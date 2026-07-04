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
        const t = mkTask({ id: `srv-${serverTasks.size + 1}`, title: req.title, labels: req.labels, dueTime: req.dueTime });
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
        const next = { ...cur, revision: cur.revision + 1n };
        for (const p of req.updateMask?.paths ?? []) {
          if (p === "title") next.title = req.task?.title ?? "";
          if (p === "labels") next.labels = req.task?.labels ?? [];
          if (p === "completed_time") next.completedTime = req.task?.completedTime;
          if (p === "due_time") next.dueTime = req.task?.dueTime;
          if (p === "notes") next.notes = req.task?.notes ?? "";
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
});
