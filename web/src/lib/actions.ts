import type { JsonObject } from "@bufbuild/protobuf";
import type { Task } from "../gen/task/task_pb";
import type { TaskStore } from "./store";
import { notify } from "./notify";
import { humanDue, tsDate, chipParts } from "./format";
import { planTodayInterval } from "./timebox";
import { pushUndo, consumeUndo, type UndoEntry } from "./undo";

// Undoable task operations, shared by the command palette, row hover actions,
// bulk bar, and keyboard. Each performs the change optimistically and shows a
// toast with Undo. Undo uses last-write-wins (expectedRevision 0) since the
// task's revision has moved on by the time the user clicks.
//
// Every undoable op also pushes an entry onto the global undo stack (lib/undo)
// so ⌘Z reaches it too; the toast's own Undo button routes through the SAME
// entry via consumeUndo, so toast-Undo and ⌘Z can never both fire one closure.

const short = (s: string) => (s.length > 32 ? s.slice(0, 31) + "…" : s);

// Until write-back to sources exists, the UI must not offer completion on a
// synced task: the next full-snapshot sync would just revert it. Every
// completion affordance is guarded; this is the shared copy.
export const SYNCED_COMPLETE_MSG = "Completion follows the source";
// Due is source-owned on a synced task the same way completion is: the next
// full-snapshot sync reverts any UI write, so scheduling is guarded at the
// action layer and this is the shared copy for the notice.
export const SYNCED_DUE_MSG = "The due date follows the source";

/** A toast Undo action bound to a stack entry (shared consumption path). */
const undoAction = (entry: UndoEntry) => ({ label: "Undo", onClick: () => void consumeUndo(entry) });

// --- delete-undo fidelity helpers ------------------------------------------
//
// A delete's undo recreates the task as a brand-new local one (a new id), so it
// must carry EVERY user-owned field forward — including recurrence and the
// parent link — or the undo silently downgrades a recurring task to a one-off
// and orphans a subtask. Parent deletes are extra work: the server re-parents
// surviving children to top-level, so undo re-nests them under the rebuilt id.

/** The create fields undo needs to rebuild a deleted task with full fidelity:
 *  title/notes/labels/due plus recurrence and (when it still exists) its
 *  parent. `parentId` is resolved by the caller against the live snapshot. */
function recreateFields(t: Task, parentId: string | undefined) {
  return {
    title: t.title,
    notes: t.notes,
    labels: t.labels,
    due: tsDate(t.dueTime),
    recurrence: t.recurrence || undefined,
    parentId,
  };
}

/** Ids of tasks currently parented to `parentId`, read from the live snapshot. */
function childrenOf(store: TaskStore, parentId: string): string[] {
  return [...store.getSnapshot().tasks.values()]
    .filter((c) => c.parentId === parentId)
    .map((c) => c.id);
}

/** Best-effort re-parent of each STILL-EXISTING child under `newParentId`
 *  (last-write-wins — the child's revision has moved on since delete). */
function relinkChildren(store: TaskStore, childIds: string[], newParentId: string): void {
  const snap = store.getSnapshot();
  for (const cid of childIds) {
    if (snap.tasks.has(cid)) void store.update(cid, { parentId: newParentId }).catch(() => {});
  }
}

export function completeTask(store: TaskStore, t: Task): void {
  // Defense-in-depth: even if a caller reaches here for a synced task, no-op
  // with an explanatory toast instead of a write the sync will undo.
  if (t.source !== "") {
    notify.toast({ kind: "info", message: SYNCED_COMPLETE_MSG });
    return;
  }
  if (t.recurrence !== "") {
    completeRecurring(store, t);
    return;
  }
  store.update(t.id, { completed: true, expectedRevision: t.revision }).catch(() => {});
  const entry = pushUndo(`complete “${short(t.title)}”`, () =>
    void store.update(t.id, { completed: false }).catch(() => {}),
  );
  notify.toast({
    kind: "success",
    message: `Completed “${short(t.title)}”`,
    action: undoAction(entry),
  });
}

// Completing a recurring task is a server roll-forward, not a completion: the
// live task stays active with its due advanced and a frozen archive copy
// captures the moment. The update is non-optimistic (store.ts) — we await the
// authoritative result to learn the next due and the archive's id. Undo then
// restores the previous due AND deletes that archive, so the completion leaves
// no trace. Its undo shares the single toast-Undo/⌘Z consumption path.
function completeRecurring(store: TaskStore, t: Task): void {
  const prevDue = tsDate(t.dueTime) ?? null;
  void store
    .update(t.id, { completed: true, expectedRevision: t.revision })
    .then((res) => {
      const spawnedId = res?.spawnedOccurrence?.id;
      const nextDue = tsDate(res?.task?.dueTime);
      const entry = pushUndo(`complete “${short(t.title)}”`, () => {
        void store.update(t.id, { due: prevDue }).catch(() => {});
        if (spawnedId) void store.delete(spawnedId).catch(() => {});
      });
      notify.toast({
        kind: "success",
        message: nextDue
          ? `Completed — next ${humanDue(nextDue, new Date()).text}`
          : `Completed “${short(t.title)}”`,
        action: undoAction(entry),
      });
    })
    .catch(() => {
      // store.update already surfaced the failure toast; nothing to undo.
    });
}

export function deleteTaskWithUndo(store: TaskStore, t: Task): void {
  // Capture this task's current children BEFORE deleting: the server re-parents
  // them to top-level (they survive the delete), so undo has to re-nest them
  // under the recreated parent rather than leave them orphaned.
  const childIds = childrenOf(store, t.id);
  store.delete(t.id).catch(() => {});
  // CreateTaskRequest has no `source` field — only UpsertExternalTasks can
  // write a source-owned task (DESIGN §9 invariant #3) — so undo can only
  // ever recreate a synced task as a brand-new LOCAL one: a different id, and
  // (per the sources≠tasks model, DESIGN §5b) immediately visible in
  // Inbox/All, a surface the original never appeared in. If the source
  // re-syncs the same item, that's a visible duplicate. Rather than recreate
  // something misleading, offer no undo for a synced task's delete.
  const synced = t.source !== "";
  if (synced) {
    notify.toast({
      message: `Deleted “${short(t.title)}” — it may return on the next sync.`,
    });
    return;
  }
  const entry = pushUndo(`delete “${short(t.title)}”`, async () => {
    // Reattach to the ORIGINAL parent only if it still exists at undo time;
    // a vanished parent falls back to top-level rather than erroring.
    const parentId = t.parentId && store.getSnapshot().tasks.has(t.parentId) ? t.parentId : undefined;
    const created = await store.create(recreateFields(t, parentId)).catch(() => undefined);
    if (!created) return;
    relinkChildren(store, childIds, created.id);
  });
  notify.toast({
    message: `Deleted “${short(t.title)}”`,
    action: undoAction(entry),
  });
}

export function rescheduleTask(store: TaskStore, t: Task, due: Date | null): void {
  // Due is source-owned on a synced task (like completion) — a UI write would
  // just be reverted by the next full-snapshot sync, so no-op with a notice.
  if (t.source !== "") {
    notify.toast({ kind: "info", message: SYNCED_DUE_MSG });
    return;
  }
  const prev = tsDate(t.dueTime) ?? null;
  store.update(t.id, { due, expectedRevision: t.revision }).catch(() => {});
  const entry = pushUndo(
    due ? `reschedule “${short(t.title)}”` : `clear date on “${short(t.title)}”`,
    () => void store.update(t.id, { due: prev }).catch(() => {}),
  );
  notify.toast({
    message: due ? `Scheduled “${short(t.title)}”` : `Cleared date on “${short(t.title)}”`,
    action: undoAction(entry),
  });
}

/** Sets/replaces the single "priority" label (p1-3), or clears it. Undoable via
 *  the toast's Undo AND ⌘Z (shared entry), restoring the exact prior labels —
 *  the same fidelity a DetailPanel label edit gets. */
export function setPriority(store: TaskStore, t: Task, priority: string | null): void {
  const prev = t.labels;
  const labels = t.labels.filter((l) => !/^p[1-3]$/.test(l));
  if (priority) labels.push(priority);
  store.update(t.id, { labels, expectedRevision: t.revision }).catch(() => {});
  const entry = pushUndo(`priority on “${short(t.title)}”`, () =>
    void store.update(t.id, { labels: prev }).catch(() => {}),
  );
  notify.toast({
    message: priority ? `Priority ${priority}` : `Priority cleared`,
    action: undoAction(entry),
  });
}

export function addLabel(store: TaskStore, t: Task, label: string): void {
  if (t.labels.includes(label)) return;
  const prev = t.labels;
  store.update(t.id, { labels: [...t.labels, label], expectedRevision: t.revision }).catch(() => {});
  const entry = pushUndo(`label “${short(t.title)}”`, () =>
    void store.update(t.id, { labels: prev }).catch(() => {}),
  );
  notify.toast({ message: `Added ${label}`, action: undoAction(entry) });
}

/** Timeboxes a task for today without dragging: writes user_data.timebox at
 *  the next round half-hour for 60 min. Merges the freshest user_data and
 *  writes WITHOUT expectedRevision — user_data is single-writer, so
 *  last-write-wins avoids 409s against the watch echo (same rule the gcal rail
 *  uses). The calendar rail renders the block. */
export function planToday(store: TaskStore, t: Task, now: Date): void {
  const iv = planTodayInterval(now);
  const prev = t.userData ?? null;
  const userData: JsonObject = {
    ...(t.userData ?? {}),
    timebox: { start: iv.start.toISOString(), end: iv.end.toISOString() },
  };
  store.update(t.id, { userData }).catch(() => {});
  // Undo restores the exact prior user_data (timebox and all), reachable via
  // both the toast's Undo and ⌘Z through one shared, single-consumption entry.
  const entry = pushUndo(`plan “${short(t.title)}”`, () =>
    void store.update(t.id, { userData: prev }).catch(() => {}),
  );
  notify.toast({
    kind: "success",
    message: `Planned “${short(t.title)}” for today`,
    action: undoAction(entry),
  });
}

/** Sets/replaces the single "project:" label, or clears it (move to Inbox).
 *  Undoable via the toast's Undo AND ⌘Z, restoring the exact prior labels. */
export function setProject(store: TaskStore, t: Task, project: string | null): void {
  const prev = t.labels;
  const labels = t.labels.filter((l) => !l.startsWith("project:"));
  if (project) labels.push(project);
  store.update(t.id, { labels, expectedRevision: t.revision }).catch(() => {});
  const entry = pushUndo(`move “${short(t.title)}”`, () =>
    void store.update(t.id, { labels: prev }).catch(() => {}),
  );
  notify.toast({
    message: project ? `Moved to ${chipParts(project).val}` : `Moved to Inbox`,
    action: undoAction(entry),
  });
}

// --- bulk operations (one summarizing toast for the whole batch) ---

const plural = (n: number) => `${n} task${n === 1 ? "" : "s"}`;

export function completeMany(store: TaskStore, tasks: Task[]): void {
  // Complete only local tasks; synced ones can't be completed client-side (the
  // next sync reverts them), so they're skipped and called out in the toast.
  const local = tasks.filter((t) => t.source === "");
  const skipped = tasks.length - local.length;
  if (local.length === 0) {
    notify.toast({ kind: "info", message: `${SYNCED_COMPLETE_MSG} — ${plural(skipped)} skipped` });
    return;
  }
  // Recurring members can't be completed as a batch (each rolls forward and
  // archives independently), so route them through the same per-task path —
  // each shows its own "next …" toast + undo. Only one-off tasks share the
  // single summarizing toast below.
  const recurring = local.filter((t) => t.recurrence !== "");
  const oneOff = local.filter((t) => t.recurrence === "");
  for (const t of recurring) completeRecurring(store, t);
  if (oneOff.length === 0) {
    // No one-off batch toast to carry the skipped-count suffix — but the synced
    // members were still skipped, so emit a standalone notice regardless of the
    // recurring/one-off split (the recurring members each toast on their own).
    if (skipped) notify.toast({ kind: "info", message: `${skipped} synced skipped` });
    return;
  }
  for (const t of oneOff) store.update(t.id, { completed: true, expectedRevision: t.revision }).catch(() => {});
  const entry = pushUndo(`complete ${plural(oneOff.length)}`, () =>
    oneOff.forEach((t) => void store.update(t.id, { completed: false }).catch(() => {})),
  );
  notify.toast({
    kind: "success",
    message: skipped
      ? `Completed ${plural(oneOff.length)} · ${skipped} synced skipped`
      : `Completed ${plural(oneOff.length)}`,
    action: undoAction(entry),
  });
}

export function deleteMany(store: TaskStore, tasks: Task[]): void {
  const deletedIds = new Set(tasks.map((t) => t.id));
  // Per deleted task, capture the children that will SURVIVE (i.e. aren't
  // themselves in this batch) BEFORE deleting — the server re-parents them to
  // top-level, and undo has to re-nest them under the rebuilt parent.
  const survivingChildren = new Map<string, string[]>();
  for (const t of tasks) {
    const kids = childrenOf(store, t.id).filter((cid) => !deletedIds.has(cid));
    if (kids.length) survivingChildren.set(t.id, kids);
  }
  for (const t of tasks) store.delete(t.id).catch(() => {});
  // Only local tasks can be safely recreated (see deleteTaskWithUndo) — any
  // synced task in the selection just won't come back via undo.
  const locals = tasks.filter((t) => t.source === "");
  const entry = pushUndo(`delete ${plural(tasks.length)}`, async () => {
    const idMap = new Map<string, string>(); // old id → rebuilt id
    // Pass 1: top-level tasks and those whose parent survived (wasn't in the
    // batch) — rebuild first, recording old→new so pass 2 can wire up the rest.
    const snap = store.getSnapshot();
    const deferred: Task[] = [];
    for (const t of locals) {
      if (t.parentId !== "" && deletedIds.has(t.parentId)) {
        deferred.push(t);
        continue;
      }
      const parentId = t.parentId && snap.tasks.has(t.parentId) ? t.parentId : undefined;
      const created = await store.create(recreateFields(t, parentId)).catch(() => undefined);
      if (created) idMap.set(t.id, created.id);
    }
    // Pass 2: tasks whose parent was ALSO deleted — re-parent to the parent's
    // rebuilt id (or top-level if the parent couldn't be rebuilt, e.g. synced).
    for (const t of deferred) {
      const created = await store.create(recreateFields(t, idMap.get(t.parentId))).catch(() => undefined);
      if (created) idMap.set(t.id, created.id);
    }
    // Finally, re-nest surviving children of deleted parents under the new id.
    for (const [oldParentId, kids] of survivingChildren) {
      const newParentId = idMap.get(oldParentId);
      if (newParentId) relinkChildren(store, kids, newParentId);
    }
  });
  notify.toast({
    message: `Deleted ${plural(tasks.length)}`,
    action: undoAction(entry),
  });
}

export function rescheduleMany(store: TaskStore, tasks: Task[], due: Date | null): void {
  // Due is source-owned on synced tasks — reschedule only local members and
  // call out any synced ones that were skipped (mirrors completeMany).
  const local = tasks.filter((t) => t.source === "");
  const skipped = tasks.length - local.length;
  if (local.length === 0) {
    notify.toast({ kind: "info", message: `${SYNCED_DUE_MSG} — ${plural(skipped)} skipped` });
    return;
  }
  const prev = local.map((t) => ({ id: t.id, due: tsDate(t.dueTime) ?? null }));
  for (const t of local) store.update(t.id, { due, expectedRevision: t.revision }).catch(() => {});
  const entry = pushUndo(
    due ? `reschedule ${plural(local.length)}` : `clear date on ${plural(local.length)}`,
    () => prev.forEach((p) => void store.update(p.id, { due: p.due }).catch(() => {})),
  );
  const base = due ? `Scheduled ${plural(local.length)}` : `Cleared date on ${plural(local.length)}`;
  notify.toast({
    // Keep the skipped-count notice even when every local member was scheduled.
    message: skipped ? `${base} · ${skipped} synced skipped` : base,
    action: undoAction(entry),
  });
}

export function addLabelMany(store: TaskStore, tasks: Task[], label: string): void {
  // Batch the label onto every task missing it in ONE undoable step (each undo
  // restores that task's exact prior labels) — not by delegating to addLabel,
  // which would fire a toast + undo entry per task.
  const targets = tasks.filter((t) => !t.labels.includes(label));
  const prev = targets.map((t) => ({ id: t.id, labels: t.labels }));
  for (const t of targets) {
    store.update(t.id, { labels: [...t.labels, label], expectedRevision: t.revision }).catch(() => {});
  }
  const entry = pushUndo(`label ${plural(tasks.length)}`, () =>
    prev.forEach((p) => void store.update(p.id, { labels: p.labels }).catch(() => {})),
  );
  notify.toast({ message: `Labeled ${plural(tasks.length)} “${label}”`, action: undoAction(entry) });
}
