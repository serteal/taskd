import { test as base, expect } from "@playwright/test";
import path from "node:path";
import {
  startDaemon,
  waitForSources,
  FIXED_NOW,
  daysFromNow,
  ROOT,
  type Api,
  type Daemon,
  type Task,
  type ExtensionEntry,
} from "../testkit/daemon";
import { gotoApp } from "../testkit/e2e";

// Core's own fixtures: a `mode` toggle between a clean daemon and one with the
// in-tree mock extensions (gcal/github) + the hand-written testext fixture
// staged. Built on the shared test kit (web/testkit) — the same primitives an
// extension uses via defineExtensionE2E.

const TESTEXT: ExtensionEntry = {
  name: "testext",
  dir: path.join(ROOT, "web", "e2e", "fixtures", "testext"),
};
const WITH_EXTENSIONS: ExtensionEntry[] = ["gcal", "github", TESTEXT];

export type DaemonMode = "clean" | "extensions";

export const test = base.extend<{
  mode: DaemonMode;
  daemon: Daemon;
  api: Api;
  consoleErrors: string[];
  expectNoConsoleErrors: boolean;
}>({
  mode: ["clean", { option: true }],
  expectNoConsoleErrors: [true, { option: true }],

  daemon: async ({ mode }, use) => {
    const extensions = mode === "extensions" ? WITH_EXTENSIONS : [];
    const waitFor = mode === "extensions" ? waitForSources("github", "gcal") : undefined;
    const d = await startDaemon(extensions, waitFor);
    await use(d);
    await d.stop();
  },

  api: async ({ daemon }, use) => {
    await use(daemon.api);
  },

  consoleErrors: async ({}, use) => {
    await use([]);
  },

  page: async ({ page, daemon, consoleErrors, expectNoConsoleErrors }, use) => {
    await gotoApp(page, daemon.baseURL, consoleErrors);
    await use(page);
    if (expectNoConsoleErrors) {
      expect(consoleErrors, `console errors:\n${consoleErrors.join("\n")}`).toHaveLength(0);
    }
  },
});

export { expect, FIXED_NOW, daysFromNow };
export type { Api, Task };
