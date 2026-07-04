import type { Task } from "../gen/task/task_pb";
import type { TaskStore } from "./store";
import { notify } from "./notify";
import { tsDate } from "./format";

// Undoable task operations, shared by the command palette, row hover actions,
// bulk bar, and keyboard. Each performs the change optimistically and shows a
// toast with Undo. Undo uses last-write-wins (expectedRevision 0) since the
// task's revision has moved on by the time the user clicks.

const short = (s: string) => (s.length > 32 ? s.slice(0, 31) + "…" : s);

export function completeTask(store: TaskStore, t: Task): void {
  store.update(t.id, { completed: true, expectedRevision: t.revision }).catch(() => {});
  notify.toast({
    kind: "success",
    message: `Completed “${short(t.title)}”`,
    action: { label: "Undo", onClick: () => void store.update(t.id, { completed: false }).catch(() => {}) },
  });
}

export function deleteTaskWithUndo(store: TaskStore, t: Task): void {
  store.delete(t.id).catch(() => {});
  notify.toast({
    message: `Deleted “${short(t.title)}”`,
    action: {
      label: "Undo",
      // Delete is permanent server-side, so undo recreates (new id;
      // best-effort — a synced task would also just re-sync).
      onClick: () =>
        void store
          .create({ title: t.title, notes: t.notes, labels: t.labels, due: tsDate(t.dueTime) })
          .catch(() => {}),
    },
  });
}

export function rescheduleTask(store: TaskStore, t: Task, due: Date | null): void {
  const prev = tsDate(t.dueTime) ?? null;
  store.update(t.id, { due, expectedRevision: t.revision }).catch(() => {});
  notify.toast({
    message: due ? `Scheduled “${short(t.title)}”` : `Cleared date on “${short(t.title)}”`,
    action: { label: "Undo", onClick: () => void store.update(t.id, { due: prev }).catch(() => {}) },
  });
}

/** Sets/replaces the single "priority" label (p1-3), or clears it. No toast —
 *  it's a light, reversible edit usually done inline. */
export function setPriority(store: TaskStore, t: Task, priority: string | null): void {
  const labels = t.labels.filter((l) => !/^p[1-3]$/.test(l));
  if (priority) labels.push(priority);
  store.update(t.id, { labels, expectedRevision: t.revision }).catch(() => {});
}

export function addLabel(store: TaskStore, t: Task, label: string): void {
  if (t.labels.includes(label)) return;
  store.update(t.id, { labels: [...t.labels, label], expectedRevision: t.revision }).catch(() => {});
}

/** Sets/replaces the single "project:" label, or clears it (move to Inbox). */
export function setProject(store: TaskStore, t: Task, project: string | null): void {
  const labels = t.labels.filter((l) => !l.startsWith("project:"));
  if (project) labels.push(project);
  store.update(t.id, { labels, expectedRevision: t.revision }).catch(() => {});
}

// --- bulk operations (one summarizing toast for the whole batch) ---

const plural = (n: number) => `${n} task${n === 1 ? "" : "s"}`;

export function completeMany(store: TaskStore, tasks: Task[]): void {
  for (const t of tasks) store.update(t.id, { completed: true, expectedRevision: t.revision }).catch(() => {});
  notify.toast({
    kind: "success",
    message: `Completed ${plural(tasks.length)}`,
    action: {
      label: "Undo",
      onClick: () => tasks.forEach((t) => void store.update(t.id, { completed: false }).catch(() => {})),
    },
  });
}

export function deleteMany(store: TaskStore, tasks: Task[]): void {
  for (const t of tasks) store.delete(t.id).catch(() => {});
  notify.toast({
    message: `Deleted ${plural(tasks.length)}`,
    action: {
      label: "Undo",
      onClick: () =>
        tasks.forEach(
          (t) =>
            void store
              .create({ title: t.title, notes: t.notes, labels: t.labels, due: tsDate(t.dueTime) })
              .catch(() => {}),
        ),
    },
  });
}

export function rescheduleMany(store: TaskStore, tasks: Task[], due: Date | null): void {
  const prev = tasks.map((t) => ({ id: t.id, due: tsDate(t.dueTime) ?? null }));
  for (const t of tasks) store.update(t.id, { due, expectedRevision: t.revision }).catch(() => {});
  notify.toast({
    message: due ? `Scheduled ${plural(tasks.length)}` : `Cleared date on ${plural(tasks.length)}`,
    action: {
      label: "Undo",
      onClick: () => prev.forEach((p) => void store.update(p.id, { due: p.due }).catch(() => {})),
    },
  });
}

export function addLabelMany(store: TaskStore, tasks: Task[], label: string): void {
  for (const t of tasks) if (!t.labels.includes(label)) addLabel(store, t, label);
  notify.toast({ message: `Labeled ${plural(tasks.length)} “${label}”` });
}
