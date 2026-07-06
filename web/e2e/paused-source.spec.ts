import { test, expect } from "./fixtures";

// A source whose extension is disabled is "paused": its syncer is stopped, so
// its feed no longer updates. Settings, the sidebar, and the source view share
// one extension-state store (lib/admin.ts), so toggling in Settings surfaces
// the paused pill and banner live — no reload.

test.describe("paused sources", () => {
  test.use({ mode: "extensions" });

  // gcal's mock syncs into source "gcal:personal"; extension "gcal" owns it via
  // the name / name-prefix rule.
  const SOURCE = "gcal:personal";
  const sourceItem = (page: import("@playwright/test").Page) =>
    page.locator(`[data-testid="side-item"][data-label="${SOURCE}"]`);
  const gcalToggle = (page: import("@playwright/test").Page) =>
    page.locator('[data-testid="ext-toggle"][data-ext="gcal"]');

  test("disabling the extension paints the sidebar pill and the source banner, live; re-enabling clears them", async ({
    page,
  }) => {
    // Present and unpaused to start.
    await expect(sourceItem(page)).toBeVisible();
    await expect(sourceItem(page)).not.toHaveAttribute("data-paused", "true");

    // Disable gcal from Settings.
    await page.getByTestId("open-settings").click();
    await gcalToggle(page).click();
    await expect(gcalToggle(page)).toHaveAttribute("aria-checked", "false");
    await page.keyboard.press("Escape");

    // The sidebar pill + dimmed count appear without a reload (shared store).
    await expect(sourceItem(page)).toHaveAttribute("data-paused", "true");
    await expect(sourceItem(page).getByTestId("source-paused-pill")).toBeVisible();

    // Opening the source view surfaces the pause banner.
    await sourceItem(page).click();
    const banner = page.getByTestId("source-paused-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toContainText("no longer syncing");

    // The banner's inline action opens Settings, where we re-enable it.
    await banner.getByRole("button", { name: "Re-enable it in Settings" }).click();
    await expect(page.getByTestId("settings")).toBeVisible();
    await gcalToggle(page).click();
    await expect(gcalToggle(page)).toHaveAttribute("aria-checked", "true");
    await page.keyboard.press("Escape");

    // Both the banner and the sidebar pill are gone.
    await expect(banner).toBeHidden();
    await expect(sourceItem(page)).not.toHaveAttribute("data-paused", "true");
    await expect(sourceItem(page).getByTestId("source-paused-pill")).toBeHidden();
  });
});
