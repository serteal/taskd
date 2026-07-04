// Test-kit daemon primitives: spawn a real `taskd` (webui build) with a chosen
// set of extensions staged into a temp data dir, and talk to it over the
// Connect JSON API. Runner-agnostic (no @playwright/test import) so both the
// e2e fixtures and any ad-hoc script can use it.

import { spawn, type ChildProcess } from "node:child_process";
import { cp, mkdir, mkdtemp, rm } from "node:fs/promises";
import { existsSync } from "node:fs";
import net from "node:net";
import os from "node:os";
import path from "node:path";

// Find the repo root by walking up to go.mod. This avoids import.meta/__dirname
// (the kit is loaded from both web/ specs, which resolve as ESM, and extension
// specs outside any package.json scope, which Playwright treats as CJS).
function findRepoRoot(): string {
  let dir = process.cwd();
  for (let i = 0; i < 12; i++) {
    if (existsSync(path.join(dir, "go.mod"))) return dir;
    const up = path.dirname(dir);
    if (up === dir) break;
    dir = up;
  }
  throw new Error(`test kit: repo root (go.mod) not found from ${process.cwd()}`);
}

export const ROOT = findRepoRoot();
export const WEB = path.join(ROOT, "web");
const TASKD = path.join(ROOT, "taskd");

// A fixed "now" for the browser clock — Monday mid-day UTC, the same calendar
// day across common timezones. Seed due dates relative to this.
export const FIXED_NOW = new Date("2026-07-06T12:00:00.000Z");

export function daysFromNow(n: number): Date {
  return new Date(FIXED_NOW.getTime() + n * 86_400_000);
}

// --- API client (Connect JSON) ---------------------------------------------

export interface Task {
  id: string;
  title: string;
  notes: string;
  labels: string[];
  dueTime?: string;
  completedTime?: string;
  source: string;
  externalRef: string;
  externalData?: Record<string, unknown>;
  userData?: Record<string, unknown>;
  revision: string;
}

export interface ExternalTaskInit {
  externalRef: string;
  title: string;
  dueTime?: string;
  completedTime?: string;
  externalData?: Record<string, unknown>;
}

export interface Api {
  createTask(t: { title: string; notes?: string; labels?: string[]; due?: Date }): Promise<Task>;
  upsertExternal(
    source: string,
    tasks: ExternalTaskInit[],
    opts?: { applyLabels?: string[]; fullSnapshot?: boolean },
  ): Promise<void>;
  listActive(): Promise<Task[]>;
  listAll(): Promise<Task[]>;
  getTask(id: string): Promise<Task>;
  updateTask(id: string, maskPaths: string[], task: Partial<Task>, expectedRevision?: number): Promise<Task>;
  deleteTask(id: string): Promise<void>;
}

export function makeApi(baseURL: string): Api {
  const call = async (method: string, body: unknown): Promise<any> => {
    const res = await fetch(`${baseURL}/task.TaskService/${method}`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!res.ok) throw new Error(`${method} ${res.status}: ${await res.text()}`);
    return res.json();
  };
  return {
    createTask: (t) =>
      call("CreateTask", {
        title: t.title,
        notes: t.notes,
        labels: t.labels,
        dueTime: t.due?.toISOString(),
      }).then((r) => r.task),
    upsertExternal: (source, tasks, opts) =>
      call("UpsertExternalTasks", {
        source,
        tasks,
        applyLabels: opts?.applyLabels,
        fullSnapshot: opts?.fullSnapshot ?? true,
      }).then(() => undefined),
    listActive: () =>
      call("ListTasks", { filter: { completed: false }, pageSize: 1000 }).then((r) => r.tasks ?? []),
    listAll: () => call("ListTasks", { pageSize: 1000 }).then((r) => r.tasks ?? []),
    getTask: (id) => call("GetTask", { id }).then((r) => r.task),
    updateTask: (id, maskPaths, task, expectedRevision) =>
      call("UpdateTask", {
        id,
        updateMask: maskPaths.join(","),
        task,
        expectedRevision,
      }).then((r) => r.task),
    deleteTask: (id) => call("DeleteTask", { id }).then(() => undefined),
  };
}

// --- extension staging -----------------------------------------------------

/** An extension to stage: an in-tree name ("gcal"/"github"/"ics") resolved to
 *  ROOT/extensions/<name>, or an explicit { name, dir } for anything else
 *  (a test fixture, or an out-of-tree extension under development). */
export type ExtensionEntry = string | { name: string; dir: string };

export function resolveExtension(entry: ExtensionEntry): { name: string; dir: string } {
  if (typeof entry === "string") return { name: entry, dir: path.join(ROOT, "extensions", entry) };
  return entry;
}

// Copy the runtime bits the daemon needs — manifest, web bundle, and the
// syncer binary if the manifest declares one — into <dataDir>/extensions/<name>.
async function stageExtension(dataDir: string, entry: ExtensionEntry): Promise<void> {
  const { name, dir } = resolveExtension(entry);
  const dst = path.join(dataDir, "extensions", name);
  await mkdir(dst, { recursive: true });
  await cp(path.join(dir, "manifest.json"), path.join(dst, "manifest.json"));
  if (existsSync(path.join(dir, "web"))) {
    await cp(path.join(dir, "web"), path.join(dst, "web"), { recursive: true });
  }
  const bin = path.join(dir, `task-sync-${name}`);
  if (existsSync(bin)) await cp(bin, path.join(dst, `task-sync-${name}`));
}

// --- daemon lifecycle ------------------------------------------------------

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const srv = net.createServer();
    srv.unref();
    srv.on("error", reject);
    srv.listen(0, "127.0.0.1", () => {
      const port = (srv.address() as net.AddressInfo).port;
      srv.close(() => resolve(port));
    });
  });
}

async function waitHealthy(baseURL: string, tries = 100): Promise<void> {
  for (let i = 0; i < tries; i++) {
    try {
      if ((await fetch(`${baseURL}/healthz`)).ok) return;
    } catch {
      /* not up yet */
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`daemon at ${baseURL} never became healthy`);
}

export interface Daemon {
  baseURL: string;
  dir: string;
  api: Api;
  stop: () => Promise<void>;
}

/** Start a fresh taskd with `extensions` staged. `waitFor`, if given, is polled
 *  until it resolves (e.g. wait for a syncer's first data). */
export async function startDaemon(
  extensions: ExtensionEntry[] = [],
  waitFor?: (api: Api) => Promise<void>,
): Promise<Daemon> {
  const dir = await mkdtemp(path.join(os.tmpdir(), "taskd-e2e-"));
  for (const e of extensions) await stageExtension(dir, e);

  const port = await freePort();
  let stderr = "";
  const proc: ChildProcess = spawn(TASKD, ["-dir", dir, "-listen", `127.0.0.1:${port}`], {
    stdio: ["ignore", "ignore", "pipe"],
  });
  proc.stderr?.on("data", (d) => (stderr += d.toString()));

  const baseURL = `http://127.0.0.1:${port}`;
  const api = makeApi(baseURL);
  try {
    await waitHealthy(baseURL);
    if (waitFor) await waitFor(api);
  } catch (err) {
    proc.kill("SIGKILL");
    throw new Error(`${(err as Error).message}\n--- taskd stderr ---\n${stderr}`);
  }

  const stop = async () => {
    proc.kill("SIGTERM");
    await new Promise((r) => setTimeout(r, 150));
    await rm(dir, { recursive: true, force: true });
  };
  return { baseURL, dir, api, stop };
}

/** Poll until every named source has streamed at least one task. Handy as a
 *  `waitFor` for extensions whose syncer mocks data on startup. */
export function waitForSources(...sources: string[]): (api: Api) => Promise<void> {
  return async (api) => {
    for (let i = 0; i < 120; i++) {
      const all = await api.listAll();
      if (sources.every((s) => all.some((t) => t.source === s || t.source.startsWith(s)))) return;
      await new Promise((r) => setTimeout(r, 100));
    }
    throw new Error(`sources never synced: ${sources.join(", ")}`);
  };
}
