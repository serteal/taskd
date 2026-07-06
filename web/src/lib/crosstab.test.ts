import { describe, expect, it, vi } from "vitest";

// Cross-tab convergence for the localStorage-backed stores (saved filters,
// saved views, per-view prefs): when ANOTHER document writes a store's key,
// the store reloads and notifies — so a PWA window and a browser tab converge
// instead of last-writer-wins destroying each other's edits.
//
// The stores bind their `storage` listeners at module init, so window +
// localStorage are stubbed BEFORE the dynamic imports below.

const mem = new Map<string, string>();
const win = new EventTarget();
vi.stubGlobal("window", win);
vi.stubGlobal("localStorage", {
  getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
  setItem: (k: string, v: string) => void mem.set(k, v),
  removeItem: (k: string) => void mem.delete(k),
  clear: () => mem.clear(),
});

const { savedFilters } = await import("./filters");
const { savedViews } = await import("./savedviews");
const { readViewPrefs, subscribeViewPrefs, DEFAULT_VIEW_PREFS } = await import("./views");

/** What another tab's write looks like from here: the shared storage already
 *  holds the new value when the `storage` event arrives. */
function externalWrite(key: string, data: unknown): void {
  mem.set(key, JSON.stringify({ v: 1, data }));
  win.dispatchEvent(Object.assign(new Event("storage"), { key }));
}

describe("cross-tab: saved filters", () => {
  it("reloads on another tab's write and notifies subscribers", () => {
    let notified = 0;
    const unsub = savedFilters.subscribe(() => notified++);

    externalWrite("taskd-filters", [{ id: "ext-1", name: "FromOtherTab", predicate: {} }]);

    expect(notified).toBe(1);
    expect(savedFilters.getSnapshot().map((f) => f.name)).toContain("FromOtherTab");
    expect(savedFilters.get("ext-1")?.name).toBe("FromOtherTab");
    unsub();
  });

  it("a later local add() builds on the other tab's edit instead of clobbering it", () => {
    externalWrite("taskd-filters", [{ id: "ext-2", name: "Theirs", predicate: {} }]);

    savedFilters.add({ name: "Mine", predicate: { text: "x" } });

    // Both survive — in memory AND in the persisted array.
    const names = savedFilters.getSnapshot().map((f) => f.name);
    expect(names).toContain("Theirs");
    expect(names).toContain("Mine");
    const stored = JSON.parse(mem.get("taskd-filters")!) as { data: Array<{ name: string }> };
    expect(stored.data.map((f) => f.name)).toEqual(expect.arrayContaining(["Theirs", "Mine"]));
  });
});

describe("cross-tab: saved views", () => {
  it("reloads on another tab's write, and a local add preserves it", () => {
    let notified = 0;
    const unsub = savedViews.subscribe(() => notified++);

    externalWrite("taskd-saved-views", [
      { id: "sv-1", name: "TheirView", view: { kind: "today" }, sort: "smart", board: false, groupBy: "priority" },
    ]);
    expect(notified).toBe(1);
    expect(savedViews.getSnapshot().map((v) => v.name)).toContain("TheirView");

    savedViews.add({ name: "MyView", view: { kind: "inbox" }, sort: "title", board: false, groupBy: "priority" });
    const stored = JSON.parse(mem.get("taskd-saved-views")!) as { data: Array<{ name: string }> };
    expect(stored.data.map((v) => v.name)).toEqual(expect.arrayContaining(["TheirView", "MyView"]));
    unsub();
  });
});

describe("cross-tab: view prefs", () => {
  it("notifies subscribers when another tab writes the prefs map, and re-reads fresh", () => {
    let notified = 0;
    const unsub = subscribeViewPrefs(() => notified++);

    expect(readViewPrefs({ kind: "today" })).toEqual(DEFAULT_VIEW_PREFS);

    externalWrite("taskd-view-prefs", { "?view=today": { sort: "manual", board: true } });

    expect(notified).toBe(1);
    // The re-read (what App does in its subscription) sees the other tab's
    // choice merged over defaults.
    expect(readViewPrefs({ kind: "today" })).toEqual({ sort: "manual", board: true, groupBy: "priority" });
    unsub();
  });

  it("ignores writes to unrelated keys", () => {
    let notified = 0;
    const unsub = subscribeViewPrefs(() => notified++);
    win.dispatchEvent(Object.assign(new Event("storage"), { key: "some-other-key" }));
    expect(notified).toBe(0);
    unsub();
  });

  it("a storage clear (key === null) also triggers a reload", () => {
    externalWrite("taskd-filters", [{ id: "gone-soon", name: "Doomed", predicate: {} }]);
    expect(savedFilters.get("gone-soon")).toBeTruthy();

    mem.clear();
    win.dispatchEvent(Object.assign(new Event("storage"), { key: null }));
    expect(savedFilters.getSnapshot()).toEqual([]);
  });
});
