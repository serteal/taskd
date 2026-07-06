import type { Task } from "../gen/task/task_pb";
import { tsDate } from "./format";

/** Ids of local (non-synced), currently-due tasks at `now`. Pure so the
 *  transition logic in useDueReminders — which fires a notification only for
 *  ids newly entering this set, not ones already in it — is unit-testable
 *  without a DOM. Synced tasks are excluded to match the built-in lists'
 *  local-by-default rule (see the sources≠tasks model in views.ts). */
export function dueTaskIds(tasks: Iterable<Task>, now: Date): Set<string> {
  const out = new Set<string>();
  for (const t of tasks) {
    if (t.source !== "") continue;
    const due = tsDate(t.dueTime);
    if (due !== undefined && due.getTime() <= now.getTime()) out.add(t.id);
  }
  return out;
}

/** Local (non-synced) tasks whose due time fell in the half-open window
 *  (watermarkMs, now] — i.e. strictly after the last watermark and at or before
 *  now. This is the "became due while the app was closed" backlog: on load,
 *  useDueReminders reads the persisted watermark and summarises whatever slipped
 *  through. The lower bound is exclusive (already reported last session) and the
 *  upper bound inclusive (exactly-now counts as due), matching dueTaskIds.
 *  Synced tasks are excluded, same as the built-in lists' local-by-default rule. */
export function missedSince(tasks: Iterable<Task>, watermarkMs: number, now: Date): Task[] {
  const out: Task[] = [];
  const nowMs = now.getTime();
  for (const t of tasks) {
    if (t.source !== "") continue;
    const due = tsDate(t.dueTime);
    if (due === undefined) continue;
    const d = due.getTime();
    if (d > watermarkMs && d <= nowMs) out.push(t);
  }
  return out;
}
