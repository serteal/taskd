import type { Task } from "../gen/task/task_pb";

// Manual ordering lives in user_data.order (a number) — client-side only, so
// no proto change and it survives syncs (user-owned). Lists in "manual" sort
// read it; dragging a row renumbers to spaced integers (only changed rows are
// written), which keeps values clean without fractional drift.

const STEP = 1024;

export function taskOrder(t: Task): number | undefined {
  const o = (t.userData as Record<string, unknown> | undefined)?.order;
  return typeof o === "number" ? o : undefined;
}

/**
 * Given the current manually-sorted list and a move (dragged id inserted at
 * targetIndex among the others), return the {id, order} writes to apply —
 * only for rows whose order actually changes.
 */
export function computeReorder(
  sorted: Task[],
  draggedId: string,
  targetIndex: number,
): { id: string; order: number }[] {
  const dragged = sorted.find((t) => t.id === draggedId);
  if (!dragged) return [];
  const without = sorted.filter((t) => t.id !== draggedId);
  const idx = Math.max(0, Math.min(targetIndex, without.length));
  const next = [...without.slice(0, idx), dragged, ...without.slice(idx)];
  const updates: { id: string; order: number }[] = [];
  next.forEach((t, i) => {
    const want = i * STEP;
    if (taskOrder(t) !== want) updates.push({ id: t.id, order: want });
  });
  return updates;
}
