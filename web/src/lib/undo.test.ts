import { beforeEach, describe, expect, it, vi } from "vitest";
import { pushUndo, consumeUndo, undoLast, _resetUndo } from "./undo";

beforeEach(() => _resetUndo());

describe("undo stack ordering", () => {
  it("undoLast runs the most recent entry first (LIFO)", () => {
    const order: string[] = [];
    pushUndo("a", () => void order.push("a"));
    pushUndo("b", () => void order.push("b"));
    pushUndo("c", () => void order.push("c"));

    expect(undoLast()?.label).toBe("c");
    expect(undoLast()?.label).toBe("b");
    expect(undoLast()?.label).toBe("a");
    expect(undoLast()).toBeNull(); // empty
    expect(order).toEqual(["c", "b", "a"]);
  });

  it("returns null when there is nothing left to undo", () => {
    expect(undoLast()).toBeNull();
  });
});

describe("single consumption across ⌘Z and toast-Undo", () => {
  it("consumeUndo runs the reversal exactly once", () => {
    const run = vi.fn();
    const entry = pushUndo("x", run);

    expect(consumeUndo(entry)).toBe(true);
    expect(consumeUndo(entry)).toBe(false); // already consumed
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("a toast-Undo (consumeUndo) then ⌘Z (undoLast) does not double-fire", () => {
    const run = vi.fn();
    const entry = pushUndo("x", run);

    consumeUndo(entry); // user clicked the toast's Undo
    // ⌘Z now skips the consumed entry entirely — nothing else on the stack.
    expect(undoLast()).toBeNull();
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("⌘Z (undoLast) then a later toast-Undo (consumeUndo) does not double-fire", () => {
    const run = vi.fn();
    const entry = pushUndo("x", run);

    expect(undoLast()?.label).toBe("x"); // ⌘Z consumed it
    expect(consumeUndo(entry)).toBe(false); // stale toast click is a no-op
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("undoLast skips already-consumed entries and reaches the next live one", () => {
    const runA = vi.fn();
    const runB = vi.fn();
    const a = pushUndo("a", runA);
    pushUndo("b", runB);

    consumeUndo(a); // consume the OLDER entry out of band
    expect(undoLast()?.label).toBe("b"); // newest live entry
    expect(undoLast()).toBeNull(); // a was already consumed
    expect(runA).toHaveBeenCalledTimes(1);
    expect(runB).toHaveBeenCalledTimes(1);
  });
});

describe("stack cap", () => {
  it("keeps at most 50 entries, dropping the oldest", () => {
    const runs: number[] = [];
    for (let i = 0; i < 60; i++) pushUndo(`e${i}`, () => void runs.push(i));

    // The 10 oldest (0..9) were evicted; 59 down to 10 remain, newest first.
    expect(undoLast()?.label).toBe("e59");
    let count = 1;
    while (undoLast()) count++;
    expect(count).toBe(50);
    // Nothing below index 10 ever ran.
    expect(Math.min(...runs)).toBe(10);
  });
});
