import { test, expect } from "./fixtures";
import { seed } from "./helpers";

test.describe("left sidebar collapse", () => {
  test("collapsing hides the nav to an icon rail; expanding restores it; state persists across a reload", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Anchor" }]);
    await expect(page.getByTestId("side-item").first()).toBeVisible();

    await page.getByRole("button", { name: "Collapse sidebar" }).click();
    await expect(page.getByTestId("sidebar-collapsed")).toBeVisible();
    await expect(page.getByTestId("side-item")).toHaveCount(0);

    // Still usable while collapsed — Add task works from the rail.
    await page.getByRole("button", { name: "Add task", exact: true }).click();
    await expect(page.getByRole("dialog", { name: "New task" })).toBeVisible();
    await page.keyboard.press("Escape");

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    await expect(page.getByTestId("sidebar-collapsed")).toBeVisible();

    await page.getByRole("button", { name: "Expand sidebar" }).click();
    await expect(page.getByTestId("side-item").first()).toBeVisible();
    await expect(page.getByTestId("sidebar-collapsed")).toHaveCount(0);
  });
});
