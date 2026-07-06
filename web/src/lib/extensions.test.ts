import { afterEach, describe, expect, it, vi } from "vitest";
import { create } from "@bufbuild/protobuf";
import { TaskSchema, type Task } from "../gen/task/task_pb";
import type { TaskStore, TaskPatch } from "./store";
import { buildAPI } from "./extensions";

// The extension boundary is type-only in the .d.ts, so a bundle can pass fields
// the host never meant to expose. buildAPI's store.update bridge is where that
// gets enforced at runtime: a whitelist of user-owned patch keys, the same
// field-ownership rule the UI applies (completion is source-owned on a synced
// task), and a void result so the internal UpdateResult can't leak.

function mkTask(over: Partial<Task> & { id: string }): Task {
  return Object.assign(create(TaskSchema, { title: "t", revision: 1n }), over);
}

function stubStore(tasks: Task[] = []) {
  const map = new Map<string, Task>(tasks.map((t) => [t.id, t]));
  const updates: { id: string; patch: TaskPatch }[] = [];
  const store = {
    update: (id: string, patch: TaskPatch) => {
      updates.push({ id, patch });
      return Promise.resolve({});
    },
    getSnapshot: () => ({ tasks: map, connected: true, pulses: new Map(), generation: 0 }),
    subscribe: () => () => {},
    create: () => Promise.resolve(mkTask({ id: "x" })),
    delete: () => Promise.resolve(),
    client: {},
  };
  return { store: store as unknown as TaskStore, updates };
}

afterEach(() => vi.restoreAllMocks());

describe("extension store.update boundary", () => {
  it("drops non-public keys (recurrence, parentId) and warns once, naming the extension", async () => {
    const warn = vi.spyOn(console, "warn").mockImplementation(() => {});
    const { store, updates } = stubStore([mkTask({ id: "a" })]);
    const api = buildAPI(store, "smuggler");

    await api.store.update("a", { title: "ok", recurrence: "FREQ=DAILY", parentId: "p" });

    expect(updates).toEqual([{ id: "a", patch: { title: "ok" } }]); // recurrence/parentId gone
    expect(warn.mock.calls.some((c) => String(c[0]).includes("smuggler"))).toBe(true);
  });

  it("strips completed:true on a synced task but keeps the rest of the patch", async () => {
    vi.spyOn(console, "warn").mockImplementation(() => {});
    const { store, updates } = stubStore([mkTask({ id: "gh", source: "github" })]);
    const api = buildAPI(store, "gcal");

    await api.store.update("gh", { completed: true, notes: "keep" });

    expect(updates).toEqual([{ id: "gh", patch: { notes: "keep" } }]);
  });

  it("passes a normal patch (incl. completed on a LOCAL task) straight through", async () => {
    const { store, updates } = stubStore([mkTask({ id: "a" })]); // local (source "")
    const api = buildAPI(store, "ext");
    const due = new Date(2026, 6, 10);

    await api.store.update("a", { title: "x", labels: ["l"], due, completed: true });

    expect(updates).toEqual([{ id: "a", patch: { title: "x", labels: ["l"], due, completed: true } }]);
  });

  it("resolves to void — the internal UpdateResult is not part of the contract", async () => {
    const { store } = stubStore([mkTask({ id: "a" })]);
    const api = buildAPI(store, "ext");
    expect(await api.store.update("a", { title: "x" })).toBeUndefined();
  });
});
