import { beforeEach, describe, expect, it, vi } from "vitest";
import { readVersionedJSON, writeVersionedJSON } from "./storage";

const mem = new Map<string, string>();
beforeEach(() => {
  mem.clear();
  vi.stubGlobal("localStorage", {
    getItem: (k: string) => (mem.has(k) ? mem.get(k)! : null),
    setItem: (k: string, v: string) => void mem.set(k, v),
    removeItem: (k: string) => void mem.delete(k),
    clear: () => mem.clear(),
  });
});

const KEY = "taskd-test";

describe("readVersionedJSON / writeVersionedJSON", () => {
  it("round-trips an enveloped value at the current version", () => {
    writeVersionedJSON(KEY, 1, [{ a: 1 }, { a: 2 }]);
    expect(mem.get(KEY)).toBe(JSON.stringify({ v: 1, data: [{ a: 1 }, { a: 2 }] }));
    expect(readVersionedJSON(KEY, { version: 1 })).toEqual([{ a: 1 }, { a: 2 }]);
  });

  it("returns null for a missing key", () => {
    expect(readVersionedJSON(KEY, { version: 1 })).toBeNull();
  });

  it("returns null (not a throw) for corrupt / non-JSON", () => {
    mem.set(KEY, "{not json");
    expect(readVersionedJSON(KEY, { version: 1 })).toBeNull();
  });

  it("reads a legacy pre-envelope ARRAY and rewrites it enveloped", () => {
    // Exactly how existing users' saved-views/filters look before this change.
    mem.set(KEY, JSON.stringify([{ id: "x", name: "Legacy" }]));
    const got = readVersionedJSON<Array<{ id: string; name: string }>>(KEY, { version: 1 });
    expect(got).toEqual([{ id: "x", name: "Legacy" }]);
    // And the stored value is now the envelope, so the upgrade is one-time.
    expect(mem.get(KEY)).toBe(JSON.stringify({ v: 1, data: [{ id: "x", name: "Legacy" }] }));
  });

  it("reads a legacy pre-envelope OBJECT and rewrites it enveloped", () => {
    // The view-prefs map is a plain object keyed by view search string.
    mem.set(KEY, JSON.stringify({ "?view=today": { sort: "manual" } }));
    const got = readVersionedJSON(KEY, { version: 1 });
    expect(got).toEqual({ "?view=today": { sort: "manual" } });
    expect(mem.get(KEY)).toBe(JSON.stringify({ v: 1, data: { "?view=today": { sort: "manual" } } }));
  });

  it("returns null when the stored envelope is a NEWER version (don't downgrade)", () => {
    mem.set(KEY, JSON.stringify({ v: 5, data: ["from the future"] }));
    expect(readVersionedJSON(KEY, { version: 1 })).toBeNull();
    // Left untouched for the newer client.
    expect(mem.get(KEY)).toBe(JSON.stringify({ v: 5, data: ["from the future"] }));
  });

  it("migrates an OLDER envelope forward and rewrites at the new version", () => {
    mem.set(KEY, JSON.stringify({ v: 1, data: { name: "old" } }));
    const migrate = (data: unknown, from: number | null) => {
      expect(from).toBe(1);
      return { ...(data as object), migrated: true };
    };
    const got = readVersionedJSON(KEY, { version: 2, migrate });
    expect(got).toEqual({ name: "old", migrated: true });
    expect(mem.get(KEY)).toBe(JSON.stringify({ v: 2, data: { name: "old", migrated: true } }));
  });

  it("discards a value the migrate function rejects (returns null)", () => {
    mem.set(KEY, JSON.stringify("a bare string that isn't our shape"));
    const got = readVersionedJSON(KEY, { version: 1, migrate: () => null });
    expect(got).toBeNull();
  });

  it("write swallows a storage error instead of throwing", () => {
    vi.stubGlobal("localStorage", {
      getItem: () => null,
      setItem: () => {
        throw new Error("quota exceeded");
      },
    });
    expect(() => writeVersionedJSON(KEY, 1, { a: 1 })).not.toThrow();
  });
});
