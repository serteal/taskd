import { test, expect } from "./fixtures";
import { row, seed, dialog, openPalette } from "./helpers";

// Dependency-free accessibility checks: modal dialogs are labelled and marked
// aria-modal, and the primary controls expose accessible names.
test.describe("accessibility", () => {
  test("modal surfaces are labelled and aria-modal", async ({ page }) => {
    await page.keyboard.press("q");
    const newTask = dialog(page, "New task");
    await expect(newTask).toHaveAttribute("aria-modal", "true");
    await page.keyboard.press("Escape");

    await openPalette(page);
    await expect(dialog(page, "Command palette")).toHaveAttribute("aria-modal", "true");
    await page.keyboard.press("Escape");

    await page.keyboard.press("?");
    await expect(dialog(page, "Keyboard shortcuts")).toHaveAttribute("aria-modal", "true");
  });

  test("primary controls have accessible names", async ({ page, api }) => {
    // The header View-options menu and the sidebar add are reachable by name.
    await expect(page.getByRole("button", { name: "View options" })).toBeVisible();
    await expect(page.getByRole("button", { name: /Add task/ })).toBeVisible();

    // Row + detail controls are named for assistive tech.
    await seed(api, [{ title: "Reachable" }]);
    await expect(
      row(page, "Reachable").getByRole("button", { name: "Complete Reachable" }),
    ).toBeVisible();

    await page.getByText("Reachable", { exact: true }).click();
    await expect(page.getByRole("button", { name: "Close details" })).toBeVisible();
    await expect(page.getByRole("textbox", { name: "Title" })).toBeVisible();
  });

  test("decorative icons are hidden from assistive tech", async ({ page }) => {
    const svgs = page.getByRole("button", { name: /Add task/ }).locator("svg[aria-hidden='true']");
    await expect(svgs.first()).toBeVisible();
  });
});
