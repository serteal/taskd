import { test, expect, daysFromNow } from "./fixtures";
import { rows, row, seed, dragReorder } from "./helpers";

const sortSelect = (page: import("@playwright/test").Page) => page.locator("header select");

test.describe("sort & grouping", () => {
  test("smart sort groups by time pressure", async ({ page, api }) => {
    await seed(api, [
      { title: "Was due", due: daysFromNow(-1) },
      { title: "Due today", due: daysFromNow(0) },
      { title: "Due tomorrow", due: daysFromNow(1) },
      { title: "Due later", due: daysFromNow(10) },
    ]);
    await expect(rows(page)).toHaveCount(4);

    for (const heading of ["Overdue", "Today", "Tomorrow", "Later"]) {
      await expect(page.getByRole("heading", { name: heading })).toBeVisible();
    }
  });

  test("title sort orders alphabetically within a group", async ({ page, api }) => {
    await seed(api, [{ title: "Banana" }, { title: "Apple" }, { title: "Cherry" }]);
    await expect(rows(page)).toHaveCount(3);

    await sortSelect(page).selectOption("title");
    await expect(rows(page).nth(0)).toContainText("Apple");
    await expect(rows(page).nth(1)).toContainText("Banana");
    await expect(rows(page).nth(2)).toContainText("Cherry");
  });

  test("dragging to reorder switches sort to Manual", async ({ page, api }) => {
    // Distinct due dates give a deterministic smart order (A, B, C) — tasks
    // with tied create-times would otherwise sort by unstable arrival order.
    await seed(api, [
      { title: "A", due: daysFromNow(1) },
      { title: "B", due: daysFromNow(2) },
      { title: "C", due: daysFromNow(3) },
    ]);
    await expect(rows(page)).toHaveCount(3);
    await expect(sortSelect(page)).toHaveValue("smart");
    await expect(rows(page).first()).toContainText("A");

    // Drag the last row (C) to the top — a reorder from a non-manual sort.
    await dragReorder(page, "C", "A", false);

    await expect(sortSelect(page)).toHaveValue("manual");
    await expect(rows(page).first()).toContainText("C");
  });
});
