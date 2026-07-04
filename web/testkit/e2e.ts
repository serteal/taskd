// The e2e entry point for the test kit. An extension writes:
//
//   import { defineExtensionE2E, row, seed } from "<path-to>/web/testkit/e2e";
//   const { test, expect } = defineExtensionE2E("myext", {
//     waitFor: waitForSources("myext"),   // optional: wait for the syncer
//   });
//   test("my presenter renders", async ({ page, api }) => { ... });
//
// It gets a `taskd` with its extension staged, a frozen clock, live seeding
// via `api`, and the zero-console-error invariant — the same rig core uses.

import { test as base, expect, type Page } from "@playwright/test";
import {
  startDaemon,
  FIXED_NOW,
  type Api,
  type Daemon,
  type ExtensionEntry,
} from "./daemon";

export * from "./helpers";
export {
  FIXED_NOW,
  daysFromNow,
  waitForSources,
  resolveExtension,
  ROOT,
  WEB,
  type Api,
  type Task,
  type Daemon,
  type ExtensionEntry,
} from "./daemon";
export { expect };

/** Freeze the clock, collect console errors, open the app in test mode, and
 *  wait until the replica is live (so keyboard effects are attached and seeds
 *  will stream in). Shared by core and extension fixtures. */
export async function gotoApp(page: Page, baseURL: string, consoleErrors: string[]): Promise<void> {
  await page.clock.setFixedTime(FIXED_NOW);
  page.on("console", (m) => {
    if (m.type() === "error") consoleErrors.push(m.text());
  });
  page.on("pageerror", (e) => consoleErrors.push(String(e)));
  await page.goto(`${baseURL}/?test=1&view=all`);
  await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible({
    timeout: 15_000,
  });
}

export interface DaemonFixtures {
  /** Assert the app logged no console errors on teardown (default true). */
  expectNoConsoleErrors: boolean;
  daemon: Daemon;
  api: Api;
  consoleErrors: string[];
}

/** Build a Playwright `test` whose daemon stages the given extensions. The
 *  extension set + readiness gate are captured in the closure (not exposed as
 *  Playwright options — a function-valued option default breaks its fixture
 *  analysis). */
export function makeDaemonTest(defaults: {
  extensions?: ExtensionEntry[];
  waitFor?: (api: Api) => Promise<void>;
} = {}) {
  const extensions = defaults.extensions ?? [];
  const waitFor = defaults.waitFor;
  const test = base.extend<DaemonFixtures>({
    expectNoConsoleErrors: [true, { option: true }],

    daemon: async ({}, use) => {
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
  return { test, expect };
}

/** Convenience for the common case: one (or a few) extensions, staged. */
export function defineExtensionE2E(
  entry: ExtensionEntry | ExtensionEntry[],
  opts: { waitFor?: (api: Api) => Promise<void> } = {},
) {
  return makeDaemonTest({
    extensions: Array.isArray(entry) ? entry : [entry],
    waitFor: opts.waitFor,
  });
}
