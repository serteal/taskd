import { test, expect } from "./fixtures";
import { row, rows, seed } from "./helpers";

const gotoCompleted = (page: import("@playwright/test").Page) =>
  page.locator('[data-testid="side-item"][data-label="Completed"]').click();

test.describe("completed view", () => {
  test("shows the empty archive message when nothing is done", async ({ page }) => {
    await gotoCompleted(page);
    await expect(page.getByText("Nothing completed yet.")).toBeVisible();
  });

  test("a completed task appears in the archive", async ({ page, api }) => {
    await seed(api, [{ title: "Done me" }]);
    await expect(rows(page)).toHaveCount(1);

    await row(page, "Done me").getByRole("button", { name: "Complete Done me" }).click();
    await expect(rows(page)).toHaveCount(0); // gone from the active list

    await gotoCompleted(page);
    await expect(page.getByText("Done me")).toBeVisible();
  });
});
