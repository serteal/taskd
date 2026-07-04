// Unit-test kit for extension web logic. An extension unit-tests the parts of
// its `register(api)` that don't render React — presenter `match`/`rowMeta`,
// command `run` side effects, quick-add token `match` — against a spy `api`:
//
//   import { mockApi, makeTask } from "<path-to>/web/testkit/unit";
//   const api = mockApi();
//   extension.register(api);
//   const p = api.registered.presenters[0];
//   expect(p.match(makeTask({ source: "myext" }))).toBe(true);
//   expect(p.rowMeta(makeTask(...))).toMatchObject({ subtitle: "…" });
//
// No @playwright/test import here, so it is safe to use from vitest.

import type { Task } from "../src/gen/task/task_pb";

export type { Task };

/** A minimal, valid-enough Task for pure-logic tests. */
export function makeTask(partial: Partial<Task> = {}): Task {
  return {
    id: "t",
    title: "",
    notes: "",
    labels: [],
    source: "",
    externalRef: "",
    revision: 0n,
    ...partial,
  } as unknown as Task;
}

/** What an icon(name) call resolves to in unit tests — an inert marker instead
 *  of a React element, so no renderer is needed. */
export interface IconMarker {
  __icon: string;
}

interface Spy {
  (...args: unknown[]): unknown;
  calls: unknown[][];
}

function spy(impl: (...args: unknown[]) => unknown = () => undefined): Spy {
  const fn = ((...args: unknown[]) => {
    fn.calls.push(args);
    return impl(...args);
  }) as Spy;
  fn.calls = [];
  return fn;
}

// What register(api) hands back to us, captured for assertions.
export interface Registered {
  presenters: any[];
  views: any[];
  panels: any[];
  commands: any[];
  quickAddTokens: any[];
}

export interface MockApi {
  registered: Registered;
  registerPresenter: Spy;
  registerView: Spy;
  registerPanel: Spy;
  registerCommand: Spy;
  registerQuickAddToken: Spy;
  icon: (name: string, opts?: unknown) => IconMarker;
  notify: { toast: Spy; error: Spy; browser: Spy };
  store: { update: Spy; create: Spy; delete: Spy };
  ui: { openTask: Spy };
  getTasks: () => Task[];
  hooks: { useTasks: () => Task[]; useNow: () => Date };
  client: unknown;
  dnd: { setTaskDrag: Spy; readTaskId: Spy };
  format: { humanDue: Spy };
}

/** A spy ExtensionAPI. Pass `{ tasks, now }` to control what the hooks read. */
export function mockApi(opts: { tasks?: Task[]; now?: Date } = {}): MockApi {
  const tasks = opts.tasks ?? [];
  const now = opts.now ?? new Date("2026-07-06T12:00:00Z");
  const registered: Registered = {
    presenters: [],
    views: [],
    panels: [],
    commands: [],
    quickAddTokens: [],
  };
  return {
    registered,
    registerPresenter: spy((p) => registered.presenters.push(p)),
    registerView: spy((v) => registered.views.push(v)),
    registerPanel: spy((p) => registered.panels.push(p)),
    registerCommand: spy((c) => registered.commands.push(c)),
    registerQuickAddToken: spy((t) => registered.quickAddTokens.push(t)),
    icon: (name) => ({ __icon: name }),
    notify: { toast: spy(), error: spy(), browser: spy() },
    store: { update: spy(() => Promise.resolve()), create: spy(() => Promise.resolve()), delete: spy(() => Promise.resolve()) },
    ui: { openTask: spy() },
    getTasks: () => tasks,
    hooks: { useTasks: () => tasks, useNow: () => now },
    client: {},
    dnd: { setTaskDrag: spy(), readTaskId: spy(() => null) },
    format: { humanDue: spy(() => ({ text: "", tone: "later" })) },
  };
}
