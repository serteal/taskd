import type { JsonObject } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { Code, ConnectError } from "@connectrpc/connect";
import { notify } from "./notify";
import type { Task, WatchTasksResponse } from "../gen/task/task_pb";
import type { TaskClient } from "./client";

// TaskStore is a live replica of the ACTIVE task set, maintained with the
// protocol the API documents: open WatchTasks, await the empty handshake,
// snapshot via ListTasks, then apply events using revision to discard
// anything older than what we hold. Completed tasks leave the replica (the
// Completed view pages the server directly). Any stream failure means
// reconnect-and-refetch — there is no cursor state to get wrong.
//
// Mutations are optimistic: the expected result lands in the replica
// immediately, and the authoritative response/watch event (with the real
// revision) overwrites it. Recently self-mutated ids are remembered so the
// echo of our own write doesn't render as an external change ("pulse").

export interface Snapshot {
  readonly tasks: ReadonlyMap<string, Task>;
  readonly connected: boolean;
  /** True from the first successful handshake onward (never resets). Lets the
   *  UI tell a real drop (everConnected && !connected) apart from the normal
   *  not-yet-connected moments of a cold load. */
  readonly everConnected: boolean;
  /** id → time of the last change that arrived from OUTSIDE this client. */
  readonly pulses: ReadonlyMap<string, number>;
  readonly generation: number;
}

export interface TaskPatch {
  title?: string;
  notes?: string;
  labels?: string[];
  /** undefined = untouched; null = clear the due date. */
  due?: Date | null;
  completed?: boolean;
  /**
   * Whole-field replace of the task's user_data (undefined = untouched;
   * null = clear). Merge by spreading the current value:
   * `{ ...task.userData, timebox }`.
   */
  userData?: JsonObject | null;
  /** Canonical RRULE subset; undefined = untouched, null or "" = stop recurring. */
  recurrence?: string | null;
  /** Parent task id; undefined = untouched, null or "" = detach to top-level. */
  parentId?: string | null;
  expectedRevision?: bigint;
}

/** UpdateTask's authoritative result. `spawnedOccurrence` is set only when
 *  completing a recurring task rolled the series forward — the frozen archive
 *  copy, so callers can undo the completion precisely. */
export interface UpdateResult {
  task?: Task;
  spawnedOccurrence?: Task;
}

const ECHO_MS = 4000;
const PULSE_MS = 2500;

export class TaskStore {
  private tasks = new Map<string, Task>();
  private pulses = new Map<string, number>();
  private echoes = new Map<string, number>();
  private listeners = new Set<() => void>();
  private connected = false;
  private everConnected = false;
  private generation = 0;
  private snap: Snapshot;

  constructor(
    readonly client: TaskClient,
    private opts: { minBackoffMs?: number; maxBackoffMs?: number; now?: () => number } = {},
  ) {
    this.snap = this.buildSnapshot();
  }

  subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => this.listeners.delete(fn);
  };

  getSnapshot = (): Snapshot => this.snap;

  /** Runs the watch → snapshot → apply loop until the signal aborts. */
  async start(signal: AbortSignal): Promise<void> {
    const min = this.opts.minBackoffMs ?? 500;
    const max = this.opts.maxBackoffMs ?? 5000;
    let delay = min;
    while (!signal.aborted) {
      try {
        let sawHandshake = false;
        for await (const msg of this.client.watchTasks({}, { signal })) {
          if (!sawHandshake) {
            sawHandshake = true; // subscription live; snapshot can't miss changes
            await this.refetchAll(signal);
            this.setConnected(true);
            delay = min;
            continue;
          }
          this.applyEvent(msg);
        }
      } catch {
        // fall through to reconnect
      }
      if (signal.aborted) return;
      this.setConnected(false);
      await new Promise((r) => setTimeout(r, delay));
      delay = Math.min(delay * 2, max);
    }
  }

  // --- mutations ---------------------------------------------------------

  async create(fields: {
    title: string;
    notes?: string;
    labels?: string[];
    due?: Date;
    recurrence?: string;
    parentId?: string;
  }): Promise<Task> {
    const res = await this.client.createTask({
      title: fields.title,
      notes: fields.notes ?? "",
      labels: fields.labels ?? [],
      dueTime: fields.due ? timestampFromDate(fields.due) : undefined,
      recurrence: fields.recurrence ?? "",
      parentId: fields.parentId ?? "",
    });
    const task = res.task!;
    this.markEcho(task.id);
    this.applyAuthoritative(task);
    return task;
  }

  async update(id: string, patch: TaskPatch): Promise<UpdateResult> {
    const paths: string[] = [];
    const init: Record<string, unknown> = {};
    if (patch.title !== undefined) {
      paths.push("title");
      init.title = patch.title;
    }
    if (patch.notes !== undefined) {
      paths.push("notes");
      init.notes = patch.notes;
    }
    if (patch.labels !== undefined) {
      paths.push("labels");
      init.labels = patch.labels;
    }
    if (patch.due !== undefined) {
      paths.push("due_time");
      init.dueTime = patch.due ? timestampFromDate(patch.due) : undefined;
    }
    if (patch.completed !== undefined) {
      paths.push("completed_time");
      init.completedTime = patch.completed ? timestampFromDate(new Date()) : undefined;
    }
    if (patch.userData !== undefined) {
      paths.push("user_data");
      init.userData = patch.userData ?? undefined;
    }
    if (patch.recurrence !== undefined) {
      paths.push("recurrence");
      init.recurrence = patch.recurrence ?? "";
    }
    if (patch.parentId !== undefined) {
      paths.push("parent_id");
      init.parentId = patch.parentId ?? "";
    }
    if (paths.length === 0) return {};

    const cur = this.tasks.get(id);
    // Completing a RECURRING task is NOT optimistic: the client can't compute
    // the next due, and the server keeps the task ACTIVE (rolling it forward)
    // rather than removing it. Skip the optimistic apply and let the awaited
    // response land the advanced live task; the watch echo dedup handles the
    // rest.
    const recurringComplete = patch.completed === true && cur !== undefined && cur.recurrence !== "";

    // Optimistic application; the response or a refetch will correct it.
    if (cur && !recurringComplete) {
      this.markEcho(id);
      const opt: Task = {
        ...cur,
        ...(patch.title !== undefined ? { title: patch.title } : null),
        ...(patch.notes !== undefined ? { notes: patch.notes } : null),
        ...(patch.labels !== undefined ? { labels: patch.labels } : null),
        ...(patch.due !== undefined
          ? { dueTime: patch.due ? timestampFromDate(patch.due) : undefined }
          : null),
        ...(patch.completed !== undefined
          ? { completedTime: patch.completed ? timestampFromDate(new Date()) : undefined }
          : null),
        ...(patch.userData !== undefined ? { userData: patch.userData ?? undefined } : null),
        ...(patch.recurrence !== undefined ? { recurrence: patch.recurrence ?? "" } : null),
        ...(patch.parentId !== undefined ? { parentId: patch.parentId ?? "" } : null),
      };
      this.place(opt);
      this.emit();
    }

    try {
      const res = await this.client.updateTask({
        id,
        task: init,
        updateMask: { paths },
        expectedRevision: patch.expectedRevision ?? 0n,
      });
      this.markEcho(id);
      this.applyAuthoritative(res.task!);
      return { task: res.task, spawnedOccurrence: res.spawnedOccurrence };
    } catch (err) {
      await this.resync(id);
      this.notifyError(err);
      throw err;
    }
  }

  /** The Completed view pages the server; the archive is not replicated.
   *  Ordered by completion time, not last-updated — editing a field on an
   *  already-completed task (title, notes, due) shouldn't resurface it above
   *  one completed more recently. */
  async listCompleted(pageToken = ""): Promise<{ tasks: Task[]; next: string }> {
    const res = await this.client.listTasks({
      filter: { completed: true },
      orderBy: "completed desc",
      pageSize: 50,
      pageToken,
    });
    return { tasks: res.tasks, next: res.nextPageToken };
  }

  async delete(id: string): Promise<void> {
    this.markEcho(id);
    const had = this.tasks.delete(id);
    if (had) this.emit();
    try {
      await this.client.deleteTask({ id });
    } catch (err) {
      await this.resync(id);
      this.notifyError(err);
      throw err;
    }
  }

  private notifyError(err: unknown): void {
    const cerr = ConnectError.from(err);
    if (cerr.code === Code.Aborted) {
      notify.toast({ kind: "info", message: "Task changed elsewhere — showing the latest version." });
    } else {
      notify.error(`Couldn't save: ${cerr.rawMessage}`);
    }
  }

  // --- internals ---------------------------------------------------------

  private async refetchAll(signal: AbortSignal): Promise<void> {
    const next = new Map<string, Task>();
    let pageToken = "";
    do {
      const res = await this.client.listTasks(
        { filter: { completed: false }, pageSize: 1000, pageToken },
        { signal },
      );
      for (const t of res.tasks) next.set(t.id, t);
      pageToken = res.nextPageToken;
    } while (pageToken !== "");
    this.tasks = next;
    this.emit();
  }

  /** Re-reads one task after a failed mutation (rollback by truth). */
  private async resync(id: string): Promise<void> {
    try {
      const res = await this.client.getTask({ id });
      this.markEcho(id);
      const t = res.task!;
      this.place(t);
    } catch {
      this.tasks.delete(id); // gone on the server too
    }
    this.emit();
  }

  private applyEvent(msg: WatchTasksResponse): void {
    switch (msg.change.case) {
      case "task": {
        const t = msg.change.value;
        const external = !this.isEcho(t.id);
        if (!this.applyAuthoritative(t, { emit: false })) return;
        if (external) this.pulses.set(t.id, this.now());
        this.emit();
        return;
      }
      case "deletedTaskId": {
        if (this.tasks.delete(msg.change.value)) this.emit();
        return;
      }
      default:
        return; // handshake or a change kind newer than this client
    }
  }

  /**
   * Applies a server-confirmed task state, respecting the revision order.
   * Returns false when the replica already holds something newer.
   */
  private applyAuthoritative(t: Task, o: { emit: boolean } = { emit: true }): boolean {
    const cur = this.tasks.get(t.id);
    if (cur && cur.revision >= t.revision) return false;
    this.place(t);
    if (o.emit) this.emit();
    return true;
  }

  /** Inserts an active task, or removes it if it is completed. */
  private place(t: Task): void {
    if (t.completedTime) this.tasks.delete(t.id);
    else this.tasks.set(t.id, t);
  }

  private markEcho(id: string): void {
    this.echoes.set(id, this.now() + ECHO_MS);
  }

  private isEcho(id: string): boolean {
    const until = this.echoes.get(id);
    return until !== undefined && this.now() < until;
  }

  private setConnected(v: boolean): void {
    if (this.connected === v) return;
    this.connected = v;
    if (v) this.everConnected = true;
    this.emit();
  }

  private now(): number {
    return this.opts.now ? this.opts.now() : Date.now();
  }

  private emit(): void {
    const now = this.now();
    for (const [id, at] of this.pulses) {
      if (now - at > PULSE_MS) this.pulses.delete(id);
    }
    this.generation++;
    this.snap = this.buildSnapshot();
    for (const fn of this.listeners) fn();
  }

  private buildSnapshot(): Snapshot {
    return {
      tasks: this.tasks,
      connected: this.connected,
      everConnected: this.everConnected,
      pulses: this.pulses,
      generation: this.generation,
    };
  }
}
