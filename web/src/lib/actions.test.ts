import { afterEach, describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { TaskSchema, type Task } from "../gen/task/task_pb";
import type { TaskStore, TaskPatch } from "./store";
import { notify } from "./notify";
import { completeMany, rescheduleMany, addLabelMany, deleteTaskWithUndo } from "./actions";

function mkTask(over: Partial<Task> & { id: string }): Task {
  return Object.assign(create(TaskSchema, { title: "t", revision: 1n }), over);
}

type CreateFields = Parameters<TaskStore["create"]>[0];

// A minimal store double: actions.ts only calls update/create/delete and
// never inspects what they resolve to, so recording the calls is enough.
function fakeStore() {
  const updates: { id: string; patch: TaskPatch }[] = [];
  const creates: CreateFields[] = [];
  const store = {
    update: (id: string, patch: TaskPatch) => {
      updates.push({ id, patch });
      return Promise.resolve();
    },
    create: (fields: CreateFields) => {
      creates.push(fields);
      return Promise.resolve(mkTask({ id: "new" }));
    },
    delete: (_id: string) => Promise.resolve(),
  };
  return { store: store as unknown as TaskStore, updates, creates };
}

const lastToast = () => notify.getSnapshot().at(-1);

afterEach(() => {
  for (const t of notify.getSnapshot().slice()) notify.dismiss(t.id);
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
