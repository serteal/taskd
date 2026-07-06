import { afterEach, describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { TaskSchema, type Task } from "../gen/task/task_pb";
import type { TaskStore, TaskPatch, UpdateResult } from "./store";
import { notify } from "./notify";
import {
  completeTask,
  completeMany,
  rescheduleTask,
  rescheduleMany,
  addLabel,
  addLabelMany,
  setPriority,
  setProject,
  planToday,
  deleteTaskWithUndo,
  deleteMany,
  SYNCED_COMPLETE_MSG,
  SYNCED_DUE_MSG,
} from "./actions";
import { _resetUndo, undoLast } from "./undo";

function mkTask(over: Partial<Task> & { id: string }): Task {
  return Object.assign(create(TaskSchema, { title: "t", revision: 1n }), over);
}

type CreateFields = Parameters<TaskStore["create"]>[0];

// A minimal store double: actions.ts calls update/create/delete; recurring
// completion additionally reads UpdateResult (the advanced live task + the
// spawned archive copy), served from `updateResults` by task id when present.
// `initial` seeds a live map so getSnapshot() reflects delete/create — the
// delete-undo path reads it to check whether a parent still exists and which
// children survived. create() returns distinct ids (`new-1`, `new-2`, …) so
// re-parenting undo can map an old id to the rebuilt one.
function fakeStore(updateResults: Record<string, UpdateResult> = {}, initial: Task[] = []) {
  const updates: { id: string; patch: TaskPatch }[] = [];
  const creates: CreateFields[] = [];
  const deletes: string[] = [];
  const live = new Map<string, Task>(initial.map((t) => [t.id, t]));
  const store = {
    update: (id: string, patch: TaskPatch) => {
      updates.push({ id, patch });
      return Promise.resolve(updateResults[id] ?? {});
    },
    create: (fields: CreateFields) => {
      creates.push(fields);
      const t = mkTask({ id: `new-${creates.length}`, title: fields.title });
      live.set(t.id, t);
      return Promise.resolve(t);
    },
    delete: (id: string) => {
      deletes.push(id);
      live.delete(id);
      return Promise.resolve();
    },
    getSnapshot: () => ({ tasks: live, connected: true, pulses: new Map(), generation: 0 }),
  };
  return { store: store as unknown as TaskStore, updates, creates, deletes, live };
}

const lastToast = () => notify.getSnapshot().at(-1);

afterEach(() => {
  for (const t of notify.getSnapshot().slice()) notify.dismiss(t.id);
  _resetUndo();
});

describe("synced tasks can't be completed client-side", () => {
  it("completeTask no-ops with an info toast for a synced task, no write", () => {
    const { store, updates } = fakeStore();
    completeTask(store, mkTask({ id: "gh", source: "github", title: "PR #1" }));
    expect(updates).toHaveLength(0);
    expect(lastToast()?.message).toBe(SYNCED_COMPLETE_MSG);
    expect(lastToast()?.kind).toBe("info");
  });

  it("completeTask completes and offers Undo for a local task", () => {
    const { store, updates } = fakeStore();
    completeTask(store, mkTask({ id: "a", title: "local" }));
    expect(updates.map((u) => u.id)).toEqual(["a"]);
    expect(lastToast()?.message).toBe("Completed “local”");
    expect(lastToast()?.action?.label).toBe("Undo");
  });

  it("completeMany completes only the local tasks and names the skipped synced count", () => {
    const { store, updates } = fakeStore();
    completeMany(store, [mkTask({ id: "a" }), mkTask({ id: "gh", source: "github" })]);
    expect(updates.map((u) => u.id)).toEqual(["a"]); // synced skipped
    expect(lastToast()?.message).toBe("Completed 1 task · 1 synced skipped");
  });

  it("completeMany with only synced tasks writes nothing and explains why", () => {
    const { store, updates } = fakeStore();
    completeMany(store, [mkTask({ id: "gh", source: "github" })]);
    expect(updates).toHaveLength(0);
    expect(lastToast()?.message).toBe(`${SYNCED_COMPLETE_MSG} — 1 task skipped`);
  });
});

describe("deleteTaskWithUndo on a synced task", () => {
  it("offers no Undo and warns the item may return on the next sync", () => {
    const { store, creates } = fakeStore();
    deleteTaskWithUndo(store, mkTask({ id: "gh", source: "github", title: "PR" }));
    expect(lastToast()?.action).toBeUndefined();
    expect(lastToast()?.message).toContain("may return on the next sync");
    expect(creates).toHaveLength(0);
  });
});

describe("bulk action toasts pluralize by count", () => {
  it("completeMany: singular for one task, plural for more", () => {
    const { store } = fakeStore();
    completeMany(store, [mkTask({ id: "a" })]);
    expect(lastToast()?.message).toBe("Completed 1 task");

    completeMany(store, [mkTask({ id: "a" }), mkTask({ id: "b" })]);
    expect(lastToast()?.message).toBe("Completed 2 tasks");
  });
});

describe("rescheduleMany undo", () => {
  it("restores each task's own prior due date, not a shared one", () => {
    const { store, updates } = fakeStore();
    const a = mkTask({ id: "a", revision: 1n, dueTime: timestampFromDate(new Date(2026, 6, 1)) });
    const b = mkTask({ id: "b", revision: 1n, dueTime: timestampFromDate(new Date(2026, 6, 5)) });

    rescheduleMany(store, [a, b], new Date(2026, 6, 10));
    updates.length = 0; // clear the forward-scheduling calls; only Undo matters below

    lastToast()!.action!.onClick();

    const dueFor = (id: string) => updates.find((u) => u.id === id)?.patch.due as Date;
    expect(dueFor("a").getDate()).toBe(1);
    expect(dueFor("b").getDate()).toBe(5);
  });
});

describe("addLabelMany", () => {
  it("skips a task that already has the label, updates the rest, still reports the full count", () => {
    const { store, updates } = fakeStore();
    const has = mkTask({ id: "a", labels: ["urgent"] });
    const doesnt = mkTask({ id: "b", labels: [] });

    addLabelMany(store, [has, doesnt], "urgent");

    expect(updates.map((u) => u.id)).toEqual(["b"]);
    expect(lastToast()?.message).toBe("Labeled 2 tasks “urgent”");
  });
});

describe("completing a recurring task", () => {
  const prevDue = new Date(2026, 6, 6, 23, 59, 59);
  const nextDue = new Date(2026, 6, 9, 23, 59, 59);
  const recurring = () =>
    mkTask({
      id: "r",
      title: "Water plants",
      recurrence: "FREQ=DAILY;INTERVAL=3",
      dueTime: timestampFromDate(prevDue),
    });
  // What the server answers: the live task rolled forward + the archive copy.
  const results = () => ({
    r: {
      task: mkTask({
        id: "r",
        recurrence: "FREQ=DAILY;INTERVAL=3",
        dueTime: timestampFromDate(nextDue),
        revision: 2n,
      }),
      spawnedOccurrence: mkTask({ id: "r-arch", completedTime: timestampFromDate(new Date()) }),
    },
  });

  it("awaits the server, toasts the next date, and Undo restores due + deletes the archive", async () => {
    const { store, updates, deletes } = fakeStore(results());
    completeTask(store, recurring());
    await vi.waitFor(() => expect(lastToast()?.message).toMatch(/^Completed — next /));

    // Exactly one write so far: the completion, guarded by the task's revision.
    expect(updates).toEqual([{ id: "r", patch: { completed: true, expectedRevision: 1n } }]);

    lastToast()!.action!.onClick();
    await vi.waitFor(() => expect(deletes).toEqual(["r-arch"]));
    // Undo restored the PREVIOUS due (last-write-wins), not the advanced one.
    const undoWrite = updates.find((u) => u.patch.due !== undefined);
    expect(undoWrite?.id).toBe("r");
    expect((undoWrite?.patch.due as Date).getTime()).toBe(prevDue.getTime());
  });

  it("toast Undo and ⌘Z share one entry — the reversal runs at most once", async () => {
    const { store, updates, deletes } = fakeStore(results());
    completeTask(store, recurring());
    await vi.waitFor(() => expect(lastToast()?.action).toBeTruthy());

    lastToast()!.action!.onClick();
    await vi.waitFor(() => expect(deletes).toHaveLength(1));
    const writesAfterUndo = updates.length;

    expect(undoLast()).toBeNull(); // the shared entry is already consumed
    expect(deletes).toHaveLength(1); // no second archive delete
    expect(updates).toHaveLength(writesAfterUndo); // no second due restore
  });

  it("⌘Z reaches the same reversal when the toast is untouched", async () => {
    const { store, deletes } = fakeStore(results());
    completeTask(store, recurring());
    await vi.waitFor(() => expect(lastToast()?.action).toBeTruthy());

    expect(undoLast()?.label).toContain("complete");
    await vi.waitFor(() => expect(deletes).toEqual(["r-arch"]));
  });

  it("completeMany routes recurring members through the per-task path", async () => {
    const { store, updates } = fakeStore(results());
    completeMany(store, [recurring(), mkTask({ id: "b", title: "one-off" })]);

    // The one-off half gets the batch toast; the recurring member lands its
    // own "next" toast once the server answers.
    expect(notify.getSnapshot().some((t) => t.message === "Completed 1 task")).toBe(true);
    await vi.waitFor(() =>
      expect(notify.getSnapshot().some((t) => t.message.startsWith("Completed — next"))).toBe(true),
    );
    expect(updates.map((u) => u.id).sort()).toEqual(["b", "r"]);
  });

  it("completeMany with only recurring members shows no batch toast", async () => {
    const { store } = fakeStore(results());
    completeMany(store, [recurring()]);
    await vi.waitFor(() => expect(lastToast()?.message).toMatch(/^Completed — next /));
    expect(notify.getSnapshot().filter((t) => /Completed \d/.test(t.message))).toHaveLength(0);
  });

  it("emits the synced-skipped notice even when EVERY local member is recurring", async () => {
    // All-recurring + a synced task: there's no one-off batch toast to carry a
    // "· N synced skipped" suffix, so the skip must be reported on its own —
    // it used to be dropped by an early return (item 5).
    const { store } = fakeStore(results());
    completeMany(store, [recurring(), mkTask({ id: "gh", source: "github" })]);
    expect(notify.getSnapshot().some((t) => t.message === "1 synced skipped")).toBe(true);
    // The recurring member still rolls forward on its own path.
    await vi.waitFor(() =>
      expect(notify.getSnapshot().some((t) => t.message.startsWith("Completed — next"))).toBe(true),
    );
  });
});

describe("deleteTaskWithUndo", () => {
  it("undo recreates the task with the same title, notes, labels and due", () => {
    const { store, creates } = fakeStore();
    const t = mkTask({
      id: "a",
      title: "Buy milk",
      notes: "2%",
      labels: ["errand"],
      dueTime: timestampFromDate(new Date(2026, 6, 4)),
    });

    deleteTaskWithUndo(store, t);
    lastToast()!.action!.onClick();

    expect(creates).toHaveLength(1);
    expect(creates[0]).toMatchObject({ title: "Buy milk", notes: "2%", labels: ["errand"] });
    expect((creates[0].due as Date).getDate()).toBe(4);
  });
});

describe("delete-undo restores full fidelity (recurrence, parent link, children)", () => {
  it("single recurring delete-undo recreates WITH the recurrence rule", () => {
    const { store, creates } = fakeStore();
    deleteTaskWithUndo(store, mkTask({ id: "r", title: "Water", recurrence: "FREQ=DAILY;INTERVAL=3" }));
    lastToast()!.action!.onClick();
    expect(creates).toHaveLength(1);
    expect(creates[0].recurrence).toBe("FREQ=DAILY;INTERVAL=3"); // was silently dropped before
  });

  it("subtask delete-undo recreates WITH the parent link when the parent still exists", () => {
    const parent = mkTask({ id: "p", title: "Parent" });
    const child = mkTask({ id: "c", title: "Child", parentId: "p" });
    const { store, creates } = fakeStore({}, [parent, child]);
    deleteTaskWithUndo(store, child);
    lastToast()!.action!.onClick();
    expect(creates[0].parentId).toBe("p");
  });

  it("subtask delete-undo falls back to top-level when the parent has vanished", () => {
    const child = mkTask({ id: "c", title: "Child", parentId: "gone" });
    const { store, creates } = fakeStore({}, [child]); // parent "gone" not in the store
    deleteTaskWithUndo(store, child);
    lastToast()!.action!.onClick();
    expect(creates[0].parentId).toBeUndefined();
  });

  it("parent-with-children delete-undo re-nests the surviving children under the rebuilt id", async () => {
    const parent = mkTask({ id: "p", title: "Parent" });
    const child = mkTask({ id: "c", title: "Child", parentId: "p" });
    const { store, creates, updates } = fakeStore({}, [parent, child]);

    // Single delete of the parent: the server re-parents the child to
    // top-level, so it survives and undo must re-nest it.
    deleteTaskWithUndo(store, parent);
    lastToast()!.action!.onClick();

    await vi.waitFor(() => expect(creates).toHaveLength(1));
    await vi.waitFor(() =>
      expect(updates).toContainEqual({ id: "c", patch: { parentId: "new-1" } }),
    );
  });

  it("bulk parent+child delete-undo rebuilds the pair, child mapped to the parent's NEW id", async () => {
    const parent = mkTask({ id: "p", title: "Parent" });
    const child = mkTask({ id: "c", title: "Child", parentId: "p" });
    const { store, creates } = fakeStore({}, [parent, child]);

    deleteMany(store, [parent, child]);
    lastToast()!.action!.onClick();

    await vi.waitFor(() => expect(creates).toHaveLength(2));
    const parentCreate = creates.find((c) => c.title === "Parent")!;
    const childCreate = creates.find((c) => c.title === "Child")!;
    expect(parentCreate.parentId).toBeUndefined(); // pass 1: rebuilt top-level
    expect(childCreate.parentId).toBe("new-1"); // pass 2: mapped to the parent's rebuilt id
  });
});

describe("scheduling is guarded on synced tasks (due is source-owned)", () => {
  it("rescheduleTask no-ops with an info toast for a synced task", () => {
    const { store, updates } = fakeStore();
    rescheduleTask(store, mkTask({ id: "gh", source: "github" }), new Date(2026, 6, 10));
    expect(updates).toHaveLength(0);
    expect(lastToast()?.message).toBe(SYNCED_DUE_MSG);
    expect(lastToast()?.kind).toBe("info");
  });

  it("rescheduleTask still schedules a local task", () => {
    const { store, updates } = fakeStore();
    rescheduleTask(store, mkTask({ id: "a" }), new Date(2026, 6, 10));
    expect(updates.map((u) => u.id)).toEqual(["a"]);
  });

  it("rescheduleMany reschedules only local members and names the skipped synced count", () => {
    const { store, updates } = fakeStore();
    rescheduleMany(
      store,
      [mkTask({ id: "a" }), mkTask({ id: "gh", source: "github" })],
      new Date(2026, 6, 10),
    );
    expect(updates.map((u) => u.id)).toEqual(["a"]); // synced skipped
    expect(lastToast()?.message).toBe("Scheduled 1 task · 1 synced skipped");
  });

  it("rescheduleMany keeps the skipped notice even when EVERY local member was scheduled", () => {
    const { store } = fakeStore();
    rescheduleMany(
      store,
      [mkTask({ id: "a" }), mkTask({ id: "b" }), mkTask({ id: "gh", source: "github" })],
      new Date(2026, 6, 10),
    );
    expect(lastToast()?.message).toBe("Scheduled 2 tasks · 1 synced skipped");
  });

  it("rescheduleMany with only synced tasks writes nothing and explains why", () => {
    const { store, updates } = fakeStore();
    rescheduleMany(store, [mkTask({ id: "gh", source: "github" })], new Date(2026, 6, 10));
    expect(updates).toHaveLength(0);
    expect(lastToast()?.message).toBe(`${SYNCED_DUE_MSG} — 1 task skipped`);
  });
});

describe("⌘Z coverage for light edits (each pushes a single-consumption undo)", () => {
  it("setPriority restores the exact prior labels on undo", () => {
    const { store, updates } = fakeStore();
    setPriority(store, mkTask({ id: "a", labels: ["keep"] }), "p1");
    expect(updates[0].patch.labels).toEqual(["keep", "p1"]);
    expect(lastToast()?.action?.label).toBe("Undo");
    updates.length = 0;
    lastToast()!.action!.onClick();
    expect(updates).toEqual([{ id: "a", patch: { labels: ["keep"] } }]);
  });

  it("addLabel restores the exact prior labels on undo", () => {
    const { store, updates } = fakeStore();
    addLabel(store, mkTask({ id: "a", labels: ["keep"] }), "urgent");
    updates.length = 0;
    lastToast()!.action!.onClick();
    expect(updates).toEqual([{ id: "a", patch: { labels: ["keep"] } }]);
  });

  it("setProject restores the exact prior labels on undo", () => {
    const { store, updates } = fakeStore();
    setProject(store, mkTask({ id: "a", labels: ["project:old"] }), "project:new");
    expect(updates[0].patch.labels).toEqual(["project:new"]);
    updates.length = 0;
    lastToast()!.action!.onClick();
    expect(updates).toEqual([{ id: "a", patch: { labels: ["project:old"] } }]);
  });

  it("planToday restores the exact prior user_data (timebox) on undo", () => {
    const { store, updates } = fakeStore();
    planToday(store, mkTask({ id: "a", userData: { note: "x" } }), new Date(2026, 6, 6, 9, 0));
    updates.length = 0;
    lastToast()!.action!.onClick();
    expect(updates).toEqual([{ id: "a", patch: { userData: { note: "x" } } }]);
  });

  it("addLabelMany pushes ONE undo that restores each task's prior labels", () => {
    const { store, updates } = fakeStore();
    addLabelMany(store, [mkTask({ id: "a", labels: [] }), mkTask({ id: "b", labels: ["x"] })], "new");
    updates.length = 0;
    lastToast()!.action!.onClick();
    expect(updates).toEqual([
      { id: "a", patch: { labels: [] } },
      { id: "b", patch: { labels: ["x"] } },
    ]);
  });

  it("toast Undo and ⌘Z share one entry — the edit reverses at most once", () => {
    const { store, updates } = fakeStore();
    addLabel(store, mkTask({ id: "a", labels: [] }), "urgent");
    updates.length = 0;
    lastToast()!.action!.onClick(); // consume via the toast
    expect(updates).toHaveLength(1);
    expect(undoLast()).toBeNull(); // ⌘Z finds the shared entry already consumed
    expect(updates).toHaveLength(1); // no second reversal
  });
});
