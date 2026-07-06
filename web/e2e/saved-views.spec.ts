import { test, expect } from "./fixtures";
import { runCommand } from "./helpers";

test.describe("saved views", () => {
  test("save from the palette via the naming modal, then remove", async ({ page }) => {
    // "Save current view…" now opens a modal dialog (matching FilterBuilder's
    // styling) instead of a native window.prompt.
    await runCommand(page, "Save current view");
    const dlg = page.getByTestId("save-view-dialog");
    await expect(dlg).toBeVisible();
    await dlg.getByRole("textbox", { name: "View name" }).fill("Focus");
    await dlg.getByRole("button", { name: "Save" }).click();
    await expect(dlg).toBeHidden();

    const section = page.getByTestId("sidebar-section-views");
    await expect(section.getByTestId("saved-view")).toContainText("Focus");

    // Applying it is a no-op navigation here; just confirm it's clickable.
    await section.getByRole("button", { name: "Focus" }).click();

    await section.getByRole("button", { name: "Remove view Focus" }).click();
    await expect(page.getByTestId("saved-view")).toHaveCount(0);
  });

  test("Enter saves from the name field; Escape cancels without saving", async ({ page }) => {
    // Escape closes the modal and saves nothing.
    await runCommand(page, "Save current view");
    let dlg = page.getByTestId("save-view-dialog");
    await dlg.getByRole("textbox", { name: "View name" }).fill("Abandoned");
    await page.keyboard.press("Escape");
    await expect(dlg).toBeHidden();
    await expect(page.getByTestId("saved-view")).toHaveCount(0);

    // Enter in the name field saves.
    await runCommand(page, "Save current view");
    dlg = page.getByTestId("save-view-dialog");
    const input = dlg.getByRole("textbox", { name: "View name" });
    await input.fill("Persisted");
    await input.press("Enter");
    await expect(dlg).toBeHidden();
    await expect(page.getByTestId("saved-view")).toContainText("Persisted");
  });

  test("persists across a reload", async ({ page }) => {
    await runCommand(page, "Save current view");
    const dlg = page.getByTestId("save-view-dialog");
    await dlg.getByRole("textbox", { name: "View name" }).fill("Persisted");
    await dlg.getByRole("button", { name: "Save" }).click();
    await expect(page.getByTestId("saved-view")).toContainText("Persisted");

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await expect(page.getByTestId("saved-view")).toContainText("Persisted");
  });
});
