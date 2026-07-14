import { test, expect } from "./fixtures";
import { row, seed } from "./helpers";

test.describe("right dock", () => {
  test.use({ mode: "extensions" });

  test("the panel toggle button opens and closes the calendar rail", async ({ page }) => {
    const rail = page.getByTestId("calendar-rail");
    await expect(rail).toBeVisible(); // defaultOpen

    // The button is now a generic icon (not the extension's own title as
    // literal text) — same phrasing the command palette already uses for
    // this action ("Toggle today panel").
    const toggle = page.locator("header").getByRole("button", { name: "Toggle today panel" });
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

  // The detail surface is a modal overlay now — it floats above everything
  // rather than competing with the extension panels for the right-hand slot,
  // so the panel stays mounted and visible the whole time.
  test("opening a task's detail leaves an open panel in place (detail is a modal)", async ({
    page,
    api,
  }) => {
    await seed(api, [{ title: "Peek me" }]);
    const rail = page.getByTestId("calendar-rail");
    await expect(rail).toBeVisible(); // defaultOpen

    await page.getByText("Peek me", { exact: true }).click();
    await expect(page.getByTestId("detail-panel")).toBeVisible();
    await expect(rail).toBeVisible();

    await page.getByRole("button", { name: "Close details" }).click();
    await expect(page.getByTestId("detail-panel")).toBeHidden();
    await expect(rail).toBeVisible();
  });
});
