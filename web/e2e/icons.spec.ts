import { test, expect } from "./fixtures";
import { row, rows, seed, dialog } from "./helpers";

test.describe("icons", () => {
  test("core controls render inline SVGs (not emoji)", async ({ page, api }) => {
    // Sidebar "Add task" button carries the plus icon: an aria-hidden,
    // currentColor-stroked svg.
    const addSvg = page.getByRole("button", { name: /Add task/ }).locator("svg").first();
    await expect(addSvg).toBeVisible();
    await expect(addSvg).toHaveAttribute("aria-hidden", "true");
    await expect(addSvg).toHaveAttribute("stroke", "currentColor");

    // A task row's complete control is an svg check.
    await seed(api, [{ title: "Icon me" }]);
    await expect(
      row(page, "Icon me").getByRole("button", { name: "Complete Icon me" }).locator("svg"),
    ).toHaveCount(1);

    // The new-task overlay pills are icon buttons.
    await page.keyboard.press("q");
    await expect(dialog(page, "New task").getByRole("button", { name: "Schedule" }).locator("svg")).toBeVisible();
  });
});

test.describe("icons (extensions)", () => {
  test.use({ mode: "extensions" });

  test("a github row renders its own state icon", async ({ page }) => {
    // Synced items are local-by-default hidden from the built-in lists; they
    // live in their Source view. Open it, then inspect a github row.
    await page.locator('[data-testid="side-item"][data-label="github"]').click();
    // Presenter icon (github octicon) + the core complete check → at least 2 svgs.
    const ghRow = rows(page).filter({ hasText: "#" }).first();
    await expect(ghRow).toBeVisible();
    expect(await ghRow.locator("svg").count()).toBeGreaterThanOrEqual(2);
  });
});
