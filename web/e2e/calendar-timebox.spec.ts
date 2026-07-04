import { test, expect } from "./fixtures";
import { row, seed, dragTo } from "./helpers";

// The calendar rail is an extension surface, so these run with extensions on.
test.use({ mode: "extensions" });

test.describe("calendar timebox", () => {
  test("dragging a task onto the rail timeboxes it — and does NOT switch sort", async ({ page, api }) => {
    await seed(api, [{ title: "Plan the day" }]);
    await expect(row(page, "Plan the day")).toBeVisible();
    await expect(page.getByTestId("calendar-rail")).toBeVisible();
    await expect(page.locator("header select")).toHaveValue("smart");

    await dragTo(page, "Plan the day", '[data-testid="cal-daycolumn"]', { clientY: 400 });

    // A timebox block renders in the rail…
    await expect(page.getByTestId("cal-timebox")).toHaveCount(1);
    // …the timebox persisted to user_data…
    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === "Plan the day")?.userData)
      .toHaveProperty("timebox");
    // …and, crucially, the list sort stays smart (calendar drops are not reorders).
    await expect(page.locator("header select")).toHaveValue("smart");
  });

  test("clearing a timebox removes the block", async ({ page, api }) => {
    await seed(api, [{ title: "Plan the day" }]);
    await expect(row(page, "Plan the day")).toBeVisible();

    await dragTo(page, "Plan the day", '[data-testid="cal-daycolumn"]', { clientY: 400 });
    await expect(page.getByTestId("cal-timebox")).toHaveCount(1);

    await page.getByRole("button", { name: "Remove timebox" }).click();

    await expect(page.getByTestId("cal-timebox")).toHaveCount(0);
    await expect
      .poll(async () => (await api.listAll()).find((t) => t.title === "Plan the day")?.userData ?? {})
      .not.toHaveProperty("timebox");
  });
});
