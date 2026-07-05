import { test, expect, daysFromNow } from "./fixtures";
import { rows, seed, dragReorder } from "./helpers";

// The sort choice now lives in the header's "View options" menu (a popover of
// radios), and the header exposes the active choice via a data attribute.
const header = (page: import("@playwright/test").Page) => page.locator("header");
const openViewMenu = (page: import("@playwright/test").Page) =>
  header(page).getByRole("button", { name: "View options" }).click();
const chooseSort = async (page: import("@playwright/test").Page, label: string) => {
  await openViewMenu(page);
  await page.getByRole("radio", { name: label, exact: true }).click();
  await page.keyboard.press("Escape"); // close the popover
};

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

    await chooseSort(page, "Title");
    await expect(header(page)).toHaveAttribute("data-view-sort", "title");
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
    await expect(header(page)).toHaveAttribute("data-view-sort", "smart");
    await expect(rows(page).first()).toContainText("A");

    // Drag the last row (C) to the top — a reorder from a non-manual sort.
    await dragReorder(page, "C", "A", false);

    await expect(header(page)).toHaveAttribute("data-view-sort", "manual");
    await expect(rows(page).first()).toContainText("C");
  });

  test("a chosen sort survives a reload", async ({ page, api }) => {
    await seed(api, [{ title: "Banana" }, { title: "Apple" }]);
    await expect(rows(page)).toHaveCount(2);

    await chooseSort(page, "Title");
    await expect(header(page)).toHaveAttribute("data-view-sort", "title");

    await page.reload();
    await expect(page.locator('[data-testid="conn-status"][data-connected="true"]')).toBeVisible();
    // The per-view layout is sticky: Title sort is restored, not reset to Smart.
    await expect(header(page)).toHaveAttribute("data-view-sort", "title");
  });
});
