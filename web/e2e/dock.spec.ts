import { test, expect } from "./fixtures";
import { row, seed } from "./helpers";

test.describe("right dock", () => {
  test.use({ mode: "extensions" });

  test("the panel toggle button opens and closes the calendar rail", async ({ page }) => {
    const rail = page.getByTestId("calendar-rail");
    await expect(rail).toBeVisible(); // defaultOpen

    const toggle = page.locator("header").getByRole("button", { name: "Today" });
    await toggle.click();
    await expect(rail).toBeHidden();
    await toggle.click();
    await expect(rail).toBeVisible();
  });

  test("t key toggles the first panel", async ({ page }) => {
    const rail = page.getByTestId("calendar-rail");
    await expect(rail).toBeVisible();
    await page.keyboard.press("t");
    await expect(rail).toBeHidden();
    await page.keyboard.press("t");
    await expect(rail).toBeVisible();
  });

  test("opening a task shows the detail column; close hides it", async ({ page, api }) => {
    await seed(api, [{ title: "Peek me" }]);
    await expect(row(page, "Peek me")).toBeVisible();

    await page.getByText("Peek me", { exact: true }).click();
    await expect(page.getByTestId("detail-panel")).toBeVisible();

    await page.getByRole("button", { name: "Close details" }).click();
    await expect(page.getByTestId("detail-panel")).toBeHidden();
  });
});
