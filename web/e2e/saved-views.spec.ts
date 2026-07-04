import { test, expect } from "./fixtures";
import { runCommand } from "./helpers";

test.describe("saved views", () => {
  test("save from the palette, then remove", async ({ page }) => {
    page.on("dialog", (d) => d.accept("Focus")); // window.prompt("Save this view as:")
    await runCommand(page, "Save current view");

    const section = page.getByTestId("sidebar-section-views");
    await expect(section.getByTestId("saved-view")).toContainText("Focus");

    // Applying it is a no-op navigation here; just confirm it's clickable.
    await section.getByRole("button", { name: "Focus" }).click();

    await section.getByRole("button", { name: "Remove view Focus" }).click();
    await expect(page.getByTestId("saved-view")).toHaveCount(0);
  });

  test("persists across a reload", async ({ page }) => {
    page.on("dialog", (d) => d.accept("Persisted"));
    await runCommand(page, "Save current view");
    await expect(page.getByTestId("saved-view")).toContainText("Persisted");

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await expect(page.getByTestId("saved-view")).toContainText("Persisted");
  });
});
