import { defineConfig } from "@playwright/test";

// End-to-end tests run against a real `taskd` (webui build) spawned per test
// by the `daemon` fixture (see e2e/fixtures.ts). globalSetup builds the
// current bundle + daemon so tests never hit a stale artifact.
export default defineConfig({
  // Discover core specs (web/e2e) and any extension specs (extensions/*/e2e),
  // which drive the app through the shared test kit (web/testkit).
  testDir: "..",
  testMatch: ["web/e2e/**/*.spec.ts", "extensions/**/e2e/**/*.spec.ts"],
  globalSetup: "./e2e/global-setup.ts",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: [["list"]],
  timeout: 30_000,
  expect: { timeout: 8_000 },
  use: {
    trace: "on-first-retry",
    reducedMotion: "reduce",
    viewport: { width: 1280, height: 800 },
    // Pin the zone so day-bucketing and calendar-block positions are the same
    // on every machine (FIXED_NOW is noon UTC; seeded times are mid-band UTC).
    timezoneId: "UTC",
  },
  projects: [{ name: "chromium", use: { browserName: "chromium" } }],
});
