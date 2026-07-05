import { describe, expect, it, vi } from "vitest";

// savedviews reads localStorage at import time (node vitest has none), so stub
// it before importing the module under test.
const mem = new Map<string, string>();
vi.stubGlobal("localStorage", {
  getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
  setItem: (k: string, v: string) => void mem.set(k, v),
  removeItem: (k: string) => void mem.delete(k),
  clear: () => mem.clear(),
});

const { savedViews } = await import("./savedviews");

describe("savedViews store", () => {
  it("adds, persists to localStorage, and removes", () => {
    savedViews.add({
      name: "Hot",
      view: { kind: "today" },
      sort: "smart",
      board: false,
      groupBy: "priority",
    });

    const snap = savedViews.getSnapshot();
    expect(snap.map((v) => v.name)).toContain("Hot");
    expect(localStorage.getItem("taskd-saved-views")).toContain("Hot");

    const id = snap.find((v) => v.name === "Hot")!.id;
    expect(typeof id).toBe("string");
    savedViews.remove(id);
    expect(savedViews.getSnapshot().some((v) => v.id === id)).toBe(false);
  });

  it("loads legacy entries that still carry a removed `search` field", async () => {
    // Simulate a pre-migration localStorage payload, then load a fresh module.
    mem.set(
      "taskd-saved-views",
      JSON.stringify([
        { id: "old", name: "Legacy", view: { kind: "today" }, sort: "smart", board: false, groupBy: "priority", search: "milk" },
      ]),
    );
    vi.resetModules();
    const { savedViews: reloaded } = await import("./savedviews");
    const found = reloaded.getSnapshot().find((v) => v.name === "Legacy");
    expect(found).toBeTruthy();
    // The old `search` field is simply ignored — no throw, no dependence on it.
    expect(found!.sort).toBe("smart");
  });
});
