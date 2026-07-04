import { describe, expect, it } from "vitest";
import type { Task } from "../gen/task/task_pb";
import { computeReorder, taskOrder } from "./reorder";

const task = (id: string, order?: number): Task =>
  ({ id, userData: order === undefined ? {} : { order } }) as unknown as Task;

describe("taskOrder", () => {
  it("reads a numeric user_data.order, else undefined", () => {
    expect(taskOrder(task("a", 1024))).toBe(1024);
    expect(taskOrder(task("a"))).toBeUndefined();
    expect(taskOrder({ id: "a", userData: { order: "x" } } as unknown as Task)).toBeUndefined();
  });
});

describe("computeReorder", () => {
  const seq = [task("A", 0), task("B", 1024), task("C", 2048)];

  it("renumbers to spaced integers and returns only changed rows", () => {
    // Move C to the front → [C, A, B].
    const updates = computeReorder(seq, "C", 0);
    expect(updates).toEqual([
      { id: "C", order: 0 },
      { id: "A", order: 1024 },
      { id: "B", order: 2048 },
    ]);
  });

  it("is a no-op when the position does not change", () => {
    expect(computeReorder(seq, "A", 0)).toEqual([]);
  });

  it("clamps the target index into range", () => {
    const updates = computeReorder(seq, "A", 99); // move A past the end
    // Renumbered in the resulting order [B, C, A]; A lands last at 2048.
    expect(updates.map((u) => u.id)).toEqual(["B", "C", "A"]);
    expect(updates.find((u) => u.id === "A")!.order).toBe(2048);
  });

  it("returns nothing when the dragged id is absent", () => {
    expect(computeReorder(seq, "ZZ", 0)).toEqual([]);
  });
});
